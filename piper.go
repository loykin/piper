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

	iagent "github.com/loykin/piper/internal/agent"
	"github.com/loykin/piper/internal/artifact"
	"github.com/loykin/piper/internal/event"
	"github.com/loykin/piper/internal/grpcagent"
	"github.com/loykin/piper/internal/logstore"
	"github.com/loykin/piper/internal/pipelinedispatch"
	"github.com/loykin/piper/internal/proto"
	ischeduler "github.com/loykin/piper/internal/scheduler"
	"github.com/loykin/piper/internal/srcfetch"
	"github.com/loykin/piper/pkg/credential"
	"github.com/loykin/piper/pkg/notebook"
	notebookdispatch "github.com/loykin/piper/pkg/notebook/dispatch"
	"github.com/loykin/piper/pkg/pipeline"
	"github.com/loykin/piper/pkg/pipeline/run"
	worker "github.com/loykin/piper/pkg/pipeline/worker"
	"github.com/loykin/piper/pkg/project"
	"github.com/loykin/piper/pkg/security"
	"github.com/loykin/piper/pkg/serving"
	servingdispatch "github.com/loykin/piper/pkg/serving/dispatch"
	"github.com/loykin/piper/pkg/storage"

	storemod "github.com/loykin/piper/internal/store"
)

// servingBundle groups the serving manager and proxy together.
type servingBundle struct {
	manager *serving.Manager
	proxy   *serving.Proxy
}

