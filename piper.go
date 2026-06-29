package piper

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"gopkg.in/yaml.v3"

	iagent "github.com/piper/piper/internal/agent"
	"github.com/piper/piper/internal/artifact"
	"github.com/piper/piper/internal/event"
	"github.com/piper/piper/internal/grpcagent"
	"github.com/piper/piper/internal/logstore"
	"github.com/piper/piper/internal/pipelinedispatch"
	"github.com/piper/piper/internal/proto"
	"github.com/piper/piper/internal/queue"
	ischeduler "github.com/piper/piper/internal/scheduler"
	"github.com/piper/piper/internal/srcfetch"
	"github.com/piper/piper/pkg/connection"
	"github.com/piper/piper/pkg/notebook"
	notebookdispatch "github.com/piper/piper/pkg/notebook/dispatch"
	"github.com/piper/piper/pkg/pipeline"
	"github.com/piper/piper/pkg/pipeline/run"
	worker "github.com/piper/piper/pkg/pipeline/worker"
	"github.com/piper/piper/pkg/project"
	"github.com/piper/piper/pkg/secret"
	"github.com/piper/piper/pkg/serving"
	servingdispatch "github.com/piper/piper/pkg/serving/dispatch"
	"github.com/piper/piper/pkg/storage"

	storemod "github.com/piper/piper/internal/store"
)

// servingBundle groups the serving manager and proxy together.
type servingBundle struct {
	manager *serving.Manager
	proxy   *serving.Proxy
}

// Piper is the library entry point.
// Embed it in projects such as data-voyager.
//
//	p := piper.New(piper.DefaultConfig())
//	result, err := p.RunFile(ctx, "train.yaml")
type Piper struct {
	cfg             Config
	ctx             context.Context // cancelled on Close; passed to background goroutines and hooks
	repos           *storemod.Repos
	logs            logstore.LogStore
	metrics         logstore.MetricStore
	queue           *queue.Queue
	serving         servingBundle
	notebookManager *notebook.Manager
	agentRegistry   *iagent.Registry
	workloadRouter  *iagent.Router
	grpcAgentServer *grpcagent.Server
	store           storage.Store // nil when no artifact store configured
	secrets         *secret.Store
	connections     *connection.Store
	storageURL      string            // resolved storage URL (for K8s launcher, artifact resolver)
	storageErr      error             // last artifact store open error, if any
	resolver        artifact.Resolver // central artifact resolver
	backend         pipelinedispatch.ExecutionBackend
	events          *event.Hub
	scheduler       *ischeduler.Scheduler
	startedAt       time.Time // wall-clock when New() ran; used for misfire detection

	stopCtx context.CancelFunc // cancels ctx on Close
	wg      sync.WaitGroup
}

