package piper

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	iagent "github.com/loykin/piper/internal/agent"
	ialerting "github.com/loykin/piper/internal/alerting"
	"github.com/loykin/piper/internal/artifact"
	"github.com/loykin/piper/internal/event"
	"github.com/loykin/piper/internal/logsink"
	"github.com/loykin/piper/internal/logstore"
	"github.com/loykin/piper/internal/pipelinedispatch"
	"github.com/loykin/piper/internal/proto"
	"github.com/loykin/piper/internal/queue"
	"github.com/loykin/piper/internal/runlifecycle"
	ischeduler "github.com/loykin/piper/internal/scheduler"
	"github.com/loykin/piper/internal/srcfetch"
	"github.com/loykin/piper/pkg/alerting"
	"github.com/loykin/piper/pkg/credential"
	"github.com/loykin/piper/pkg/federation"
	"github.com/loykin/piper/pkg/integration/mlflow"
	"github.com/loykin/piper/pkg/integration/outbox"
	"github.com/loykin/piper/pkg/notebook"
	"github.com/loykin/piper/pkg/notebook/execution"
	notebookmcp "github.com/loykin/piper/pkg/notebook/execution/mcp"
	"github.com/loykin/piper/pkg/pipeline"
	"github.com/loykin/piper/pkg/pipeline/run"
	"github.com/loykin/piper/pkg/project"
	"github.com/loykin/piper/pkg/serving"
	"github.com/loykin/piper/pkg/statsstore"
	"github.com/loykin/piper/pkg/storage"

	storemod "github.com/loykin/piper/internal/store"
)

// notebookLocalRuntimeID is the fixed identity used by the in-process
// notebook direct-runtime driver (docker/baremetal), matching the
// localDockerRuntimeID/localBaremetalRuntimeID convention already used by
// internal/pipelinedispatch for pipeline direct-runtime.
const notebookLocalRuntimeID = "piper-notebook-runtime"

// servingLocalRuntimeID is the equivalent fixed identity for the in-process
// serving direct-runtime driver.
const servingLocalRuntimeID = "piper-serving-runtime"

// notebookK8sLocalRuntimeID is the fixed identity for the in-process K8s
// notebook direct-runtime driver — distinct from notebookLocalRuntimeID
// (docker/baremetal) even though only one is ever active per Piper
// instance, matching internal/pipelinedispatch's per-infra constant convention.
const notebookK8sLocalRuntimeID = "piper-notebook-k8s-runtime"

// servingK8sLocalRuntimeID is the equivalent fixed identity for the
// in-process K8s serving direct-runtime driver.
const servingK8sLocalRuntimeID = "piper-serving-k8s-runtime"

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
	cfg                Config
	ctx                context.Context // cancelled on Close; passed to background goroutines and hooks
	repos              *storemod.Repos
	logs               logstore.LogStore
	metrics            logstore.MetricStore
	stats              *statsstore.Store
	queue              *queue.Queue
	serving            servingBundle
	notebookManager    *notebook.Manager
	notebookExecutions *execution.Service       // docs/jupyter-mcp-execution.md Phase 1: Kernel session / NotebookExecution domain service
	notebookMCP        *notebookmcp.Handler     // docs/jupyter-mcp-execution.md Phase 2: read-only MCP endpoint over notebookExecutions; nil unless cfg.MCP.Enabled and notebookExecutions is available
	nbWorkspace        notebook.WorkspaceReader // reads a notebook volume's live workspace files, for pipeline template snapshotting
	store              storage.Store            // nil when no artifact store configured
	credentials        *credential.Store
	alerts             *alerting.Service
	alertEngine        *ialerting.Engine
	federationSvc      *federation.Service
	storageURL         string // resolved storage URL (for K8s launcher, artifact resolver)
	storageErr         error  // last artifact store open error, if any
	// storageIdentity is the non-secret storage-identity (storageIdentity()
	// in settings.go) computed from storageURL at the same point p.store is
	// assigned, below. It is the single source of truth both the
	// stamping-at-write-time code (Run/Template Create) and the
	// mismatch-check-at-read-time code (artifact download, viewer, template
	// snapshot, ModelService from_artifact) compare against.
	storageIdentity string
	resolver        artifact.Resolver // central artifact resolver
	backend         pipelinedispatch.ExecutionBackend
	events          *event.Hub
	scheduler       *ischeduler.Scheduler
	startedAt       time.Time // wall-clock when New() ran; used for misfire detection
	runs            *runlifecycle.Manager
	mlflowClients   mlflow.ClientFactory // per-integration MLflow REST client factory; used by both the dispatcher (piper.go's New) and the Integrations REST handler's connection-test endpoint (member_project.go)

	stopCtx context.CancelFunc // cancels ctx on Close
	wg      sync.WaitGroup
}

