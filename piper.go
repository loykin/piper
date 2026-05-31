package piper

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"

	s3sdk "github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/google/uuid"

	"github.com/piper/piper/internal/queue"
	"github.com/piper/piper/pkg/artifact"
	"github.com/piper/piper/pkg/backend"
	"github.com/piper/piper/pkg/event"
	"github.com/piper/piper/pkg/executor"
	"github.com/piper/piper/pkg/logstore"
	"github.com/piper/piper/pkg/notebook"
	"github.com/piper/piper/pkg/pipeline"
	"github.com/piper/piper/pkg/proto"
	"github.com/piper/piper/pkg/run"
	"github.com/piper/piper/pkg/s3client"
	"github.com/piper/piper/pkg/serving"
	"github.com/piper/piper/pkg/source"

	storemod "github.com/piper/piper/internal/store"
	iworker "github.com/piper/piper/internal/worker"
)

// servingBundle groups the serving manager, proxy, and worker registry together.
type servingBundle struct {
	manager        *serving.Manager
	proxy          *serving.Proxy
	workerRegistry *serving.ServingWorkerRegistry
}

// Piper is the library entry point.
// Embed it in projects such as data-voyager.
//
//	p := piper.New(piper.DefaultConfig())
//	result, err := p.RunFile(ctx, "train.yaml")
type Piper struct {
	cfg                    Config
	ctx                    context.Context // cancelled on Close; passed to background goroutines and hooks
	repos                  *storemod.Repos
	logs                   logstore.LogStore
	metrics                logstore.MetricStore
	queue                  *queue.Queue
	registry               *iworker.Registry
	serving                servingBundle
	notebookManager        *notebook.Manager
	notebookWorkerRegistry *notebook.NotebookWorkerRegistry
	notebookDriverMode     string            // "k8s" or "worker"
	s3Cli                  *s3sdk.Client     // nil when S3 not configured
	resolver               artifact.Resolver // central artifact resolver
	backend                backend.ExecutionBackend
	events                 *event.Hub

	stopCtx context.CancelFunc // cancels ctx on Close
}