func New(cfg Config) (*Piper, error) {
	def := DefaultConfig()
	if cfg.OutputDir == "" {
		cfg.OutputDir = def.OutputDir
	}
	if cfg.Server.Addr == "" {
		cfg.Server.Addr = def.Server.Addr
	}
	if cfg.Schedule.MisfirePolicy == "" {
		cfg.Schedule.MisfirePolicy = def.Schedule.MisfirePolicy
	}
	if cfg.Schedule.MisfireGracePeriod == 0 {
		cfg.Schedule.MisfireGracePeriod = def.Schedule.MisfireGracePeriod
	}
	if persistedStorage, ok, err := loadStorageSettings(filepath.Join(cfg.OutputDir, "storage.yaml"), cfg.Storage); err != nil {
		return nil, fmt.Errorf("load storage settings: %w", err)
	} else if ok {
		cfg.Storage = persistedStorage
	}
	if err := cfg.Validate(); err != nil {
		return nil, err
	}

	if err := os.MkdirAll(cfg.OutputDir, 0755); err != nil {
		return nil, fmt.Errorf("create output dir: %w", err)
	}

	repos, err := openStore(cfg)
	if err != nil {
		return nil, fmt.Errorf("open store: %w", err)
	}
	if err := ensureDefaultProject(context.Background(), repos.Project); err != nil {
		_ = repos.Close()
		return nil, fmt.Errorf("ensure default project: %w", err)
	}
	secretKey := cfg.Server.SecretEncryptionKey
	if secretKey == "" {
		secretKey = "sha256:piper-dev-insecure-key-change-in-production"
		slog.Warn("server.secret_encryption_key is not set — using an insecure dev key; set it before production use")
	}
	secretStore, err := secret.NewStore(repos.Secret, secretKey)
	if err != nil {
		_ = repos.Close()
		return nil, fmt.Errorf("create secret store: %w", err)
	}
	connectionStore, err := connection.NewStoreFromKey(repos.Connection, secretKey)
	if err != nil {
		_ = repos.Close()
		return nil, fmt.Errorf("create connection store: %w", err)
	}
	if cfg.Auth.Factory != nil {
		authConfig, err := cfg.Auth.Factory(AuthDependencies{
			DB:            repos.DB(),
			Driver:        repos.Driver(),
			SecureCookies: cfg.Server.TLS.Enabled,
			Executor:      repos.Executor(),
		})
		if err != nil {
			_ = repos.Close()
			return nil, fmt.Errorf("create auth capabilities: %w", err)
		}
		if authConfig.Factory != nil {
			_ = repos.Close()
			return nil, fmt.Errorf("create auth capabilities: nested factory is not allowed")
		}
		cfg.Auth = authConfig
		if err := cfg.Validate(); err != nil {
			_ = repos.Close()
			return nil, err
		}
	}
	modelDir := cfg.Serving.ModelDir
	if modelDir == "" {
		modelDir = filepath.Join(cfg.OutputDir, "models")
	}
	if err := os.MkdirAll(modelDir, 0755); err != nil {
		return nil, fmt.Errorf("create model dir: %w", err)
	}

	agentReg := iagent.NewRegistry()
	workloadRouter := iagent.NewRouter(agentReg)

	grpcSrv := grpcagent.NewServer(
		func(reg grpcagent.Registration) {
			info := iagent.Info{
				ID:             reg.ID,
				Infrastructure: reg.Infrastructure,
				Hostname:       reg.Hostname,
				Capabilities:   reg.Capabilities,
				ClusterName:    reg.ClusterName,
				Labels:         reg.Labels,
			}
			// Extract capacity encoded in Labels by grpcagent.Client.
			if c := reg.Labels["capacity"]; c != "" {
				if n, err := strconv.Atoi(c); err == nil {
					info.Capacity = n
				}
			}
			agentReg.Register(info)
		},
		agentReg.Remove,
	)

	servingDriver := servingdispatch.NewAgentDriver(workloadRouter, grpcSrv, repos.Serving, repos.WorkerPodPolicy).
		WithEnvResolver(secretStore.ResolveEnv)
	servingMgr := serving.New(repos.Serving, servingDriver)

	nbDriver := notebook.Driver(notebookdispatch.NewAgentDriver(workloadRouter, grpcSrv, repos.Notebook, repos.WorkerPodPolicy).
		WithEnvResolver(secretStore.ResolveEnv))
	nbMgr := notebook.New(repos.Notebook, repos.NotebookVolume, nbDriver)
	bgCtx, stopFn := context.WithCancel(context.Background())
	q := queue.NewQueue(bgCtx, repos.Run, repos.Step)
	grpcSrv.SetPushHandler(newWorkerPushHandler(nbMgr, servingMgr, q, grpcSrv, repos.Log, repos.Metric))
	// On agent (re)connect: sync notebook status so master DB catches up on any
	// state changes that occurred while the connection was down.
	grpcSrv.SetConnectHandler(func(agentID string) {
		nbMgr.SyncAgent(context.Background(), agentID)
		servingMgr.SyncAgent(context.Background(), agentID)
	})

	p := &Piper{
		cfg:         cfg,
		ctx:         bgCtx,
		repos:       repos,
		logs:        repos.Log,
		metrics:     repos.Metric,
		secrets:     secretStore,
		connections: connectionStore,
		queue:       q,
		serving: servingBundle{
			manager: servingMgr,
			proxy:   serving.NewProxy(repos.Serving),
		},
		notebookManager: nbMgr,
		agentRegistry:   agentReg,
		workloadRouter:  workloadRouter,
		grpcAgentServer: grpcSrv,
		stopCtx:         stopFn,
		events:          event.NewHub(),
	}
	storageURL := resolveStorageURL(cfg)
	if storageURL != "" {
		if st, err := storage.Open(storageURL, cfg.Storage.Token); err != nil {
			slog.Warn("artifact store unavailable", "url", storageURL, "err", err)
			p.storageErr = err
		} else {
			p.store = st
			p.storageURL = storageURL
		}
	}
	q.SetStorageConfig(p.storageURL, cfg.Storage.Token)
	p.resolver = &piperArtifactResolver{
		runRepo:    repos.Run,
		outputDir:  cfg.OutputDir,
		storageURL: p.storageURL,
	}
	// Pipeline tasks are delivered only through gRPC-connected agents.
	p.SetBackend(pipelinedispatch.NewAgentBackend(workloadRouter, p.grpcAgentServer, repos.WorkerPodPolicy))
	q.OnRunSuccess = p.handleRunSuccess
	q.SetEventPublisher(p.events)
	p.serving.manager.SetEventPublisher(p.events)
	p.notebookManager.SetEventPublisher(p.events)
	p.recoverInterruptedRuns(context.Background())

	// Start the in-memory scheduler and seed it from the DB.
	// startedAt is set before LoadFromRepo so misfire detection works on first Add.
	p.startedAt = time.Now().UTC()
	p.scheduler = ischeduler.New(p.scheduleFired)
	p.scheduler.Start()
	if err := ischeduler.LoadFromRepo(context.Background(), p.repos.Schedule, p.scheduler); err != nil {
		slog.Warn("load schedules from repo failed", "err", err)
	}

	p.wg.Add(1)
	go func() {
		defer p.wg.Done()
		p.runCleanup(p.ctx)
	}()
	return p, nil
}