func New(cfg Config) (*Piper, error) {
	def := DefaultConfig()
	if cfg.OutputDir == "" {
		cfg.OutputDir = def.OutputDir
	}
	// OutputDir must be absolute: it backs Docker bind-mount sources (which
	// reject relative host paths outright — see the docker driver's
	// ".results" mount) and is compared against a LocalStore's always-absolute
	// Root() during the orphan-artifact sweep. Resolving it once here, rather
	// than expecting every downstream consumer to know to do it, is what a
	// relative output_dir (the common case — see config/piper.yaml's default
	// "./piper-data") requires to work with every runtime.type.
	if abs, err := filepath.Abs(cfg.OutputDir); err != nil {
		return nil, fmt.Errorf("resolve output dir: %w", err)
	} else {
		cfg.OutputDir = abs
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
	if cfg.Stats.Spool == (StatsSpoolConfig{}) {
		cfg.Stats.Spool = def.Stats.Spool
	}
	if cfg.Stats.Logs == (StatsBackendConfig{}) {
		cfg.Stats.Logs = def.Stats.Logs
	}
	if cfg.Stats.Metrics == (StatsBackendConfig{}) {
		cfg.Stats.Metrics = def.Stats.Metrics
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
	localStats := cfg.Stats
	if localStats.Logs.URL != "" {
		localStats.Logs.ManageRetention = false
	}
	if localStats.Metrics.URL != "" {
		localStats.Metrics.ManageRetention = false
	}
	if err := validateStatsRetentionSupport(localStats, repos.Log, repos.Metric); err != nil {
		_ = repos.Close()
		return nil, err
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
	statsBackend := logstore.NewBackend(repos.Log, repos.Metric)
	spoolDir := cfg.Stats.Spool.Dir
	if spoolDir == "" {
		spoolDir = filepath.Join(cfg.OutputDir, "stats-spool")
	}
	stats, err := statsstore.Open(statsstore.Config{
		SpoolDir: spoolDir, SpoolMaxBytes: cfg.Stats.Spool.MaxBytes,
		Logs:    statsstore.BackendConfig{URL: cfg.Stats.Logs.URL, CredentialRef: cfg.Stats.Logs.CredentialRef, Retention: cfg.Stats.Logs.Retention, ManageRetention: cfg.Stats.Logs.ManageRetention},
		Metrics: statsstore.BackendConfig{URL: cfg.Stats.Metrics.URL, CredentialRef: cfg.Stats.Metrics.CredentialRef, Retention: cfg.Stats.Metrics.Retention, ManageRetention: cfg.Stats.Metrics.ManageRetention},
		Resolve: func(ctx context.Context, ref string) (map[string]string, error) {
			value, resolveErr := credentialStore.Resolve(ctx, project.SystemID, ref)
			return value.Data, resolveErr
		},
	}, statsstore.Fallback{Logs: statsBackend, Metrics: statsBackend, Capabilities: statsBackend.Capabilities()})
	if err != nil {
		_ = repos.Close()
		return nil, fmt.Errorf("open statistics store: %w", err)
	}
	statsAdapter := logstore.NewStatsAdapter(stats)
	if cfg.Stats.Logs.URL != "" {
		repos.Log = statsAdapter
	}
	if cfg.Stats.Metrics.URL != "" {
		repos.Metric = statsAdapter
	}
	if cfg.Auth.Factory != nil {
		authConfig, err := cfg.Auth.Factory(AuthDependencies{
			DB:            repos.DB(),
			Driver:        repos.Driver(),
			SecureCookies: cfg.Server.TLS.Enabled,
			Executor:      repos.Executor(),
		})
		if err != nil {
			_ = stats.Close()
			_ = repos.Close()
			return nil, fmt.Errorf("create auth capabilities: %w", err)
		}
		if authConfig.Factory != nil {
			_ = stats.Close()
			_ = repos.Close()
			return nil, fmt.Errorf("create auth capabilities: nested factory is not allowed")
		}
		cfg.Auth = authConfig
		if err := cfg.Validate(); err != nil {
			_ = stats.Close()
			_ = repos.Close()
			return nil, err
		}
	}
	modelDir := cfg.Serving.ModelDir
	if modelDir == "" {
		modelDir = filepath.Join(cfg.OutputDir, "models")
	}
	if err := os.MkdirAll(modelDir, 0755); err != nil {
		_ = stats.Close()
		_ = repos.Close()
		return nil, fmt.Errorf("create model dir: %w", err)
	}

	servingRuntime, err := composeServingRuntime(cfg, repos, credentialStore)
	if err != nil {
		_ = stats.Close()
		_ = repos.Close()
		return nil, err
	}
	notebookRuntime, err := composeNotebookRuntime(cfg, repos, credentialStore)
	if err != nil {
		_ = stats.Close()
		_ = repos.Close()
		return nil, err
	}
	bgCtx, stopFn := context.WithCancel(context.Background())
	q := queue.NewQueue(bgCtx, repos.Run, repos.Step)
	if cfg.Queue.MaxAttempts > 0 || cfg.Queue.RetryDelay > 0 {
		q.SetRetryPolicy(cfg.Queue.MaxAttempts, cfg.Queue.RetryDelay)
	}
	q.SetRecoveryGracePeriod(cfg.Queue.RecoveryGrace)

	p := &Piper{
		cfg:           cfg,
		ctx:           bgCtx,
		repos:         repos,
		logs:          repos.Log,
		metrics:       repos.Metric,
		stats:         stats,
		credentials:   credentialStore,
		federationSvc: federation.NewService(repos.Project, repos.Federation),
		queue:         q,
		serving: servingBundle{
			manager: servingRuntime.manager,
			proxy:   serving.NewProxy(repos.Serving),
		},
		notebookManager: notebookRuntime.manager,
		nbWorkspace:     notebookRuntime.workspace,
		stopCtx:         stopFn,
		events:          event.NewHub(),
	}
	// Registered here (as opposed to inline in pkg/credential) so the
	// generic credential package stays free of any specific consumer's
	// knowledge — this closure is Piper's own storage-config awareness.
	// Checked against both the live (booted) CredentialRef and whatever is
	// currently *pending* in storage.yaml (UpdateStorageSettings may have
	// saved a not-yet-applied change that references this credential, even
	// if the running process itself booted with a different one) — deleting
	// either would break the credential resolution the next restart, or the
	// current one, depends on. See ErrInUse's doc comment.
	credentialStore.AddInUseChecker(func(_ context.Context, projectID, name string) (string, bool) {
		if projectID != project.SystemID {
			return "", false
		}
		if strings.TrimSpace(p.cfg.Storage.CredentialRef) == name {
			return "referenced by the running server's storage.credentialRef", true
		}
		if pending, exists, err := p.readStorageSettings(); err == nil && exists && strings.TrimSpace(pending.CredentialRef) == name {
			return "referenced by a pending (not yet applied) storage config change", true
		}
		return "", false
	})
	// docs/jupyter-mcp-execution.md Phase 1. bgCtx (== p.ctx) is passed
	// explicitly rather than reading p.ctx back off the struct: execution
	// runs asynchronously in goroutines that must outlive any single HTTP
	// request and must observe Close()'s cancellation the same way every
	// other background component here does (see NewService's doc comment).
	//
	// Guarded like repos.AlertRule below: an embedder supplying its own
	// cfg.Repos (see ExternalReposConfig) may not populate this brand-new
	// field, and the feature must degrade to "not available" rather than
	// panic on first use.
	if repos.NotebookExecution != nil {
		p.notebookExecutions = execution.NewService(bgCtx, execution.Deps{
			Repo:      repos.NotebookExecution,
			Notebooks: repos.Notebook,
			Gateway:   execution.NewGateway(),
			Events:    p.events,
			Limits:    execution.DefaultLimits(),
		})
		// docs/jupyter-mcp-execution.md Phase 2. Guarded by both
		// cfg.MCP.Enabled (an operator must opt in explicitly, default
		// off — same convention as Integrations.Mlflow.Enabled) and this
		// same repos.NotebookExecution != nil precondition
		// notebookExecutions itself needed, since every Phase 2 tool calls
		// straight into that Service.
		if cfg.MCP.Enabled {
			p.notebookMCP = notebookmcp.NewHandler(notebookmcp.Deps{
				Notebooks:  repos.Notebook,
				Executions: p.notebookExecutions,
			}, notebookmcp.Config{
				AllowedOrigins: cfg.MCP.AllowedOrigins,
				AllowedHosts:   cfg.MCP.AllowedHosts,
				SessionTTL:     cfg.MCP.SessionTTL,
			})
		}
	}
	if repos.AlertRule != nil {
		p.alertEngine = ialerting.NewEngine(repos.AlertRule, credentialStore)
		p.alerts = alerting.NewService(repos.AlertRule, credentialStore, p.alertEngine.Refresh)
		if err := p.alertEngine.Refresh(context.Background()); err != nil {
			stopFn()
			_ = stats.Close()
			_ = repos.Close()
			return nil, fmt.Errorf("load alert rules: %w", err)
		}
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
	// Stamp with the *configured* URL (storageURL, still in scope even when
	// Open above failed), not p.storageURL (only ever set on a successful
	// Open). Using p.storageURL here would make a transient open failure —
	// wrong credentials, a network hiccup — collapse the identity to the
	// same "file" constant used for "no object storage configured at all",
	// masking the real intended backend and letting an unrelated
	// mismatch/match comparison happen by coincidence later. storageIdentity
	// only ever reads scheme/host/specific non-secret query keys, so it's
	// safe to pass the post-credential-injection URL here even though that
	// URL itself may carry injected secrets in its query string.
	p.storageIdentity = storageIdentity(storageURL)
	q.SetStorageConfig(p.storageURL, cfg.Storage.Token)
	if servingRuntime.k8sDriver != nil {
		servingRuntime.k8sDriver.WithStorage(p.storageURL, cfg.Storage.Token)
	}
	p.resolver = artifact.NewResolver(repos.Run, cfg.OutputDir, p.storageURL, p.store, p.storageIdentity)
	// startedAt is set before the scheduler exists so misfire detection works
	// on its first Add (see the scheduler wiring below).
	p.startedAt = time.Now().UTC()

	// MLflow tracking export (docs/mlflow-tracking-adapter.md, Phase 1):
	// resolve-per-integration Client factory (credential lookup +
	// SSRF-policy-bound HTTP client), the Exporter (outbox.Handler), and the
	// enqueue closure wired into runlifecycle.Deps below. None of this talks
	// to MLflow on this synchronous construction/request path — only the
	// Dispatcher goroutine started further down does, and only once an
	// event has actually been claimed (design doc section 4.3).
	mlflowSSRFPolicy := mlflow.SSRFPolicy{
		AllowInsecureHTTP: cfg.Integrations.Mlflow.AllowInsecureHTTP,
		AllowedHosts:      cfg.Integrations.Mlflow.AllowedHosts,
		AllowedCIDRs:      cfg.Integrations.Mlflow.AllowedCIDRs,
	}
	mlflowClients := func(ctx context.Context, integration *mlflow.MLflowIntegration) (mlflow.Client, error) {
		cred, err := credentialStore.ResolveMlflow(ctx, integration.ProjectID, integration.CredentialRef)
		if err != nil {
			return nil, err
		}
		return mlflow.NewHTTPClient(mlflow.HTTPClientConfig{
			TrackingURI: integration.TrackingURI,
			Token:       cred.Data["token"],
			Username:    cred.Data["username"],
			Password:    cred.Data["password"],
			CACertPEM:   cred.Data["ca_cert"],
			Policy:      mlflowSSRFPolicy,
			Timeout:     cfg.Integrations.Mlflow.RequestTimeout,
		})
	}
	p.mlflowClients = mlflowClients
	mlflowExporter := mlflow.NewExporter(repos.Mlflow, mlflowClients)
	enqueuePipelineCreated := func(ctx context.Context, r *run.Run, version int) error {
		var params map[string]any
		if r.ParamsJSON != "" {
			_ = json.Unmarshal([]byte(r.ParamsJSON), &params)
		}
		startTime := r.StartedAt
		if r.ScheduledAt != nil {
			startTime = *r.ScheduledAt
		}
		// Piper has no configured public base URL (see config.go) — this is
		// a relative API path, matching mlflow.PipelineRunCreatedPayload's
		// RunURL doc comment.
		runURL := fmt.Sprintf("/api/projects/%s/runs/%s", r.ProjectID, r.ID)
		return mlflow.EnqueuePipelineRunCreated(ctx, repos.Mlflow, repos.Outbox, r.ProjectID, r.ID, params, r.PipelineName, version, r.Experiment, r.CreatedBy, cfg.Runtime.Type, runURL, startTime)
	}
	enqueuePipelineFinished := func(ctx context.Context, projectID, runID, status string) {
		if err := mlflow.EnqueuePipelineRunFinished(ctx, repos.Mlflow, repos.Outbox, projectID, runID, status, time.Now().UTC()); err != nil {
			slog.Warn("mlflow export enqueue failed", "run_id", runID, "err", err)
		}
	}

	p.runs = runlifecycle.New(runlifecycle.Deps{
		RunRepo:                repos.Run,
		StepRepo:               repos.Step,
		ScheduleRepo:           repos.Schedule,
		SubmissionRepo:         repos.Submission,
		ProjectRepo:            repos.Project,
		ServingRepo:            repos.Serving,
		RunDeleter:             repos,
		Queue:                  q,
		Credentials:            credentialStore,
		Store:                  p.store,
		StorageIdentity:        p.storageIdentity,
		OutputDir:              cfg.OutputDir,
		RuntimeType:            cfg.Runtime.Type,
		RunTTL:                 cfg.Retention.RunTTL,
		ArtifactTTL:            cfg.Retention.ArtifactTTL,
		MisfirePolicy:          cfg.Schedule.MisfirePolicy,
		MisfireGracePeriod:     cfg.Schedule.MisfireGracePeriod,
		StartedAt:              p.startedAt,
		OnRunStart:             cfg.Hooks.OnRunStart,
		DeployService:          p.DeployService,
		DeleteArtifacts:        deleteArtifactsFromStore,
		DeleteWorkspace:        deleteRunWorkspace,
		EnqueuePipelineCreated: enqueuePipelineCreated,
	})
	backend, pipelineObserver, err := composePipelineRuntime(cfg, bgCtx, repos, q, p.events)
	if err != nil {
		stopFn()
		_ = repos.Close()
		return nil, fmt.Errorf("create %s pipeline runtime: %w", cfg.Runtime.Type, err)
	}
	p.SetBackend(backend)
	q.OnRunSuccess = p.runs.HandleRunSuccess
	// pipeline_run.finished (design doc section 7.4) is wired into
	// OnRunOutcome — queue.go's finalizeRunLocked fires this only after the
	// DB CAS committing the terminal status has already applied
	// (finalizeRunLocked's `applied` check), asynchronously via
	// appendEffect, so this is fully decoupled from the synchronous run
	// lifecycle path (design doc section 4.3) the same way the alerting/
	// OnRunEnd hooks below already are — composed into the same closure
	// rather than a second field on queue.Queue.
	q.OnRunOutcome = func(ctx context.Context, projectID, runID, status string, pl *pipeline.Pipeline) {
		enqueuePipelineFinished(ctx, projectID, runID, status)
		if p.alertEngine != nil {
			p.alertEngine.NotifyPipelineOutcome(ctx, projectID, runID, status, pl)
		}
		if cfg.Hooks.OnRunEnd != nil {
			result, err := p.buildRunResult(ctx, projectID, runID)
			if err != nil {
				slog.Warn("build OnRunEnd result failed", "run_id", runID, "err", err)
				return
			}
			cfg.Hooks.OnRunEnd(project.WithContext(ctx, project.Context{ID: projectID}), runID, result)
		}
	}
	q.SetEventPublisher(p.events)
	p.serving.manager.SetEventPublisher(p.events)
	p.notebookManager.SetEventPublisher(p.events)
	if p.alertEngine != nil {
		done := p.alertEngine.Start(p.ctx, p.events)
		p.wg.Add(1)
		go func() { defer p.wg.Done(); <-done }()
	}
	if cfg.Integrations.Mlflow.Enabled {
		dispatcherConcurrency := cfg.Integrations.Mlflow.DispatcherConcurrency
		// SQLite has no SKIP LOCKED equivalent; the outbox.Repository
		// contract (design doc section 6.3) requires concurrency 1 there
		// regardless of configured value — internal/store/sqlite's
		// ClaimBatch does a plain SELECT-then-UPDATE, not a locking claim.
		if repos.Driver() == "sqlite" {
			dispatcherConcurrency = 1
		}
		mlflowDispatcher := outbox.NewDispatcher(repos.Outbox, mlflowExporter, outbox.Config{
			Owner:                 "piper-" + uuid.NewString(),
			Concurrency:           dispatcherConcurrency,
			BatchSize:             cfg.Integrations.Mlflow.BatchSize,
			PollInterval:          cfg.Integrations.Mlflow.PollInterval,
			LeaseDuration:         cfg.Integrations.Mlflow.LeaseDuration,
			MaxAttemptsBeforeDead: cfg.Integrations.Mlflow.MaxAttemptsBeforeDead,
		})
		p.wg.Add(1)
		go func() {
			defer p.wg.Done()
			mlflowDispatcher.Run(p.ctx)
		}()
	}
	p.runs.RecoverInterruptedRuns(context.Background())
	if p.notebookExecutions != nil {
		if err := p.notebookExecutions.RecoverOnStartup(context.Background()); err != nil {
			slog.Warn("notebook execution recovery failed", "err", err)
		}
	}
	if pipelineObserver != nil {
		p.wg.Add(1)
		go func() {
			defer p.wg.Done()
			pipelineObserver.Observe(p.ctx)
		}()
	}
	if notebookRuntime.observer != nil {
		p.wg.Add(1)
		go func() {
			defer p.wg.Done()
			notebookRuntime.observer.Observe(p.ctx)
		}()
	}
	if servingRuntime.observer != nil {
		p.wg.Add(1)
		go func() {
			defer p.wg.Done()
			servingRuntime.observer.Observe(p.ctx)
		}()
	}

	// Start the in-memory scheduler and seed it from the DB.
	p.scheduler = ischeduler.New(p.runs.ScheduleFired)
	p.runs.SetScheduler(p.scheduler)
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

// SetProjectOwner updates Home's authoritative Project directory and audit
// trail atomically. It returns false when the Project does not exist yet;
// creation can subsequently apply the same owner through project.OwnerResolver.
func (p *Piper) SetProjectOwner(ctx context.Context, homeID, projectID, memberID, actorID string) (bool, error) {
	return p.federationSvc.SetProjectOwner(ctx, homeID, projectID, memberID, actorID)
}

// SyncFederationMembers reconciles Home's non-secret Member directory with
// the configured enrollment identities. Previously configured Members remain
// as disabled history records; all connections start offline after restart.
func (p *Piper) SyncFederationMembers(ctx context.Context, homeID string, memberIDs []string) error {
	return p.federationSvc.SyncMembers(ctx, homeID, memberIDs)
}

// SetFederationMemberConnected atomically updates the Member directory and
// appends its connection audit event.
func (p *Piper) SetFederationMemberConnected(ctx context.Context, homeID, memberID string, connected bool) error {
	return p.federationSvc.SetMemberConnected(ctx, homeID, memberID, connected)
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
			p.reconcileBackend(ctx)
			p.queue.Cleanup(ctx, 4*time.Hour)
			p.runs.CleanupRetention(ctx)
			p.cleanupStats(ctx)
			p.cleanupOrphanArtifacts(ctx)
			if tick%recoveryReconcileEvery == 0 {
				p.runs.RecoverInterruptedRuns(ctx)
			}
		}
	}
}

const statsRetentionBatch = 1000

// cleanupStats applies log/metric retention strictly by the stats rows' own
// timestamps. It never consults run existence, RunTTL, or schedule max_runs.
func (p *Piper) cleanupStats(ctx context.Context) {
	now := time.Now().UTC()
	if retention := p.cfg.Stats.Logs.Retention; retention > 0 && p.cfg.Stats.Logs.ManageRetention {
		if sweeper, ok := p.logs.(logstore.LogRetention); ok {
			if _, err := sweeper.SweepLogs(ctx, now.Add(-retention), statsRetentionBatch); err != nil {
				slog.Warn("stats log retention sweep failed", "err", err)
			}
		}
	}
	if retention := p.cfg.Stats.Metrics.Retention; retention > 0 && p.cfg.Stats.Metrics.ManageRetention {
		if sweeper, ok := p.metrics.(logstore.MetricRetention); ok {
			if _, err := sweeper.SweepMetrics(ctx, now.Add(-retention), statsRetentionBatch); err != nil {
				slog.Warn("stats metric retention sweep failed", "err", err)
			}
		}
	}
}

func validateStatsRetentionSupport(cfg StatsConfig, logs logstore.LogStore, metrics logstore.MetricStore) error {
	if cfg.Logs.Retention > 0 && cfg.Logs.ManageRetention {
		if _, ok := logs.(logstore.LogRetention); !ok {
			return fmt.Errorf("stats.logs.manage_retention requires a log store with retention support")
		}
	}
	if cfg.Metrics.Retention > 0 && cfg.Metrics.ManageRetention {
		if _, ok := metrics.(logstore.MetricRetention); !ok {
			return fmt.Errorf("stats.metrics.manage_retention requires a metric store with retention support")
		}
	}
	return nil
}

// cleanupOrphanArtifacts sweeps outputDir for run directories with no
// matching DB row, excluding:
//   - the default serving model dir (outputDir/models) when Serving.ModelDir
//     wasn't overridden to point elsewhere — see modelDir().
//   - artifact.CacheDirName, the local staging cache the artifact resolver
//     uses for remote-store reads.
//   - ".results", the fixed bookkeeping directory the baremetal and docker
//     direct-runtime drivers create directly under OutputDir to hold each
//     task's result/task JSON — not a run's workspace, and not swept even
//     though its name never matches a run ID.
//   - a LocalStore's own root, when one is configured and happens to live
//     directly under outputDir (the default) — that directory is the
//     artifact repository itself, not a run's workspace, and must never be
//     swept by run-ID existence (fed.md §13.6).
//   - runtime.baremetal.meta_dir, when the operator configured it as a
//     subdirectory of outputDir — it holds the baremetal driver's process
//     registry, not a run's workspace.
//   - notebook.notebooks_root, when configured under outputDir — it contains
//     persistent notebook volumes and live process state, not run workspaces.
//
// Excluding a directory requires comparing it against outputDir; both sides
// are resolved to absolute paths first, since outputDir is commonly a
// relative path (e.g. "./piper-data") while a store's Root() is always
// absolute (storage.NewLocal calls filepath.Abs) — comparing a relative
// outputDir against an absolute root made filepath.Rel fail and silently
// skip the exclusion, which is what let the sweep delete the store itself.
func (p *Piper) cleanupOrphanArtifacts(ctx context.Context) {
	exclude := []string{artifact.CacheDirName, ".results"}
	if p.cfg.Serving.ModelDir == "" {
		exclude = append(exclude, "models")
	}
	absOutputDir, err := filepath.Abs(p.cfg.OutputDir)
	if err != nil {
		slog.Warn("cleanupOrphanArtifacts: resolve absolute output dir failed, skipping sweep", "err", err)
		return
	}
	excludeUnderOutputDir := func(dir string) {
		if dir == "" {
			return
		}
		absDir, err := filepath.Abs(dir)
		if err != nil {
			return
		}
		if rel, err := filepath.Rel(absOutputDir, absDir); err == nil && rel != "." && !strings.HasPrefix(rel, "..") {
			exclude = append(exclude, strings.Split(rel, string(filepath.Separator))[0])
		}
	}
	if ls, ok := p.store.(*storage.LocalStore); ok {
		excludeUnderOutputDir(ls.Root())
	}
	if p.cfg.Runtime.Type == RuntimeBaremetal {
		excludeUnderOutputDir(p.cfg.Runtime.Baremetal.MetaDir)
	}
	excludeUnderOutputDir(p.cfg.Notebook.NotebooksRoot)
	cleanupOrphanArtifacts(ctx, p.repos.Run, p.cfg.OutputDir, exclude...)
}

type jobReconciler interface {
	ReconcileJobs(ctx context.Context, report func(context.Context, proto.TaskResult) error)
}

// localLogPushClient preserves the existing batched/redacted log path while
// replacing the worker tunnel hop with a direct write to the master-owned
// log and metric stores.
type localLogPushClient struct {
	store   logstore.LogStore
	metrics logstore.MetricStore
	events  event.Publisher
}

func (c localLogPushClient) SendPush(method string, payload any) error {
	if method != iagent.MethodLogAppend {
		return fmt.Errorf("local runtime: unsupported push method %q", method)
	}
	batch, ok := payload.(logsink.LogBatch)
	if !ok {
		return fmt.Errorf("local runtime: invalid log payload %T", payload)
	}
	lines := make([]*logstore.Line, 0, len(batch.Lines))
	metricRows := make([]*logstore.Metric, 0)
	for _, line := range batch.Lines {
		lines = append(lines, &logstore.Line{
			ProjectID: batch.ProjectID,
			RunID:     batch.RunID,
			StepName:  batch.StepName,
			Ts:        line.Ts,
			Stream:    line.Stream,
			Line:      line.Text,
		})
		if key, value, ok := parsePushedMetric(line.Text); ok && c.metrics != nil {
			metricRows = append(metricRows, &logstore.Metric{ProjectID: batch.ProjectID, RunID: batch.RunID, StepName: batch.StepName, Key: key, Value: value, Ts: line.Ts})
		}
	}
	if c.metrics != nil && len(metricRows) > 0 {
		if err := c.metrics.AppendMetrics(context.Background(), metricRows); err != nil {
			slog.Warn("metric append failed", "run_id", batch.RunID, "err", err)
		} else {
			publishMetricEvents(c.events, metricRows)
		}
	}
	if len(lines) == 0 || c.store == nil {
		return nil
	}
	return c.store.Append(context.Background(), lines)
}

// persistTaskMetrics writes a completed task's structured metrics (populated
// by the runner from the step's metrics file, see
// pkg/pipeline/worker/agent/runner.go's readFinalMetrics) to the metric
// store before the result reaches Queue.Complete.
func persistTaskMetrics(ctx context.Context, metrics logstore.MetricStore, publisher event.Publisher, result proto.TaskResult) {
	if metrics == nil || len(result.Metrics) == 0 {
		return
	}
	runID, stepName, ok := strings.Cut(result.TaskID, ":")
	if !ok {
		return
	}
	now := time.Now().UTC()
	rows := make([]*logstore.Metric, 0, len(result.Metrics))
	for key, value := range result.Metrics {
		rows = append(rows, &logstore.Metric{ProjectID: result.ProjectID, RunID: runID, StepName: stepName, Key: key, Value: value, Ts: now})
	}
	if err := metrics.AppendMetrics(ctx, rows); err != nil {
		slog.Warn("pipeline metrics persist failed", "task_id", result.TaskID, "err", err)
	} else {
		publishMetricEvents(publisher, rows)
	}
}

func publishMetricEvents(publisher event.Publisher, rows []*logstore.Metric) {
	if publisher == nil {
		return
	}
	for _, row := range rows {
		publisher.Publish(event.New(row.ProjectID, "metric.recorded", map[string]any{"run_id": row.RunID, "step_name": row.StepName, "key": row.Key, "value": row.Value, "recorded_at": row.Ts}))
	}
}

// parsePushedMetric extracts a PIPER_METRIC key=value marker from a log
// line, e.g. "PIPER_METRIC loss=0.312".
func parsePushedMetric(line string) (string, float64, bool) {
	line = strings.TrimSpace(line)
	if !strings.HasPrefix(line, "PIPER_METRIC ") {
		return "", 0, false
	}
	key, raw, ok := strings.Cut(strings.TrimSpace(strings.TrimPrefix(line, "PIPER_METRIC ")), "=")
	if !ok {
		return "", 0, false
	}
	value, err := strconv.ParseFloat(strings.TrimSpace(raw), 64)
	key = strings.TrimSpace(key)
	return key, value, key != "" && err == nil
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

// queueDrainGrace bounds how long Close waits for the queue's own in-flight
// goroutines (dispatch calls, fired timeout/retry/recovery-grace timers) to
// finish flushing before giving up and tearing down the DB out from under
// them anyway.
const queueDrainGrace = 20 * time.Second

// Close stops background goroutines and closes the store.
func (p *Piper) Close() error {
	p.scheduler.Stop()
	// Drain the queue's own goroutines *before* cancelling p.ctx (== the
	// queue's serverCtx) and closing the DB: several of them persist a
	// write using that same ctx/repos, and cutting either out from under a
	// goroutine that's mid-flush would silently drop the write. Bounded so a
	// goroutine that's genuinely stuck can't hang shutdown forever.
	drainCtx, cancel := context.WithTimeout(context.Background(), queueDrainGrace)
	defer cancel()
	if err := p.queue.Close(drainCtx); err != nil {
		slog.Warn("queue drain did not finish before shutdown grace expired", "err", err)
	}
	p.stopCtx() // cancel runCleanup and any pending dispatches — also cancels notebookExecutions' background context
	p.wg.Wait()
	// Wait for any in-flight NotebookExecution runs to observe the
	// cancellation above and unwind before the DB closes underneath them.
	// finishExecution itself always persists through a fresh
	// context.Background()-derived context (see service_run.go), so this is
	// purely about not racing repos.Close() with those writers, not about
	// giving them extra time to finish executing code.
	if p.notebookExecutions != nil {
		execShutdownCtx, execCancel := context.WithTimeout(context.Background(), queueDrainGrace)
		p.notebookExecutions.Shutdown(execShutdownCtx)
		execCancel()
	}
	// DockerBackend holds a real Docker daemon client connection that must be
	// closed explicitly (unlike kubernetes.Interface, which needs no closing).
	if closer, ok := p.backend.(interface{ Close() error }); ok {
		_ = closer.Close()
	}
	return errors.Join(p.stats.Close(), p.repos.Close())
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

// RunFile runs a pipeline YAML file through the configured execution backend.
func (p *Piper) RunFile(ctx context.Context, path string) (*pipeline.RunResult, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return p.Run(ctx, data)
}

// Run runs a pipeline YAML through the configured execution backend.
func (p *Piper) Run(ctx context.Context, yamlBytes []byte) (*pipeline.RunResult, error) {
	return p.runWithConfiguredBackend(ctx, yamlBytes, RunOptions{})
}

// RunPipeline runs a parsed Pipeline through the configured execution backend.
func (p *Piper) RunPipeline(ctx context.Context, pl *pipeline.Pipeline) (*pipeline.RunResult, error) {
	return p.RunPipelineOpts(ctx, pl, RunOptions{})
}

// RunPipelineOpts runs a parsed Pipeline with options through the full dispatch stack.
func (p *Piper) RunPipelineOpts(ctx context.Context, pl *pipeline.Pipeline, opts RunOptions) (*pipeline.RunResult, error) {
	data, err := pipeline.Marshal(pl)
	if err != nil {
		return nil, err
	}
	return p.runWithConfiguredBackend(ctx, data, opts)
}

func (p *Piper) runWithConfiguredBackend(ctx context.Context, yamlBytes []byte, opts RunOptions) (*pipeline.RunResult, error) {
	events, unsub := p.events.Subscribe()
	defer unsub()

	projectID := opts.ProjectID
	if projectID == "" {
		projectID = project.DefaultID
	}
	if existing, err := p.repos.Project.Get(ctx, projectID); err != nil {
		return nil, fmt.Errorf("run: get project: %w", err)
	} else if existing == nil {
		if err := p.repos.Project.Create(ctx, &project.Project{ID: projectID, Name: projectID}); err != nil {
			return nil, fmt.Errorf("run: create project: %w", err)
		}
	}
	projectCtx := project.WithContext(ctx, project.Context{ID: projectID})
	runID, err := p.runs.StartRunFromAPI(projectCtx, string(yamlBytes), opts.Params, opts.Vars, "")
	if err != nil {
		return nil, err
	}
	if _, err := waitForRunCompleted(ctx, events, runID); err != nil {
		return nil, fmt.Errorf("run: wait for completion: %w", err)
	}
	return p.buildRunResult(ctx, projectID, runID)
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

func (p *Piper) SourceConfig() srcfetch.Config {
	return p.sourceConfig()
}

// modelDir returns the local directory for a serving model.
func (p *Piper) modelDir(serviceName string) string {
	if p.cfg.Serving.ModelDir != "" {
		return filepath.Join(p.cfg.Serving.ModelDir, serviceName)
	}
	return filepath.Join(p.cfg.OutputDir, "models", serviceName)
}

// ResolveStorageURL derives the effective storage URL from the config.
// Priority: Storage.Disabled -> empty; Storage.URL > file://{output_dir}/store.
func (cfg Config) ResolveStorageURL() string { return resolveStorageURL(cfg) }

// injectStorageCredential resolves a system-scoped credential and injects its
// access key material into a storage URL's query string. The credential kind
// must match the URL's scheme (s3 credential for s3://, gcs for gs://, azure
// for azblob://); other schemes are returned unchanged. Values already
// present in the URL are not overwritten.
func injectStorageCredential(ctx context.Context, store *credential.Store, storageURL, credentialRef string) (string, error) {
	if store == nil {
		return "", fmt.Errorf("credential store unavailable")
	}
	u, err := url.Parse(storageURL)
	if err != nil {
		return "", fmt.Errorf("parse storage url: %w", err)
	}

	q := u.Query()
	setIfAbsent := func(key, value string) {
		if value != "" && q.Get(key) == "" {
			q.Set(key, value)
		}
	}

	switch u.Scheme {
	case "s3":
		val, err := store.ResolveS3(ctx, project.SystemID, credentialRef)
		if err != nil {
			return "", err
		}
		setIfAbsent("accessKey", val.Data["access_key_id"])
		setIfAbsent("secretKey", val.Data["secret_access_key"])
		setIfAbsent("sessionToken", val.Data["session_token"])
	case "gs":
		val, err := store.ResolveGCS(ctx, project.SystemID, credentialRef)
		if err != nil {
			return "", err
		}
		setIfAbsent("serviceAccountKey", base64.StdEncoding.EncodeToString([]byte(val.Data["service_account_json"])))
	case "azblob":
		val, err := store.ResolveAzure(ctx, project.SystemID, credentialRef)
		if err != nil {
			return "", err
		}
		setIfAbsent("accountName", val.Data["account_name"])
		setIfAbsent("accountKey", base64.StdEncoding.EncodeToString([]byte(val.Data["account_key"])))
	default:
		slog.Warn("storage.credentialRef ignored: scheme does not support credential injection", "scheme", u.Scheme)
		return storageURL, nil
	}
	u.RawQuery = q.Encode()
	return u.String(), nil
}

// redactStorageURL masks credential query params (and any userinfo password)
// a storage URL may carry, so it's safe to include in log output.
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
	for _, key := range []string{"accessKey", "secretKey", "sessionToken", "serviceAccountKey", "accountKey"} {
		if q.Get(key) != "" {
			q.Set(key, "***")
		}
	}
	u.RawQuery = q.Encode()
	return u.String()
}

// resolveStorageURL is the internal implementation.
// Priority: Storage.Disabled -> empty; Storage.URL > file://{output_dir}/store (built-in).
func resolveStorageURL(cfg Config) string {
	if cfg.Storage.Disabled {
		return ""
	}
	if cfg.Storage.URL != "" {
		return cfg.Storage.URL
	}
	// Default: built-in file server under output directory.
	outputDir := cfg.OutputDir
	if outputDir == "" {
		outputDir = "./piper-outputs"
	}
	return "file://" + filepath.Join(outputDir, "store")
}