func New(cfg Config) (*Piper, error) {
	def := DefaultConfig()
	if cfg.OutputDir == "" {
		cfg.OutputDir = def.OutputDir
	}
	if cfg.MaxRetries < 0 {
		cfg.MaxRetries = def.MaxRetries
	}
	if cfg.RetryDelay == 0 {
		cfg.RetryDelay = def.RetryDelay
	}
	if cfg.Concurrency == 0 {
		cfg.Concurrency = def.Concurrency
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
	modelDir := cfg.Serving.ModelDir
	if modelDir == "" {
		modelDir = filepath.Join(cfg.OutputDir, "models")
	}
	if err := os.MkdirAll(modelDir, 0755); err != nil {
		return nil, fmt.Errorf("create model dir: %w", err)
	}

	var k8sClientset *kubernetes.Clientset
	if cfg.K8s.Kubeconfig != "" || cfg.K8s.InCluster {
		if cs, err := buildK8sClientset(cfg.K8s); err != nil {
			slog.Warn("k8s clientset unavailable for serving", "err", err)
		} else {
			k8sClientset = cs
		}
	}

	// Build serving driver: K8s if clientset available, else WorkerDriver.
	servingWorkerReg := serving.NewServingWorkerRegistry()
	var servingDriver serving.Driver
	if k8sClientset != nil {
		servingDriver = serving.NewK8sDriver(k8sClientset, repos.Serving)
	} else {
		servingDriver = serving.NewWorkerDriver(servingWorkerReg, repos.Serving, listenAddrToURL(cfg.Server.Addr))
	}
	servingMgr := serving.New(repos.Serving, servingDriver)

	// Build notebook driver: K8sDriver if k8s clientset is available and WorkerImage is set;
	// otherwise fall back to WorkerDriver.
	notebookWorkerReg := notebook.NewNotebookWorkerRegistry()
	var nbDriver notebook.Driver
	nbK8s := cfg.NotebookK8s
	notebookDriverMode := "worker"
	if k8sClientset != nil && nbK8s.WorkerImage != "" {
		nbNs := nbK8s.Namespace
		if nbNs == "" {
			nbNs = cfg.K8s.Namespace
		}
		if nbNs == "" {
			nbNs = "default"
		}
		nbDriver = notebook.NewK8sDriver(k8sClientset, notebook.K8sDriverConfig{
			Namespace:    nbNs,
			WorkerImage:  nbK8s.WorkerImage,
			StorageClass: nbK8s.StorageClass,
			StorageSize:  nbK8s.StorageSize,
			PodDefaults: notebook.K8sPodDefaults{
				Resources:    nbK8s.PodDefaults.Resources,
				NodeSelector: nbK8s.PodDefaults.NodeSelector,
				Tolerations:  nbK8s.PodDefaults.Tolerations,
				Annotations:  nbK8s.PodDefaults.Annotations,
			},
		}, repos.Notebook)
		notebookDriverMode = "k8s"
		slog.Info("k8s notebook driver enabled", "namespace", nbNs, "image", nbK8s.WorkerImage)
	} else {
		nbDriver = notebook.NewWorkerDriver(notebookWorkerReg, listenAddrToURL(cfg.Server.Addr))
	}
	nbMgr := notebook.New(repos.Notebook, repos.NotebookVolume, nbDriver)

	bgCtx, stopFn := context.WithCancel(context.Background())
	q := queue.NewQueue(bgCtx, repos.Run, repos.Step)
	q.SetRetryPolicy(cfg.MaxRetries+1, cfg.RetryDelay)
	p := &Piper{
		cfg:     cfg,
		ctx:     bgCtx,
		repos:   repos,
		logs:    repos.Log,
		metrics: repos.Metric,
		queue:   q,
		serving: servingBundle{
			manager:        servingMgr,
			proxy:          serving.NewProxy(repos.Serving),
			workerRegistry: servingWorkerReg,
		},
		notebookManager:        nbMgr,
		notebookWorkerRegistry: notebookWorkerReg,
		notebookDriverMode:     notebookDriverMode,
		stopCtx:                stopFn,
		events:                 event.NewHub(),
	}
	if cfg.S3.Bucket != "" && cfg.S3.AccessKey != "" {
		if s3c, err := newPiperS3Client(cfg.S3); err != nil {
			slog.Warn("artifact S3 client unavailable", "err", err)
		} else {
			p.s3Cli = s3c
		}
	}
	p.resolver = &piperArtifactResolver{
		runRepo:   repos.Run,
		outputDir: cfg.OutputDir,
		s3Bucket:  cfg.S3.Bucket,
	}
	q.OnRunSuccess = p.handleRunSuccess
	q.SetEventPublisher(p.events)
	p.serving.manager.SetEventPublisher(p.events)
	p.notebookManager.SetEventPublisher(p.events)
	p.recoverInterruptedRuns(context.Background())
	go p.runCleanup(p.ctx)
	go p.runScheduler(p.ctx)
	return p, nil
}

// handleRunSuccess is called (in a goroutine) when a queued run completes successfully.
// It triggers on_success.deploy if configured in the pipeline spec.
func (p *Piper) handleRunSuccess(ctx context.Context, runID string, pl *pipeline.Pipeline) {
	if pl.Spec.OnSuccess == nil || pl.Spec.OnSuccess.Deploy == nil {
		return
	}
	trigger := pl.Spec.OnSuccess.Deploy
	svc, err := p.repos.Serving.Get(ctx, trigger.Service)
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
	if _, err := p.DeployService(ctx, updatedYAML); err != nil {
		slog.Warn("auto-deploy on run success failed", "run_id", runID, "service", trigger.Service, "err", err)
	}
}

// runCleanup periodically removes expired workers and stuck queue entries.
func (p *Piper) runCleanup(ctx context.Context) {
	ticker := time.NewTicker(iworker.WorkerTTL / 2)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if p.registry != nil {
				p.registry.Cleanup()
			}
			p.reconcileBackend(ctx)
			p.serving.manager.CheckHealth(ctx)
			p.notebookManager.CheckHealth(ctx)
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

func (p *Piper) recoverInterruptedRuns(ctx context.Context) {
	runs, err := p.repos.Run.List(ctx, run.RunFilter{Status: run.StatusRunning})
	if err != nil {
		slog.Warn("recover running runs failed", "err", err)
		return
	}
	now := time.Now().UTC()
	for _, r := range runs {
		if r.PipelineYAML == "" {
			// No YAML — can't reconstruct DAG, mark failed.
			if err := p.repos.Run.UpdateStatus(ctx, r.ID, run.StatusFailed, &now); err != nil {
				slog.Warn("recover run failed", "run_id", r.ID, "err", err)
			}
			continue
		}
		pl, err := p.Parse([]byte(r.PipelineYAML))
		if err != nil {
			slog.Warn("recover: parse pipeline failed", "run_id", r.ID, "err", err)
			_ = p.repos.Run.UpdateStatus(ctx, r.ID, run.StatusFailed, &now)
			continue
		}
		dag, err := pipeline.BuildDAG(pl)
		if err != nil {
			slog.Warn("recover: build dag failed", "run_id", r.ID, "err", err)
			_ = p.repos.Run.UpdateStatus(ctx, r.ID, run.StatusFailed, &now)
			continue
		}
		steps, _ := p.repos.Step.List(ctx, r.ID)
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
		p.queue.Recover(ctx, pl, dag, r.ID, ".", outputDir, proto.BuiltinVars{ScheduledAt: r.ScheduledAt}, params, recovered)
	}
}

func (p *Piper) cleanupRetention(ctx context.Context) {
	runTTL := p.cfg.Retention.RunTTL
	artifactTTL := p.cfg.Retention.ArtifactTTL
	if runTTL <= 0 && artifactTTL <= 0 {
		return
	}
	runs, err := p.repos.Run.List(ctx, run.RunFilter{})
	if err != nil {
		slog.Warn("retention list runs failed", "err", err)
		return
	}
	now := time.Now().UTC()
	for _, r := range runs {
		if r.EndedAt == nil || r.Status == run.StatusRunning || r.Status == run.StatusScheduled {
			continue
		}
		if runTTL > 0 && r.EndedAt.Before(now.Add(-runTTL)) {
			if err := p.deleteRunWithArtifacts(ctx, r.ID); err != nil {
				slog.Warn("retention delete run failed", "run_id", r.ID, "err", err)
			}
			continue
		}
		if artifactTTL > 0 && r.EndedAt.Before(now.Add(-artifactTTL)) {
			if err := deleteArtifacts(ctx, p.s3Cli, p.cfg.S3.Bucket, p.cfg.OutputDir, r.ID); err != nil {
				slog.Warn("retention delete artifacts failed", "run_id", r.ID, "err", err)
			}
		}
	}
}

// Close stops background goroutines and closes the store.
func (p *Piper) Close() error {
	p.stopCtx() // cancel runCleanup, runScheduler, and any pending dispatches
	return p.repos.Close()
}

// openStore creates a Repos according to the Config priority rules:
//
//	Repos (external) > DB (injected sqlite) > DBDriver+DBDSN > DBPath (sqlite default)
func openStore(cfg Config) (*storemod.Repos, error) {
	// 1. Externally-constructed Repos — caller manages migrations and lifecycle.
	if cfg.Repos != nil {
		return cfg.Repos, nil
	}
	// 2. Directly injected *sql.DB (SQLite assumed)
	if cfg.DB != nil {
		return storemod.New(cfg.DB)
	}
	// 3. Explicit driver selection
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

// RunOptions holds optional parameters for local pipeline execution.
type RunOptions struct {
	Vars   proto.BuiltinVars // system-injected builtin variables (e.g. ScheduledAt)
	Params map[string]any    // run-level params; override step-level YAML params at runtime
}

// RunFile takes a YAML file path and runs the pipeline locally
func (p *Piper) RunFile(ctx context.Context, path string) (*pipeline.RunResult, error) {
	pl, err := pipeline.ParseFile(path)
	if err != nil {
		return nil, err
	}
	return p.RunPipeline(ctx, pl)
}

// Run takes YAML bytes and runs the pipeline locally
func (p *Piper) Run(ctx context.Context, yamlBytes []byte) (*pipeline.RunResult, error) {
	pl, err := pipeline.Parse(yamlBytes)
	if err != nil {
		return nil, err
	}
	return p.RunPipeline(ctx, pl)
}

// RunPipeline directly executes a parsed Pipeline struct
func (p *Piper) RunPipeline(ctx context.Context, pl *pipeline.Pipeline) (*pipeline.RunResult, error) {
	return p.runPipelineWithRunID(ctx, pl, "", RunOptions{})
}

// RunPipelineOpts executes a parsed Pipeline with optional run options (e.g. Vars.ScheduledAt, Params).
func (p *Piper) RunPipelineOpts(ctx context.Context, pl *pipeline.Pipeline, opts RunOptions) (*pipeline.RunResult, error) {
	return p.runPipelineWithRunID(ctx, pl, "", opts)
}

func (p *Piper) runPipelineWithRunID(ctx context.Context, pl *pipeline.Pipeline, runID string, opts RunOptions) (*pipeline.RunResult, error) {
	dag, err := pipeline.BuildDAG(pl)
	if err != nil {
		return nil, err
	}

	captureLogs := runID != ""
	if runID == "" {
		runID = uuid.NewString()
	}

	srcCfg := p.sourceConfig()
	outputDir := filepath.Join(p.cfg.OutputDir, runID)

	execFn := func(ctx context.Context, step *pipeline.Step) error {
		stepOutputDir := filepath.Join(outputDir, step.Name)
		if err := os.MkdirAll(stepOutputDir, 0755); err != nil {
			return err
		}

		// Capture logs: persist to store
		var stdoutW, stderrW io.Writer = os.Stdout, os.Stderr
		if captureLogs {
			stdoutW = &storeLogWriter{logs: p.logs, runID: runID, stepName: step.Name, stream: "stdout", tee: os.Stdout}
			stderrW = &storeLogWriter{logs: p.logs, runID: runID, stepName: step.Name, stream: "stderr", tee: os.Stderr}
		}

		exec := executor.New(step)
		return exec.Execute(ctx, step, executor.ExecConfig{
			WorkDir:   ".",
			InputDir:  outputDir,
			OutputDir: stepOutputDir,
			RunID:     runID,
			StepName:  step.Name,
			Params:    proto.MergeParams(step.Params, opts.Params),
			SourceCfg: srcCfg,
			Stdout:    stdoutW,
			Stderr:    stderrW,
			Vars:      opts.Vars,
		})
	}

	runnerCfg := pipeline.RunnerConfig{
		MaxRetries:  p.cfg.MaxRetries,
		RetryDelay:  p.cfg.RetryDelay,
		Concurrency: p.cfg.Concurrency,
	}

	runner := pipeline.NewRunner(pl, dag, runnerCfg, execFn)
	return runner.Run(ctx), nil
}

// StartRunOptions holds parameters for enqueuing a new distributed run.
type StartRunOptions struct {
	OwnerID    string
	ScheduleID string
	Experiment string
	Params     map[string]any
	Vars       proto.BuiltinVars
	YAML       string // raw YAML, persisted to DB
}

// startRun is the single entry point for enqueuing a pipeline run.
// Both the HTTP API and the scheduler go through here.
// It creates the DB record, initialises step rows, enqueues the DAG, and fires OnRunStart.
func (p *Piper) startRun(ctx context.Context, pl *pipeline.Pipeline, dag *pipeline.DAG, opts StartRunOptions) (string, error) {
	runID := genRunID()
	outputDir := filepath.Join(p.cfg.OutputDir, runID)
	now := time.Now().UTC()

	r := &run.Run{
		ID:           runID,
		ScheduleID:   opts.ScheduleID,
		OwnerID:      opts.OwnerID,
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
			RunID:    runID,
			StepName: s.Name,
			Status:   "pending",
		}); err != nil {
			slog.Warn("init step failed", "run_id", runID, "step", s.Name, "err", err)
		}
	}

	p.queue.Add(ctx, pl, dag, runID, ".", outputDir, opts.Vars, opts.Params)
	slog.Info("event", "type", "run.started", "run_id", runID, "pipeline", pl.Metadata.Name)

	if p.cfg.Hooks.OnRunStart != nil {
		go p.cfg.Hooks.OnRunStart(ctx, runID, pl)
	}

	return runID, nil
}

// Parse parses YAML only (for validation without execution)
func (p *Piper) Parse(yamlBytes []byte) (*pipeline.Pipeline, error) {
	return pipeline.Parse(yamlBytes)
}

// ParseFile parses a file only
func (p *Piper) ParseFile(path string) (*pipeline.Pipeline, error) {
	return pipeline.ParseFile(path)
}

func (p *Piper) sourceConfig() source.Config {
	return source.Config{
		GitUser:     p.cfg.Git.User,
		GitToken:    p.cfg.Git.Token,
		S3Endpoint:  p.cfg.S3.Endpoint,
		S3AccessKey: p.cfg.S3.AccessKey,
		S3SecretKey: p.cfg.S3.SecretKey,
		S3Bucket:    p.cfg.S3.Bucket,
		S3UseSSL:    p.cfg.S3.UseSSL,
	}
}

// SetBackend registers an external execution environment such as a K8s Job launcher.
// When set, Dispatch is called immediately whenever a task becomes ready.
// Setting nil reverts to worker polling mode.
func (p *Piper) SetBackend(b backend.ExecutionBackend) {
	p.backend = b
	p.queue.SetBackend(b)
	if b != nil {
		p.registry = nil
	}
}

func (p *Piper) workerRegistry() *iworker.Registry {
	if p.registry == nil {
		p.registry = iworker.NewRegistry(p.repos.Worker)
	}
	return p.registry
}

func (p *Piper) Config() Config {
	return p.cfg
}

// DeployService parses a ModelService YAML and deploys it.
// It resolves the artifact via the central resolver and starts the runtime process or K8s Deployment.
func (p *Piper) DeployService(ctx context.Context, yamlBytes []byte) (*serving.Service, error) {
	return p.deployService(ctx, yamlBytes, "")
}

func (p *Piper) deployService(ctx context.Context, yamlBytes []byte, ownerID string) (*serving.Service, error) {
	var svc serving.ModelService
	if err := yaml.Unmarshal(yamlBytes, &svc); err != nil {
		return nil, fmt.Errorf("parse ModelService YAML: %w", err)
	}
	if svc.Metadata.Name == "" {
		return nil, fmt.Errorf("ModelService metadata.name is required")
	}

	target := artifact.TargetLocal
	if svc.Spec.Runtime.Mode == "k8s" {
		target = artifact.TargetS3
	}

	resolved, artifactLabel, err := p.resolveServiceModel(ctx, svc, target)
	if err != nil {
		return nil, err
	}

	// Stop existing instance so the port is free. If Deploy fails we mark the
	// service "failed" so operators can see it is down rather than showing a
	// stale "stopped" status.
	_ = p.serving.manager.Stop(ctx, svc.Metadata.Name)
	if err := p.serving.manager.Deploy(ctx, svc, resolved, string(yamlBytes)); err != nil {
		_ = p.repos.Serving.SetStatus(ctx, svc.Metadata.Name, serving.StatusFailed)
		return nil, fmt.Errorf("deploy service: %w", err)
	}

	// Persist YAML and run_id
	rec, err := p.repos.Serving.Get(ctx, svc.Metadata.Name)
	if err != nil || rec == nil {
		return nil, fmt.Errorf("get service after deploy: %w", err)
	}
	rec.YAML = string(yamlBytes)
	rec.RunID = resolved.RunID
	rec.OwnerID = ownerID
	if artifactLabel != "" {
		rec.Artifact = artifactLabel
	}
	if err := p.repos.Serving.Update(ctx, rec); err != nil {
		return nil, fmt.Errorf("update service record: %w", err)
	}
	return rec, nil
}

func (p *Piper) resolveServiceModel(ctx context.Context, svc serving.ModelService, target artifact.Target) (artifact.Resolved, string, error) {
	ref := svc.Spec.Model.FromArtifact
	if ref != nil {
		resolved, err := p.resolver.Resolve(ctx, ref.Pipeline, ref.Step, ref.Artifact, ref.Run, target)
		if err != nil {
			return artifact.Resolved{}, "", fmt.Errorf("resolve artifact: %w", err)
		}
		return resolved, ref.Step + "/" + ref.Artifact, nil
	}
	uri := strings.TrimSpace(svc.Spec.Model.FromURI)
	if uri == "" {
		return artifact.Resolved{}, "", fmt.Errorf("spec.model.from_artifact or spec.model.from_uri is required")
	}
	resolved, err := p.resolveModelURI(ctx, svc.Metadata.Name, uri, target)
	if err != nil {
		return artifact.Resolved{}, "", err
	}
	return resolved, uri, nil
}

func (p *Piper) resolveModelURI(ctx context.Context, serviceName, uri string, target artifact.Target) (artifact.Resolved, error) {
	if strings.HasPrefix(uri, "s3://") {
		if target == artifact.TargetS3 {
			return artifact.Resolved{S3URI: uri}, nil
		}
		if p.s3Cli == nil {
			return artifact.Resolved{}, fmt.Errorf("local serving from s3:// URI requires S3 credentials (configure S3.AccessKey/SecretKey)")
		}
		dir := filepath.Join(p.cfg.Serving.ModelDir, serviceName)
		if p.cfg.Serving.ModelDir == "" {
			dir = filepath.Join(p.cfg.OutputDir, "models", serviceName)
		}
		if err := os.MkdirAll(dir, 0755); err != nil {
			return artifact.Resolved{}, err
		}
		if err := downloadS3URIToLocal(ctx, p.s3Cli, uri, dir); err != nil {
			return artifact.Resolved{}, fmt.Errorf("download s3 model: %w", err)
		}
		return artifact.Resolved{LocalPath: dir}, nil
	}
	if strings.HasPrefix(uri, "file://") {
		return artifact.Resolved{LocalPath: strings.TrimPrefix(uri, "file://")}, nil
	}
	if strings.HasPrefix(uri, "http://") || strings.HasPrefix(uri, "https://") {
		if target == artifact.TargetS3 {
			return artifact.Resolved{}, fmt.Errorf("k8s serving from http(s) URI requires an s3:// URI")
		}
		dir := filepath.Join(p.cfg.Serving.ModelDir, serviceName)
		if p.cfg.Serving.ModelDir == "" {
			dir = filepath.Join(p.cfg.OutputDir, "models", serviceName)
		}
		if err := os.MkdirAll(dir, 0755); err != nil {
			return artifact.Resolved{}, err
		}
		dest := filepath.Join(dir, filepath.Base(strings.Split(uri, "?")[0]))
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, uri, nil)
		if err != nil {
			return artifact.Resolved{}, err
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return artifact.Resolved{}, err
		}
		defer func() { _ = resp.Body.Close() }()
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			return artifact.Resolved{}, fmt.Errorf("download model URI: status %d", resp.StatusCode)
		}
		out, err := os.Create(dest)
		if err != nil {
			return artifact.Resolved{}, err
		}
		defer func() { _ = out.Close() }()
		if _, err := io.Copy(out, resp.Body); err != nil {
			return artifact.Resolved{}, err
		}
		return artifact.Resolved{LocalPath: dir}, nil
	}
	return artifact.Resolved{LocalPath: uri}, nil
}