func ensureDefaultProject(ctx context.Context, repo project.Repository) error {
	existing, err := repo.Get(ctx, project.DefaultID)
	if err != nil {
		return err
	}
	if existing != nil {
		return nil
	}
	return repo.Create(ctx, &project.Project{
		ID:          project.DefaultID,
		Name:        "Default",
		Description: "Default project",
	})
}

// handleRunSuccess is called (in a goroutine) when a queued run completes successfully.
// It triggers on_success.deploy if configured in the pipeline spec.
func (p *Piper) handleRunSuccess(ctx context.Context, runID string, pl *pipeline.Pipeline) {
	if pl.Spec.OnSuccess == nil || pl.Spec.OnSuccess.Deploy == nil {
		return
	}
	trigger := pl.Spec.OnSuccess.Deploy
	projectContext, _ := project.FromContext(ctx)
	svc, err := p.repos.Serving.Get(ctx, projectContext.ID, trigger.Service)
	if err != nil || svc == nil {
		return
	}
	if svc.YAML == "" {
		return
	}
	// Re-deploy with the new run's artifact
	var ms serving.ModelService
	if err := yaml.Unmarshal([]byte(svc.YAML), &ms); err != nil {
		return
	}
	if ms.Spec.Model.FromArtifact != nil {
		ms.Spec.Model.FromArtifact.Run = runID
	}
	updatedYAML, _ := yaml.Marshal(ms)
	if _, err := p.DeployService(ctx, projectContext.ID, updatedYAML); err != nil {
		slog.Warn("auto-deploy on run success failed", "run_id", runID, "service", trigger.Service, "err", err)
	}
}

// runCleanup periodically reconciles workers and removes stuck queue entries.
func (p *Piper) runCleanup(ctx context.Context) {
	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			p.reconcileBackend(ctx)
			p.queue.Cleanup(ctx, 4*time.Hour)
			p.cleanupRetention(ctx)
		}
	}
}

type jobReconciler interface {
	ReconcileJobs(ctx context.Context, report func(context.Context, proto.TaskResult) error)
}

func (p *Piper) reconcileBackend(ctx context.Context) {
	reconciler, ok := p.backend.(jobReconciler)
	if !ok {
		return
	}
	reconciler.ReconcileJobs(ctx, func(ctx context.Context, result proto.TaskResult) error {
		return p.queue.Complete(ctx, result)
	})
}

func (p *Piper) listRunsAcrossProjects(ctx context.Context, filter run.RunFilter) ([]*run.Run, error) {
	projects, err := p.repos.Project.List(ctx)
	if err != nil {
		return nil, err
	}
	var runs []*run.Run
	for _, projectRecord := range projects {
		projectRuns, err := p.repos.Run.List(ctx, projectRecord.ID, filter)
		if err != nil {
			return nil, err
		}
		runs = append(runs, projectRuns...)
	}
	return runs, nil
}

func (p *Piper) recoverInterruptedRuns(ctx context.Context) {
	runs, err := p.listRunsAcrossProjects(ctx, run.RunFilter{Status: run.StatusRunning})
	if err != nil {
		slog.Warn("recover running runs failed", "err", err)
		return
	}
	now := time.Now().UTC()
	for _, r := range runs {
		if r.PipelineYAML == "" {
			// No YAML — can't reconstruct DAG, mark failed.
			if err := p.repos.Run.UpdateStatus(ctx, r.ProjectID, r.ID, run.StatusFailed, &now); err != nil {
				slog.Warn("recover run failed", "run_id", r.ID, "err", err)
			}
			continue
		}
		pl, err := p.Parse([]byte(r.PipelineYAML))
		if err != nil {
			slog.Warn("recover: parse pipeline failed", "run_id", r.ID, "err", err)
			_ = p.repos.Run.UpdateStatus(ctx, r.ProjectID, r.ID, run.StatusFailed, &now)
			continue
		}
		dag, err := pipeline.BuildDAG(pl)
		if err != nil {
			slog.Warn("recover: build dag failed", "run_id", r.ID, "err", err)
			_ = p.repos.Run.UpdateStatus(ctx, r.ProjectID, r.ID, run.StatusFailed, &now)
			continue
		}
		steps, _ := p.repos.Step.List(ctx, r.ProjectID, r.ID)
		var recovered []queue.RecoveredStep
		for _, s := range steps {
			switch s.Status {
			case "done", "skipped":
				recovered = append(recovered, queue.RecoveredStep{Name: s.StepName, Done: true})
			case "running":
				startedAt := now
				if s.StartedAt != nil {
					startedAt = *s.StartedAt
				}
				recovered = append(recovered, queue.RecoveredStep{Name: s.StepName, StartedAt: startedAt})
			}
		}
		var params map[string]any
		if r.ParamsJSON != "" {
			_ = json.Unmarshal([]byte(r.ParamsJSON), &params)
		}
		outputDir := filepath.Join(p.cfg.OutputDir, r.ID)
		envByStep, err := p.resolvePipelineSecretEnv(ctx, r.ProjectID, pl)
		if err != nil {
			slog.Warn("recover: resolve secret env failed", "run_id", r.ID, "err", err)
			_ = p.repos.Run.UpdateStatus(ctx, r.ProjectID, r.ID, run.StatusFailed, &now)
			continue
		}
		p.queue.RecoverWithEnv(ctx, r.ProjectID, pl, dag, r.ID, ".", outputDir, proto.BuiltinVars{ScheduledAt: r.ScheduledAt}, params, recovered, envByStep)
	}
}