// Piper is the library entry point.
// Embed it in projects such as data-voyager.
//
//	cfg := piper.DefaultConfig()
//	cfg.Server.SecretEncryptionKey = "pbkdf2:replace-with-a-strong-passphrase"
//	p, err := piper.New(cfg)
//	result, err := p.RunFile(ctx, "train.yaml")
type Piper struct {
	cfg             Config
	ctx             context.Context // cancelled on Close; passed to background goroutines and hooks
	repos           *storemod.Repos
	logs            logstore.LogStore
	metrics         logstore.MetricStore
	serving         servingBundle
	notebookManager *notebook.Manager
	agentRegistry   *iagent.Registry
	workloadRouter  *iagent.Router
	grpcAgentServer *grpcagent.Server
	store           storage.Store // nil when no artifact store configured
	credentials     *credential.Store
	storageURL      string            // resolved storage URL (for K8s launcher, artifact resolver)
	storageErr      error             // last artifact store open error, if any
	resolver        artifact.Resolver // central artifact resolver
	backend         pipelinedispatch.RunDispatchBackend
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
	if err := ensureSystemProject(context.Background(), repos.Project); err != nil {
		_ = repos.Close()
		return nil, fmt.Errorf("ensure system project: %w", err)
	}
	secretKey := cfg.Server.SecretEncryptionKey
	if secretKey == "" {
		if !cfg.Server.AllowInsecureDevKey {
			_ = repos.Close()
			return nil, fmt.Errorf("server.secret_encryption_key is required; set server.allow_insecure_dev_key=true only for local development")
		}
		secretKey = "pbkdf2:piper-dev-insecure-key-change-in-production"
		slog.Warn("server.secret_encryption_key is not set — using an insecure dev key because server.allow_insecure_dev_key=true")
	}
	credentialStore, err := credential.NewStore(repos.Credential, secretKey)
	if err != nil {
		_ = repos.Close()
		return nil, fmt.Errorf("create credential store: %w", err)
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
				Namespaces:     reg.Namespaces,
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
		WithEnvResolver(credentialStore.ResolveEnv)
	servingMgr := serving.New(repos.Serving, servingDriver)

	nbDriver := notebook.Driver(notebookdispatch.NewAgentDriver(workloadRouter, grpcSrv, repos.Notebook, repos.WorkerPodPolicy).
		WithEnvResolver(credentialStore.ResolveEnv))
	nbMgr := notebook.New(repos.Notebook, repos.NotebookVolume, nbDriver)
	bgCtx, stopFn := context.WithCancel(context.Background())
	grpcSrv.SetPushHandler(newWorkerPushHandler(nbMgr, servingMgr, repos.Run, repos.Log, repos.Metric))
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
		credentials: credentialStore,
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
	if storageURL != "" && strings.TrimSpace(cfg.Storage.CredentialRef) != "" {
		injected, err := injectStorageCredential(context.Background(), credentialStore, storageURL, cfg.Storage.CredentialRef)
		if err != nil {
			_ = repos.Close()
			return nil, fmt.Errorf("resolve storage credential %q: %w", cfg.Storage.CredentialRef, err)
		}
		storageURL = injected
	}
	if storageURL != "" {
		if st, err := storage.Open(storageURL, cfg.Storage.Token); err != nil {
			slog.Warn("artifact store unavailable", "url", redactStorageURL(storageURL), "err", err)
			p.storageErr = err
		} else {
			p.store = st
			p.storageURL = storageURL
		}
	}
	servingDriver.WithStorage(p.storageURL, cfg.Storage.Token)
	p.resolver = &piperArtifactResolver{
		runRepo:    repos.Run,
		outputDir:  cfg.OutputDir,
		storageURL: p.storageURL,
	}
	// Pipeline tasks are delivered only through gRPC-connected agents.
	agentBackend := pipelinedispatch.NewAgentBackend(workloadRouter, p.grpcAgentServer, repos.Run, repos.WorkerPodPolicy)
	p.SetBackend(agentBackend)
	p.serving.manager.SetEventPublisher(p.events)
	p.notebookManager.SetEventPublisher(p.events)
	registerPipelineDBHandlers(grpcSrv, repos.Run, repos.Step, p.events, p.handleRunSuccess, agentBackend)
	p.reconcileInterruptedRuns(context.Background())

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

// ensureSystemProject seeds the reserved system project that owns
// system-scoped credentials (e.g. the artifact-storage s3 credential).
func ensureSystemProject(ctx context.Context, repo project.Repository) error {
	existing, err := repo.Get(ctx, project.SystemID)
	if err != nil {
		return err
	}
	if existing != nil {
		return nil
	}
	return repo.Create(ctx, &project.Project{
		ID:          project.SystemID,
		Name:        "System",
		Description: "Reserved project for system-scoped credentials",
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

// recoveryReconcileEvery bounds how often runCleanup's periodic pass re-runs
// recoverInterruptedRuns as a DB-truth reconciler (in addition to its one
// mandatory call at startup) — every recoveryReconcileEvery'th 15s tick, i.e.
// every 5 minutes. This is what closes the durability gap a permanently
// failed run-finalizing write would otherwise leave open indefinitely: a run
// whose terminal DB write exhausted all of persistWithRetry's attempts is
// gone from Queue.runs (so Cleanup's TTL sweep never sees it again) and
// would otherwise stay stuck non-terminal in the DB until the next process
// restart. recoverInterruptedRuns's IsTracking guard makes it safe to call
// repeatedly — it only acts on rows the DB says are still running but that
// this Queue instance is no longer actively tracking.
const recoveryReconcileEvery = 20

// runCleanup periodically reconciles workers and removes stuck queue entries.
func (p *Piper) runCleanup(ctx context.Context) {
	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()
	var tick int
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			tick++
			p.cleanupRetention(ctx)
			p.cleanupOrphanArtifacts(ctx)
			p.sweepStaleWorkerBoundRuns(ctx)
			if tick%recoveryReconcileEvery == 0 {
				p.reconcileInterruptedRuns(ctx)
			}
		}
	}
}

// staleWorkerGrace bounds how long a run-level heartbeat (pipeline.lease_renew's
// run_ids, pushed every 10s — see pkg/pipeline/worker/worker.go's leaseLoop)
// can go stale before sweepStaleWorkerBoundRuns treats the bound worker as
// permanently gone. Must comfortably exceed that 10s cadence so a handful of
// missed ticks (a brief network hiccup) doesn't spuriously kill an
// otherwise-healthy run.
const staleWorkerGrace = 60 * time.Second

// sweepStaleWorkerBoundRuns is the worker-owned-scheduling model's backstop
// against a permanently-lost worker. Unlike the old per-step model, the
// master no longer watches individual steps once dispatched — the worker's
// own scheduler (pkg/pipeline/worker/scheduler) owns that — so nothing else
// would ever notice a run whose bound worker vanished (process killed, host
// died, network partitioned for good) and force it to a terminal state
// instead of leaving it stuck "running" forever. A run is only
// force-finalized when BOTH signals agree it's actually gone — a stale
// heartbeat AND absence from the live connection registry — so a worker
// that's merely slow to report, or actively reconnecting (which reappears
// in the registry immediately, before its next heartbeat even lands), is
// never mistaken for dead. Runs whose cancel was requested while the worker
// was unreachable (see CancelRun's SetCancelRequested fallback) are
// finalized as Canceled here rather than Failed, once this same backstop
// confirms the worker they were waiting to hear back from is never coming
// back.
func (p *Piper) sweepStaleWorkerBoundRuns(ctx context.Context) {
	if p.backend == nil {
		return
	}
	runs, err := p.listRunsAcrossProjects(ctx, run.RunFilter{Status: run.StatusRunning})
	if err != nil {
		slog.Warn("sweep stale worker-bound runs: list runs failed", "err", err)
		return
	}
	cutoff := time.Now().UTC().Add(-staleWorkerGrace)
	for _, r := range runs {
		if r.WorkerID == "" {
			// Not yet dispatched (or dispatch never got far enough to bind
			// a worker) — resendUndeliveredRunDispatches's concern, not
			// this sweep's.
			continue
		}
		lastSeen := r.StartedAt
		if r.WorkerLastSeenAt != nil {
			lastSeen = *r.WorkerLastSeenAt
		}
		if lastSeen.After(cutoff) {
			continue // heartbeat (or dispatch itself, absent any heartbeat yet) still recent enough
		}
		if _, err := p.agentRegistry.Get(r.WorkerID); err == nil {
			continue // still connected — a stale DB heartbeat here just means this sweep raced a fresh push, not that the worker is gone
		}
		status := run.StatusFailed
		if r.CancelRequestedAt != nil {
			status = run.StatusCanceled
		}
		now := time.Now().UTC()
		applied, err := p.repos.Run.FinalizeStatusCAS(ctx, r.ProjectID, r.ID, status, &now)
		if err != nil {
			slog.Warn("sweep stale worker-bound run: finalize failed", "run_id", r.ID, "err", err)
			continue
		}
		if applied {
			slog.Warn("pipeline: run force-finalized, bound worker unreachable", "run_id", r.ID, "worker_id", r.WorkerID, "status", status)
		}
	}
}

// cleanupOrphanArtifacts sweeps outputDir for run directories with no
// matching DB row, excluding the default serving model dir (outputDir/models)
// when Serving.ModelDir wasn't overridden to point elsewhere — see modelDir().
func (p *Piper) cleanupOrphanArtifacts(ctx context.Context) {
	var exclude []string
	if p.cfg.Serving.ModelDir == "" {
		exclude = append(exclude, "models")
	}
	cleanupOrphanArtifacts(ctx, p.repos.Run, p.store, p.cfg.OutputDir, exclude...)
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

// resendUndeliveredRunDispatches is the worker-owned-scheduling model's
// crash-recovery reconciler — used instead of recoverInterruptedRuns
// whenever the backend supports it (see RunDispatchBackend). Unlike the old
// model, the master never held live per-step scheduling state to
// reconstruct in the first place (the worker's own scheduler owns that —
// see pkg/pipeline/worker/scheduler), so there's nothing to rebuild here.
// The only real gap a master crash can leave is a run whose
// pipeline.run_dispatch was never confirmed delivered (e.g. the master died
// between confirmRunBinding's DB write and SendRPC returning, or even
// before either). Resending is safe and idempotent: the worker's
// Registry.StartRun no-ops for a RunID it already has an active scheduler
// for, so an at-least-once resend can never start a run twice.
func (p *Piper) resendUndeliveredRunDispatches(ctx context.Context, rb pipelinedispatch.RunDispatchBackend) {
	runs, err := p.listRunsAcrossProjects(ctx, run.RunFilter{Status: run.StatusRunning})
	if err != nil {
		slog.Warn("resend run dispatches: list runs failed", "err", err)
		return
	}
	now := time.Now().UTC()
	for _, r := range runs {
		if rb.IsTracking(r.ID) {
			// Already dispatched by this process's AgentBackend instance —
			// its worker's own scheduler owns it. Resending would be
			// harmless (StartRun is idempotent) but pointless network
			// traffic on every sweep tick, so skip it.
			continue
		}
		if r.PipelineYAML == "" {
			if err := p.repos.Run.UpdateStatus(ctx, r.ProjectID, r.ID, run.StatusFailed, &now); err != nil {
				slog.Warn("resend run dispatch: mark failed (no yaml)", "run_id", r.ID, "err", err)
			}
			continue
		}
		pl, err := p.Parse([]byte(r.PipelineYAML))
		if err != nil {
			slog.Warn("resend run dispatch: parse pipeline failed", "run_id", r.ID, "err", err)
			_ = p.repos.Run.UpdateStatus(ctx, r.ProjectID, r.ID, run.StatusFailed, &now)
			continue
		}
		var params map[string]any
		if r.ParamsJSON != "" {
			_ = json.Unmarshal([]byte(r.ParamsJSON), &params)
		}
		envByStep, err := p.resolvePipelineCredentialEnv(ctx, r.ProjectID, r.ID, pl)
		if err != nil {
			slog.Warn("resend run dispatch: resolve credential env failed", "run_id", r.ID, "err", err)
			_ = p.repos.Run.UpdateStatus(ctx, r.ProjectID, r.ID, run.StatusFailed, &now)
			continue
		}
		outputDir := filepath.Join(p.cfg.OutputDir, r.ID)
		if err := rb.DispatchRun(ctx, proto.RunDispatch{
			ProjectID:    r.ProjectID,
			RunID:        r.ID,
			PipelineYAML: r.PipelineYAML,
			RunParams:    params,
			WorkDir:      ".",
			OutputDir:    outputDir,
			CreatedAt:    now,
			WorkerID:     r.WorkerID, // force placement onto the already-bound worker, if any
			Vars:         proto.BuiltinVars{ScheduledAt: r.ScheduledAt},
			Env:          envByStep,
			StorageURL:   p.storageURL,
			StorageToken: p.cfg.Storage.Token,
		}); err != nil {
			slog.Warn("resend run dispatch failed", "run_id", r.ID, "err", err)
			// Left running — retried on the next sweep tick, or eventually
			// force-finalized by sweepStaleWorkerBoundRuns if the bound
			// worker (if any) turns out to be permanently unreachable.
		}
	}
}

// reconcileInterruptedRuns is the crash-recovery entry point, called once at
// startup and periodically by runCleanup: resends pipeline.run_dispatch for
// any run this master believes is still running but can't confirm actually
// reached its bound worker (see resendUndeliveredRunDispatches) — a no-op
// when no backend is configured (SetBackend(nil) disables dispatch).
func (p *Piper) reconcileInterruptedRuns(ctx context.Context) {
	if p.backend == nil {
		return
	}
	p.resendUndeliveredRunDispatches(ctx, p.backend)
}

// retentionScheduleBatch bounds how many overflow runs cleanupScheduleRetention
// deletes per schedule per cycle, so a schedule with a very long backlog (e.g.
// max_runs just lowered on a schedule with years of history) drains over
// several 15s cycles instead of loading its entire run history in one pass.
const retentionScheduleBatch = 200

func (p *Piper) cleanupRetention(ctx context.Context) {
	runTTL := p.cfg.Retention.RunTTL
	artifactTTL := p.cfg.Retention.ArtifactTTL
	if runTTL > 0 || artifactTTL > 0 {
		// Only pull runs old enough to possibly match *either* TTL — the
		// smaller of the two positive values is the earliest cutoff either
		// branch below could act on. ListTerminalBefore does this filtering
		// in SQL (indexed on (project_id, ended_at)) instead of loading every
		// run a project has ever had, terminal or not, expired or not.
		cutoffTTL := runTTL
		if artifactTTL > 0 && (cutoffTTL <= 0 || artifactTTL < cutoffTTL) {
			cutoffTTL = artifactTTL
		}
		now := time.Now().UTC()
		cutoff := now.Add(-cutoffTTL)
		projects, err := p.repos.Project.List(ctx)
		if err != nil {
			slog.Warn("retention list projects failed", "err", err)
		}
		for _, projectRecord := range projects {
			runs, err := p.repos.Run.ListTerminalBefore(ctx, projectRecord.ID, cutoff)
			if err != nil {
				slog.Warn("retention list terminal runs failed", "project_id", projectRecord.ID, "err", err)
				continue
			}
			for _, r := range runs {
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
		// max_runs terminal runs and delete the remainder — a non-terminal run
		// doesn't consume a "kept" slot, exactly as before. The fetch is now
		// bounded to max_runs+retentionScheduleBatch instead of the schedule's
		// entire run history: if the kept quota isn't reached within that
		// window (only possible with an implausible number of non-terminal
		// runs interspersed among the newest rows), this cycle simply deletes
		// nothing for this schedule rather than risk treating an uncounted
		// run as overflow — safe to pick up next cycle.
		runs, err := p.repos.Run.List(ctx, sc.ProjectID, run.RunFilter{
			ScheduleID: sc.ID,
			Limit:      sc.MaxRuns + retentionScheduleBatch,
		})
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
	p.scheduler.Stop()
	p.stopCtx() // cancel runCleanup and any pending dispatches
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
	if identity, ok := security.IdentityFromContext(ctx); ok {
		r.CreatedBy = identity.ID
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

	envByStep, err := p.resolvePipelineCredentialEnv(ctx, opts.ProjectID, runID, pl)
	if err != nil {
		now := time.Now().UTC()
		_ = p.repos.Run.UpdateStatus(ctx, opts.ProjectID, runID, run.StatusFailed, &now)
		return "", err
	}

	if p.backend == nil {
		now := time.Now().UTC()
		_ = p.repos.Run.UpdateStatus(ctx, opts.ProjectID, runID, run.StatusFailed, &now)
		return "", fmt.Errorf("pipeline: no backend configured, cannot dispatch run")
	}
	// Hand the whole DAG to the bound worker in one message; its own local
	// scheduler (pkg/pipeline/worker/scheduler) owns dependency promotion,
	// retry, and timeout for every step from here on — see
	// docs/backend/develop.md's State Ownership section. Async so a
	// slow/failed worker round trip doesn't block this HTTP-facing call.
	//
	// Dispatch the caller-supplied pl, not opts.YAML: rerunRun's failedOnly
	// path (and retryStep) filter pl.Spec.Steps down to a subset before
	// calling startRun, while opts.YAML stays the original full manifest
	// (stored on the run row as-is as the historical record — see
	// r.PipelineYAML above). Dispatching opts.YAML here would send the
	// worker the unfiltered step list and re-run everything instead of just
	// the retried subset.
	dispatchYAML := opts.YAML
	if marshaled, merr := pipeline.Marshal(pl); merr == nil {
		dispatchYAML = string(marshaled)
	} else {
		slog.Warn("pipeline: marshal pipeline for dispatch failed, falling back to the original manifest YAML", "run_id", runID, "err", merr)
	}
	go func() {
		dispatchCtx := p.ctx
		if err := p.backend.DispatchRun(dispatchCtx, proto.RunDispatch{
			ProjectID:    opts.ProjectID,
			RunID:        runID,
			PipelineYAML: dispatchYAML,
			RunParams:    opts.Params,
			WorkDir:      ".",
			OutputDir:    outputDir,
			CreatedAt:    now,
			Vars:         opts.Vars,
			Env:          envByStep,
			StorageURL:   p.storageURL,
			StorageToken: p.cfg.Storage.Token,
		}); err != nil {
			slog.Error("pipeline: run dispatch failed", "run_id", runID, "err", err)
			// Left running: the periodic resendUndeliveredRunDispatches
			// sweep (runCleanup) will retry, and sweepStaleWorkerBoundRuns
			// eventually force-finalizes it if the target worker turns
			// out to be permanently unreachable.
		}
	}()
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
// credentialRef (explicit) > endpoint auto-match (lowest).
// Returns nil env (no error) when no credential is configured.
func (p *Piper) resolveGitEnv(ctx context.Context, projectID, runID, stepName, credentialRef, repoURL string) ([]string, error) {
	if p.credentials != nil && strings.TrimSpace(credentialRef) != "" {
		env, err := p.credentials.GitEnv(ctx, projectID, credentialRef, repoURL)
		if err == nil {
			slog.Info("git credential resolved",
				"project_id", projectID,
				"run_id", runID,
				"step", stepName,
				"repo", repoURL,
				"credential", credentialRef,
				"source", "explicit",
			)
		}
		return env, err
	}
	// Auto-match: find credential whose endpoint covers repoURL.
	if p.credentials != nil {
		best, err := p.credentials.FindGitByRepo(ctx, projectID, repoURL)
		if err != nil {
			return nil, err
		}
		if best != nil {
			env, envErr := p.credentials.GitEnv(ctx, projectID, best.Name, repoURL)
			if envErr == nil {
				slog.Info("git credential resolved",
					"project_id", projectID,
					"run_id", runID,
					"step", stepName,
					"repo", repoURL,
					"credential", best.Name,
					"source", "endpoint-auto-match",
				)
			}
			return env, envErr
		}
	}
	return nil, nil
}

func (p *Piper) resolvePipelineCredentialEnv(ctx context.Context, projectID, runID string, pl *pipeline.Pipeline) (map[string][]string, error) {
	envByStep := map[string][]string{}
	for _, step := range pl.Spec.Steps {
		var env []string

		// Git credential resolution: credentialRef > auto-match by endpoint.
		if strings.TrimSpace(step.Run.Source) == "git" && strings.TrimSpace(step.Run.Repo) != "" {
			gitEnv, err := p.resolveGitEnv(ctx, projectID, runID, step.Name, step.Run.CredentialRef, step.Run.Repo)
			if err != nil {
				return nil, fmt.Errorf("step %q git credential: %w", step.Name, err)
			}
			env = append(env, gitEnv...)
		}

		// options.env: plain values + credentialRef resolution.
		if len(step.Options.Env) > 0 {
			optEnv, err := p.credentials.ResolveEnv(ctx, projectID, step.Options.Env)
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

// SetBackend registers the execution backend runs are dispatched through —
// must implement the worker-owned scheduling model (pipeline.run_dispatch);
// see pipelinedispatch.RunDispatchBackend. Setting nil disables run dispatch
// (StartRun then fails) until another backend is configured.
func (p *Piper) SetBackend(b pipelinedispatch.RunDispatchBackend) {
	p.backend = b
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
	case artifact.TargetRemote:
		resolved := artifact.Resolved{RunID: runID, ArtifactKey: artKey}
		if strings.HasPrefix(r.storageURL, "s3://") {
			uri, err := r.artifactURI(artKey)
			if err != nil {
				return artifact.Resolved{}, err
			}
			resolved.S3URI = uri
			resolved.RemoteURI = uri
		}
		if r.storageURL == "" {
			return artifact.Resolved{}, fmt.Errorf("remote artifact delivery requires storage")
		}
		return resolved, nil
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
		return "", fmt.Errorf("remote serving requires s3 storage; HTTP artifact storage is not supported")
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

// injectStorageCredential resolves a system-scoped s3 credential and injects its
// access key material into an s3:// storage URL's query string. Non-s3 URLs are
// returned unchanged (credentialRef only applies to s3 backends). Values already
// present in the URL are not overwritten.
func injectStorageCredential(ctx context.Context, store *credential.Store, storageURL, credentialRef string) (string, error) {
	if store == nil {
		return "", fmt.Errorf("credential store unavailable")
	}
	u, err := url.Parse(storageURL)
	if err != nil {
		return "", fmt.Errorf("parse storage url: %w", err)
	}
	if u.Scheme != "s3" {
		slog.Warn("storage.credentialRef ignored: only s3:// URLs use credential injection", "scheme", u.Scheme)
		return storageURL, nil
	}
	val, err := store.ResolveS3(ctx, project.SystemID, credentialRef)
	if err != nil {
		return "", err
	}
	q := u.Query()
	setIfAbsent := func(key, value string) {
		if value != "" && q.Get(key) == "" {
			q.Set(key, value)
		}
	}
	setIfAbsent("accessKey", val.Data["access_key_id"])
	setIfAbsent("secretKey", val.Data["secret_access_key"])
	setIfAbsent("sessionToken", val.Data["session_token"])
	u.RawQuery = q.Encode()
	return u.String(), nil
}

// redactStorageURL masks the S3 credential query params (and any userinfo
// password) a storage URL may carry, so it's safe to include in log output.
func redactStorageURL(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		return "<redacted: unparseable storage url>"
	}
	if u.User != nil {
		if _, hasPassword := u.User.Password(); hasPassword {
			u.User = url.UserPassword(u.User.Username(), "***")
		}
	}
	q := u.Query()
	for _, key := range []string{"accessKey", "secretKey", "sessionToken"} {
		if q.Get(key) != "" {
			q.Set(key, "***")
		}
	}
	u.RawQuery = q.Encode()
	return u.String()
}

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