// StopService stops the named service.
func (p *Piper) StopService(ctx context.Context, name string) error {
	return p.serving.manager.Stop(ctx, name)
}

// RestartService re-deploys the named service using its stored YAML.
func (p *Piper) RestartService(ctx context.Context, name string) error {
	rec, err := p.repos.Serving.Get(ctx, name)
	if err != nil || rec == nil {
		return fmt.Errorf("service %q not found", name)
	}
	if rec.YAML == "" {
		return fmt.Errorf("service %q has no stored YAML; cannot restart", name)
	}
	_, err = p.DeployService(ctx, []byte(rec.YAML))
	return err
}

// ListServices returns all registered services.
func (p *Piper) ListServices(ctx context.Context) ([]*serving.Service, error) {
	return p.repos.Serving.List(ctx)
}

// GetService returns a single service by name.
func (p *Piper) GetService(ctx context.Context, name string) (*serving.Service, error) {
	return p.repos.Serving.Get(ctx, name)
}

// piperArtifactResolver implements artifact.Resolver for the Piper instance.
type piperArtifactResolver struct {
	runRepo   run.Repository
	outputDir string
	s3Bucket  string
}

func (r *piperArtifactResolver) Resolve(ctx context.Context, pipeline, step, artName, runRef string, target artifact.Target) (artifact.Resolved, error) {
	runID := runRef
	if runID == "latest" || runID == "" {
		latest, err := r.runRepo.GetLatestSuccessful(ctx, pipeline)
		if err != nil {
			return artifact.Resolved{}, fmt.Errorf("lookup latest run for pipeline %q: %w", pipeline, err)
		}
		if latest == nil {
			return artifact.Resolved{}, fmt.Errorf("no successful run found for pipeline %q", pipeline)
		}
		runID = latest.ID
	}

	switch target {
	case artifact.TargetS3:
		if r.s3Bucket == "" {
			return artifact.Resolved{}, fmt.Errorf("k8s serving requires S3 artifact storage (configure S3.Bucket)")
		}
		return artifact.Resolved{
			RunID: runID,
			S3URI: fmt.Sprintf("s3://%s/%s/%s/%s", r.s3Bucket, runID, step, artName),
		}, nil
	default:
		// LocalPath points to the step output directory, not an artifact subdirectory.
		// Artifacts are written flat into the step output dir by the runner, so
		// serve.py can find model.pkl at PIPER_MODEL_DIR/model.pkl.
		return artifact.Resolved{
			RunID:     runID,
			LocalPath: filepath.Join(r.outputDir, runID, step),
		}, nil
	}
}