func (p *Piper) cleanupRetention(ctx context.Context) {
	runTTL := p.cfg.Retention.RunTTL
	artifactTTL := p.cfg.Retention.ArtifactTTL
	if runTTL > 0 || artifactTTL > 0 {
		runs, err := p.listRunsAcrossProjects(ctx, run.RunFilter{})
		if err != nil {
			slog.Warn("retention list runs failed", "err", err)
		} else {
			now := time.Now().UTC()
			for _, r := range runs {
				if r.EndedAt == nil || r.Status == run.StatusRunning || r.Status == run.StatusScheduled {
					continue
				}
				if runTTL > 0 && r.EndedAt.Before(now.Add(-runTTL)) {
					if err := p.deleteRunWithArtifacts(project.WithContext(ctx, project.Context{ID: r.ProjectID}), r.ID); err != nil {
						slog.Warn("retention delete run failed", "run_id", r.ID, "err", err)
					}
					continue
				}
				if artifactTTL > 0 && r.EndedAt.Before(now.Add(-artifactTTL)) {
					if err := deleteArtifacts(ctx, p.store, p.cfg.OutputDir, r.ID); err != nil {
						slog.Warn("retention delete artifacts failed", "run_id", r.ID, "err", err)
					}
				}
			}
		}
	}
	p.cleanupScheduleRetention(ctx)
}

func (p *Piper) cleanupScheduleRetention(ctx context.Context) {
	schedules, err := p.repos.Schedule.ListWithMaxRuns(ctx)
	if err != nil {
		slog.Warn("retention list schedules with max_runs failed", "err", err)
		return
	}
	for _, sc := range schedules {
		// List returns runs newest-first (started_at DESC); we keep the first
		// max_runs terminal runs and delete the remainder.
		runs, err := p.repos.Run.List(ctx, sc.ProjectID, run.RunFilter{ScheduleID: sc.ID})
		if err != nil {
			slog.Warn("retention list schedule runs failed", "project_id", sc.ProjectID, "schedule_id", sc.ID, "err", err)
			continue
		}
		kept := 0
		deleteIDs := make([]string, 0)
		for _, r := range runs {
			if r.EndedAt == nil || r.Status == run.StatusRunning || r.Status == run.StatusScheduled {
				continue
			}
			if kept < sc.MaxRuns {
				kept++
				continue
			}
			deleteIDs = append(deleteIDs, r.ID)
		}
		if len(deleteIDs) > 0 {
			if err := p.deleteRunsWithArtifacts(project.WithContext(ctx, project.Context{ID: sc.ProjectID}), deleteIDs); err != nil {
				slog.Warn("retention delete schedule runs failed", "project_id", sc.ProjectID, "schedule_id", sc.ID, "count", len(deleteIDs), "err", err)
			}
		}
	}
}

// Close stops background goroutines and closes the store.
func (p *Piper) Close() error {
	p.stopCtx() // cancel runCleanup and any pending dispatches
	p.scheduler.Stop()
	p.wg.Wait()
	return p.repos.Close()
}

// openStore creates a Repos according to the Config priority rules:
//
//	Repos (external) > DBDriver+DBDSN > DBPath (sqlite default)
func openStore(cfg Config) (*storemod.Repos, error) {
	// 1. Externally-constructed Repos — caller manages migrations and lifecycle.
	if cfg.Repos != nil {
		return cfg.Repos, nil
	}
	// 2. Explicit driver selection
	switch cfg.DBDriver {
	case "postgres", "postgresql":
		if cfg.DBDSN == "" {
			return nil, fmt.Errorf("db_dsn is required for postgres driver")
		}
		return storemod.OpenPostgres(cfg.DBDSN)
	}
	// 4. SQLite file path (default)
	dbPath := cfg.DBPath
	if dbPath == "" {
		dbPath = filepath.Join(cfg.OutputDir, "piper.db")
	}
	return storemod.Open(dbPath)
}

// BuiltinVars holds system-injected variables propagated to every pipeline step.
// Exported here so external callers do not need to import internal/proto.
type BuiltinVars = proto.BuiltinVars

// RunOptions holds optional parameters for local pipeline execution.
type RunOptions struct {
	ProjectID string
	Vars      BuiltinVars    // system-injected builtin variables (e.g. ScheduledAt)
	Params    map[string]any // run-level params; override step-level YAML params at runtime
}

// RunFile runs a pipeline YAML file through the full dispatch stack
// (queue → gRPC → embedded worker → executor), matching the production code path.
func (p *Piper) RunFile(ctx context.Context, path string) (*pipeline.RunResult, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return p.Run(ctx, data)
}

// Run runs a pipeline YAML through the full dispatch stack
// (queue → gRPC → embedded worker → executor), matching the production code path.
func (p *Piper) Run(ctx context.Context, yamlBytes []byte) (*pipeline.RunResult, error) {
	return p.runWithEmbeddedWorker(ctx, yamlBytes, RunOptions{})
}

// RunPipeline runs a parsed Pipeline through the full dispatch stack.
func (p *Piper) RunPipeline(ctx context.Context, pl *pipeline.Pipeline) (*pipeline.RunResult, error) {
	return p.RunPipelineOpts(ctx, pl, RunOptions{})
}

// RunPipelineOpts runs a parsed Pipeline with options through the full dispatch stack.
func (p *Piper) RunPipelineOpts(ctx context.Context, pl *pipeline.Pipeline, opts RunOptions) (*pipeline.RunResult, error) {
	data, err := pipeline.Marshal(pl)
	if err != nil {
		return nil, err
	}
	return p.runWithEmbeddedWorker(ctx, data, opts)
}

// runWithEmbeddedWorker runs a pipeline through the full production stack:
// queue → gRPC → embedded worker → executor.
// This ensures p.Run() validates the same code path as a deployed worker.
func (p *Piper) runWithEmbeddedWorker(ctx context.Context, yamlBytes []byte, opts RunOptions) (*pipeline.RunResult, error) {
	httpPort, err := randomFreePort()
	if err != nil {
		return nil, fmt.Errorf("run: allocate HTTP port: %w", err)
	}
	httpAddr := fmt.Sprintf("127.0.0.1:%d", httpPort)
	masterURL := "http://" + httpAddr

	events, unsub := p.events.Subscribe()
	defer unsub()

	runCtx, runCancel := context.WithCancel(ctx)
	defer runCancel()

	go func() { _ = p.Serve(runCtx, ServeOption{Addr: httpAddr}) }()

	if err := waitForHTTPReady(ctx, masterURL+"/health", 10*time.Second); err != nil {
		return nil, fmt.Errorf("run: server not ready: %w", err)
	}

	outputDir := p.cfg.OutputDir
	if outputDir == "" {
		outputDir = "./piper-outputs"
	}
	concurrency := 4

	metaDir, err := os.MkdirTemp("", "piper-run-meta-*")
	if err != nil {
		return nil, fmt.Errorf("run: create meta dir: %w", err)
	}
	defer func() { _ = os.RemoveAll(metaDir) }()

	w, err := worker.New(worker.Config{
		Agent: worker.AgentConfig{
			MasterURL:   masterURL,
			WorkerToken: p.cfg.Server.WorkerToken,
			ID:          worker.NewID("run"),
			Concurrency: concurrency,
		},
		Store: worker.StoreConfig{
			OutputDir:        outputDir,
			LocalStoreAccess: true,
		},
		Baremetal: worker.BaremetalConfig{
			MetaDir: metaDir,
		},
	})
	if err != nil {
		return nil, fmt.Errorf("run: embedded worker: %w", err)
	}
	go func() { _ = w.Run(runCtx) }()

	if err := waitForPipelineWorker(ctx, masterURL, 10*time.Second); err != nil {
		return nil, fmt.Errorf("run: worker did not register: %w", err)
	}

	projectID := opts.ProjectID
	if projectID == "" {
		projectID = "default"
	}
	if existing, err := p.repos.Project.Get(ctx, projectID); err != nil {
		return nil, fmt.Errorf("run: get project: %w", err)
	} else if existing == nil {
		if err := p.repos.Project.Create(ctx, &project.Project{ID: projectID, Name: projectID}); err != nil {
			return nil, fmt.Errorf("run: create project: %w", err)
		}
	}
	projectCtx := project.WithContext(ctx, project.Context{ID: projectID})
	runID, err := p.startRunFromAPI(projectCtx, string(yamlBytes), opts.Params, opts.Vars, "")
	if err != nil {
		return nil, err
	}

	_, err = waitForRunCompleted(ctx, events, runID)
	if err != nil {
		return nil, fmt.Errorf("run: wait for completion: %w", err)
	}

	return p.buildRunResult(ctx, projectID, runID)
}

// randomFreePort asks the OS for an available TCP port.
func randomFreePort() (int, error) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	port := l.Addr().(*net.TCPAddr).Port
	_ = l.Close()
	return port, nil
}