func (p *Piper) SourceConfig() source.Config {
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

// SetServingK8sClientset builds a k8s clientset from the given kubeconfig path
// and swaps the serving manager's driver to K8sDriver. Call this after New()
// when the kubeconfig path is only available at runtime (e.g. via CLI flag).
func (p *Piper) SetServingK8sClientset(kubeconfig string) error {
	cs, err := buildK8sClientset(K8sConfig{Kubeconfig: kubeconfig})
	if err != nil {
		return fmt.Errorf("build k8s clientset: %w", err)
	}
	driver := serving.NewK8sDriver(cs, p.repos.Serving)
	p.serving.manager = serving.New(p.repos.Serving, driver)
	p.serving.manager.SetEventPublisher(p.events)
	return nil
}

// buildK8sClientset creates a Kubernetes clientset from the K8sConfig.
func buildK8sClientset(cfg K8sConfig) (*kubernetes.Clientset, error) {
	var restCfg *rest.Config
	var err error
	if cfg.InCluster {
		restCfg, err = rest.InClusterConfig()
	} else {
		restCfg, err = clientcmd.BuildConfigFromFlags("", cfg.Kubeconfig)
	}
	if err != nil {
		return nil, fmt.Errorf("k8s rest config: %w", err)
	}
	return kubernetes.NewForConfig(restCfg)
}

// newPiperS3Client creates an S3 client from piper's S3Config.
func newPiperS3Client(cfg S3Config) (*s3sdk.Client, error) {
	return s3client.New(cfg.Endpoint, cfg.AccessKey, cfg.SecretKey, cfg.UseSSL)
}

// listenAddrToURL converts a listen address like ":8080" or "0.0.0.0:8080"
// to a full URL like "http://localhost:8080" for use as a master callback URL.
func listenAddrToURL(addr string) string {
	if strings.HasPrefix(addr, ":") {
		return "http://localhost" + addr
	}
	if idx := strings.LastIndex(addr, ":"); idx >= 0 {
		return "http://localhost" + addr[idx:]
	}
	return "http://" + addr
}