// waitForHTTPReady polls url until it returns 2xx or timeout.
func waitForHTTPReady(ctx context.Context, url string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			return err
		}
		resp, err := http.DefaultClient.Do(req)
		if err == nil {
			_ = resp.Body.Close()
			if resp.StatusCode < 300 {
				return nil
			}
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(50 * time.Millisecond):
		}
	}
	return fmt.Errorf("not ready within %s", timeout)
}

// waitForPipelineWorker polls GET /api/workers until a pipeline worker appears.
func waitForPipelineWorker(ctx context.Context, masterURL string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, masterURL+"/api/workers", nil)
		if err != nil {
			return err
		}
		resp, err := http.DefaultClient.Do(req)
		if err == nil {
			var agents []struct {
				Capabilities []string `json:"capabilities"`
			}
			if json.NewDecoder(resp.Body).Decode(&agents) == nil {
				for _, a := range agents {
					for _, capa := range a.Capabilities {
						if capa == "pipeline" {
							_ = resp.Body.Close()
							return nil
						}
					}
				}
			}
			_ = resp.Body.Close()
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
	}
	return fmt.Errorf("no pipeline agent registered within %s", timeout)
}

// waitForRunCompleted waits for run.completed event with the given run ID and returns its status.
func waitForRunCompleted(ctx context.Context, events <-chan event.Event, runID string) (string, error) {
	for {
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case e, ok := <-events:
			if !ok {
				return "", fmt.Errorf("event channel closed")
			}
			if e.Type != "run.completed" {
				continue
			}
			if id, _ := e.Fields["run_id"].(string); id != runID {
				continue
			}
			status, _ := e.Fields["status"].(string)
			return status, nil
		}
	}
}

// buildRunResult reads the run and step records from the DB and constructs a RunResult.
func (p *Piper) buildRunResult(ctx context.Context, projectID, runID string) (*pipeline.RunResult, error) {
	r, err := p.repos.Run.Get(ctx, projectID, runID)
	if err != nil {
		return nil, fmt.Errorf("get run: %w", err)
	}
	steps, err := p.repos.Step.List(ctx, projectID, runID)
	if err != nil {
		return nil, fmt.Errorf("list steps: %w", err)
	}

	result := &pipeline.RunResult{
		PipelineName: r.PipelineName,
		StartedAt:    r.StartedAt,
	}
	if r.EndedAt != nil {
		result.EndedAt = *r.EndedAt
	}
	result.Steps = make(map[string]*pipeline.StepResult, len(steps))
	for _, s := range steps {
		sr := &pipeline.StepResult{
			StepName: s.StepName,
			Status:   pipeline.StepStatus(s.Status),
			Attempts: s.Attempts,
			ErrMsg:   s.Error,
		}
		if s.StartedAt != nil {
			sr.StartedAt = *s.StartedAt
		}
		if s.EndedAt != nil {
			sr.EndedAt = *s.EndedAt
		}
		result.Steps[s.StepName] = sr
	}
	return result, nil
}

// StartRunOptions holds parameters for enqueuing a new distributed run.
type StartRunOptions struct {
	ProjectID  string
	ScheduleID string
	Experiment string
	Params     map[string]any
	Vars       BuiltinVars
	YAML       string // raw YAML, persisted to DB
}

// startRun is the single entry point for enqueuing a pipeline run.
// Both the HTTP API and the scheduler go through here.
// It creates the DB record, initialises step rows, enqueues the DAG, and fires OnRunStart.
func (p *Piper) startRun(ctx context.Context, pl *pipeline.Pipeline, dag *pipeline.DAG, opts StartRunOptions) (string, error) {
	runID := genRunID()
	outputDir := filepath.Join(p.cfg.OutputDir, runID)
	now := time.Now().UTC()
	if opts.Vars.RunStartedAt == nil {
		opts.Vars.RunStartedAt = &now
	}

	r := &run.Run{
		ID:           runID,
		ProjectID:    opts.ProjectID,
		ScheduleID:   opts.ScheduleID,
		Experiment:   opts.Experiment,
		PipelineName: pl.Metadata.Name,
		Status:       run.StatusRunning,
		StartedAt:    now,
		ScheduledAt:  opts.Vars.ScheduledAt,
		PipelineYAML: opts.YAML,
		ParamsJSON:   encodeParams(opts.Params),
	}
	if err := p.repos.Run.Create(ctx, r); err != nil {
		return "", fmt.Errorf("create run: %w", err)
	}

	for _, s := range pl.Spec.Steps {
		if err := p.repos.Step.Upsert(ctx, &run.Step{
			ProjectID: opts.ProjectID,
			RunID:     runID,
			StepName:  s.Name,
			Status:    "pending",
		}); err != nil {
			slog.Warn("init step failed", "run_id", runID, "step", s.Name, "err", err)
		}
	}

	envByStep, err := p.resolvePipelineSecretEnv(ctx, opts.ProjectID, pl)
	if err != nil {
		now := time.Now().UTC()
		_ = p.repos.Run.UpdateStatus(ctx, opts.ProjectID, runID, run.StatusFailed, &now)
		return "", err
	}

	p.queue.AddWithEnv(ctx, opts.ProjectID, pl, dag, runID, ".", outputDir, opts.Vars, opts.Params, envByStep)
	slog.Info("event", "type", "run.started", "run_id", runID, "pipeline", pl.Metadata.Name)

	if p.cfg.Hooks.OnRunStart != nil {
		go p.cfg.Hooks.OnRunStart(ctx, runID, pl)
	}

	return runID, nil
}

// startSweep submits multiple runs from one YAML with different params.
// On partial failure it cancels already-submitted runs (best-effort).
func (p *Piper) startSweep(ctx context.Context, projectID string, req run.SweepRequest) (run.SweepResponse, error) {
	pl, err := pipeline.Parse([]byte(req.YAML))
	if err != nil {
		return run.SweepResponse{}, fmt.Errorf("parse pipeline: %w", err)
	}
	dag, err := pipeline.BuildDAG(pl)
	if err != nil {
		return run.SweepResponse{}, fmt.Errorf("build dag: %w", err)
	}

	runIDs := make([]string, 0, len(req.Runs))
	for i, trial := range req.Runs {
		runID, err := p.startRun(ctx, pl, dag, StartRunOptions{
			ProjectID:  projectID,
			Experiment: req.Experiment,
			Params:     trial.Params,
			YAML:       req.YAML,
		})
		if err != nil {
			now := time.Now().UTC()
			for _, id := range runIDs {
				_ = p.repos.Run.UpdateStatus(ctx, projectID, id, run.StatusCanceled, &now)
			}
			return run.SweepResponse{}, fmt.Errorf("trial %d: %w", i, err)
		}
		runIDs = append(runIDs, runID)
	}
	return run.SweepResponse{Experiment: req.Experiment, RunIDs: runIDs}, nil
}

// Parse parses YAML only (for validation without execution)
func (p *Piper) Parse(yamlBytes []byte) (*pipeline.Pipeline, error) {
	return pipeline.Parse(yamlBytes)
}

// ParseFile parses a file only
func (p *Piper) ParseFile(path string) (*pipeline.Pipeline, error) {
	return pipeline.ParseFile(path)
}

func (p *Piper) sourceConfig() srcfetch.Config {
	return srcfetch.Config{
		GitUser:    p.cfg.Git.User,
		GitToken:   p.cfg.Git.Token,
		StorageURL: p.storageURL,
	}
}

// resolveGitEnv resolves git credentials for a step using priority:
// connectionRef (explicit) > credentialRef (legacy secret) > endpoint auto-match (lowest).
// Returns nil env (no error) when no credential is configured.
func (p *Piper) resolveGitEnv(ctx context.Context, projectID, connectionRef string, credentialRef *pipeline.SecretRef, repoURL string) ([]string, error) {
	if p.connections != nil && strings.TrimSpace(connectionRef) != "" {
		return p.connections.GitEnv(ctx, projectID, connectionRef, repoURL)
	}
	if credentialRef != nil && strings.TrimSpace(credentialRef.Name) != "" {
		return p.secrets.GitEnv(ctx, projectID, credentialRef.Name)
	}
	// Auto-match: find connection whose endpoint covers repoURL
	if p.connections != nil {
		best, err := p.connections.FindByRepo(ctx, projectID, repoURL)
		if err != nil {
			return nil, err
		}
		if best != nil {
			return p.connections.GitEnv(ctx, projectID, best.Name, repoURL)
		}
	}
	return nil, nil
}

func (p *Piper) resolvePipelineSecretEnv(ctx context.Context, projectID string, pl *pipeline.Pipeline) (map[string][]string, error) {
	envByStep := map[string][]string{}
	for _, step := range pl.Spec.Steps {
		var env []string

		// Git credential resolution: connectionRef > credentialRef > auto-match by endpoint
		if strings.TrimSpace(step.Run.Source) == "git" && strings.TrimSpace(step.Run.Repo) != "" {
			gitEnv, err := p.resolveGitEnv(ctx, projectID, step.Run.ConnectionRef, step.Run.CredentialRef, step.Run.Repo)
			if err != nil {
				return nil, fmt.Errorf("step %q git credential: %w", step.Name, err)
			}
			env = append(env, gitEnv...)
		}

		// options.env: plain values + secretKeyRef resolution
		if len(step.Options.Env) > 0 {
			optEnv, err := p.secrets.ResolveEnv(ctx, projectID, step.Options.Env)
			if err != nil {
				return nil, fmt.Errorf("step %q env: %w", step.Name, err)
			}
			env = append(env, optEnv...)
		}

		if len(env) > 0 {
			envByStep[step.Name] = env
		}
	}
	return envByStep, nil
}

// SetBackend registers an external execution environment such as a K8s Job launcher.
// When set, Dispatch is called immediately whenever a task becomes ready.
// Setting nil disables task dispatch until another backend is configured.
func (p *Piper) SetBackend(b pipelinedispatch.ExecutionBackend) {
	p.backend = b
	p.queue.SetBackend(b)
}

func (p *Piper) Config() Config {
	return p.cfg
}

// Repos returns the underlying store.Repos, useful for admin CLI commands.
func (p *Piper) Repos() *storemod.Repos { return p.repos }

// piperArtifactResolver implements artifact.Resolver for the Piper instance.
type piperArtifactResolver struct {
	runRepo    run.Repository
	outputDir  string
	storageURL string // resolved storage URL; empty means local-only
}

func (r *piperArtifactResolver) Resolve(ctx context.Context, pipeline, step, artName, runRef string, target artifact.Target) (artifact.Resolved, error) {
	runID := runRef
	if runID == "latest" || runID == "" {
		projectContext, _ := project.FromContext(ctx)
		latest, err := r.runRepo.GetLatestSuccessful(ctx, projectContext.ID, pipeline)
		if err != nil {
			return artifact.Resolved{}, fmt.Errorf("lookup latest run for pipeline %q: %w", pipeline, err)
		}
		if latest == nil {
			return artifact.Resolved{}, fmt.Errorf("no successful run found for pipeline %q", pipeline)
		}
		runID = latest.ID
	}

	artKey := fmt.Sprintf("%s/%s/%s", runID, step, artName)

	switch target {
	case artifact.TargetS3:
		uri, err := r.artifactURI(artKey)
		if err != nil {
			return artifact.Resolved{}, err
		}
		return artifact.Resolved{RunID: runID, S3URI: uri}, nil
	default:
		// LocalPath points to the step output directory.
		return artifact.Resolved{
			RunID:     runID,
			LocalPath: filepath.Join(r.outputDir, runID, step),
		}, nil
	}
}

// artifactURI constructs a URI for the artifact key based on the configured storage.
func (r *piperArtifactResolver) artifactURI(artKey string) (string, error) {
	if r.storageURL == "" {
		return "", fmt.Errorf("artifact URI requires a storage backend (configure storage.url or s3)")
	}
	u, err := url.Parse(r.storageURL)
	if err != nil {
		return "", err
	}
	switch u.Scheme {
	case "s3":
		return "s3://" + u.Host + "/" + artKey, nil
	case "http", "https":
		base := strings.TrimRight(r.storageURL, "/")
		return base + "/" + artKey, nil
	default:
		return "", fmt.Errorf("storage backend %q cannot provide artifact URIs for remote serving", u.Scheme)
	}
}

func (p *Piper) SourceConfig() srcfetch.Config {
	return p.sourceConfig()
}

func encodeParams(params map[string]any) string {
	if params == nil {
		return "{}"
	}
	b, err := json.Marshal(params)
	if err != nil {
		return "{}"
	}
	return string(b)
}

// modelDir returns the local directory for a serving model.
func (p *Piper) modelDir(serviceName string) string {
	if p.cfg.Serving.ModelDir != "" {
		return filepath.Join(p.cfg.Serving.ModelDir, serviceName)
	}
	return filepath.Join(p.cfg.OutputDir, "models", serviceName)
}

// ResolveStorageURL derives the effective storage URL from the config.
// Priority: Storage.Disabled -> empty; Storage.URL > S3Config (backward compat) > file://{output_dir}/store.
func (cfg Config) ResolveStorageURL() string { return resolveStorageURL(cfg) }

// resolveStorageURL is the internal implementation.
// Priority: Storage.Disabled -> empty; Storage.URL > S3Config (backward compat) > file://{output_dir}/store (built-in).
func resolveStorageURL(cfg Config) string {
	if cfg.Storage.Disabled {
		return ""
	}
	if cfg.Storage.URL != "" {
		return cfg.Storage.URL
	}
	if cfg.S3.Bucket != "" {
		scheme := "http"
		if cfg.S3.UseSSL {
			scheme = "https"
		}
		endpoint := cfg.S3.Endpoint
		q := "s3ForcePathStyle=true"
		if cfg.S3.AccessKey != "" {
			q += "&accessKey=" + cfg.S3.AccessKey
		}
		if cfg.S3.SecretKey != "" {
			q += "&secretKey=" + cfg.S3.SecretKey
		}
		if endpoint != "" {
			q += "&endpoint=" + scheme + "://" + endpoint
		}
		return "s3://" + cfg.S3.Bucket + "?" + q
	}
	// Default: built-in file server under output directory.
	outputDir := cfg.OutputDir
	if outputDir == "" {
		outputDir = "./piper-outputs"
	}
	return "file://" + filepath.Join(outputDir, "store")
}
