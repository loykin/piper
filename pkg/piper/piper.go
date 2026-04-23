package piper

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"gopkg.in/yaml.v3"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"

	s3sdk "github.com/aws/aws-sdk-go-v2/service/s3"

	"github.com/piper/piper/pkg/executor"
	"github.com/piper/piper/pkg/logstore"
	"github.com/piper/piper/pkg/pipeline"
	"github.com/piper/piper/pkg/proto"
	"github.com/piper/piper/pkg/serving"
	"github.com/piper/piper/pkg/source"
	"github.com/piper/piper/pkg/store"
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
	cfg      Config
	store    *store.Store
	logs     logstore.LogStore
	queue    *queue
	registry *workerRegistry
	serving  servingBundle
	s3Cli    *s3sdk.Client // nil when S3 not configured
}

func New(cfg Config) (*Piper, error) {
	def := DefaultConfig()
	if cfg.OutputDir == "" {
		cfg.OutputDir = def.OutputDir
	}
	if cfg.MaxRetries == 0 {
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

	if err := os.MkdirAll(cfg.OutputDir, 0755); err != nil {
		return nil, fmt.Errorf("create output dir: %w", err)
	}

	st, err := openStore(cfg)
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
	servingMgr := serving.New(st, modelDir, k8sClientset)
	q := newQueue(st)
	p := &Piper{
		cfg:      cfg,
		store:    st,
		logs:     st.LogStore(),
		queue:    q,
		registry: newWorkerRegistry(st),
		serving: servingBundle{
			manager: servingMgr,
			proxy:   serving.NewProxy(st),
		},
	}
	if cfg.S3.Bucket != "" && cfg.S3.AccessKey != "" {
		if s3c, err := newPiperS3Client(cfg.S3); err != nil {
			slog.Warn("artifact S3 client unavailable", "err", err)
		} else {
			p.s3Cli = s3c
		}
	}
	q.onRunSuccess = p.handleRunSuccess
	go p.runCleanup()
	go p.runScheduler()
	return p, nil
}

// handleRunSuccess is called (in a goroutine) when a queued run completes successfully.
// It triggers on_success.deploy if configured in the pipeline spec.
func (p *Piper) handleRunSuccess(runID string, pl *pipeline.Pipeline) {
	if pl.Spec.OnSuccess == nil || pl.Spec.OnSuccess.Deploy == nil {
		return
	}
	trigger := pl.Spec.OnSuccess.Deploy
	svc, err := p.store.GetService(trigger.Service)
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
	if _, err := p.DeployService(context.Background(), updatedYAML); err != nil {
		// Non-fatal: log and continue
		_ = err
	}
}

// runCleanup periodically removes expired workers.
func (p *Piper) runCleanup() {
	ticker := time.NewTicker(workerTTL / 2)
	defer ticker.Stop()
	for range ticker.C {
		p.registry.cleanup()
	}
}

// Close closes the store
func (p *Piper) Close() error {
	return p.store.Close()
}

// openStore creates a Store according to the Config priority rules
//
//	Priority: DB (injected) > DBDriver+DBDSN > DBPath (sqlite)
func openStore(cfg Config) (*store.Store, error) {
	// 1. Directly injected *sql.DB
	if cfg.DB != nil {
		return store.New(cfg.DB)
	}
	// 2. Driver + DSN (postgres, etc.)
	if cfg.DBDriver != "" && cfg.DBDSN != "" {
		return store.NewWithDSN(cfg.DBDriver, cfg.DBDSN)
	}
	// 3. sqlite file path (default)
	dbPath := cfg.DBPath
	if dbPath == "" {
		dbPath = cfg.OutputDir + "/piper.db"
	}
	return store.Open(dbPath)
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

	srcCfg := p.sourceConfig()
	outputDir := p.cfg.OutputDir

	execFn := func(ctx context.Context, step *pipeline.Step) error {
		stepOutputDir := filepath.Join(outputDir, step.Name)
		if err := os.MkdirAll(stepOutputDir, 0755); err != nil {
			return err
		}

		// Capture logs: persist to store
		var stdoutW, stderrW io.Writer = os.Stdout, os.Stderr
		if runID != "" {
			stdoutW = &storeLogWriter{logs: p.logs, runID: runID, stepName: step.Name, stream: "stdout", tee: os.Stdout}
			stderrW = &storeLogWriter{logs: p.logs, runID: runID, stepName: step.Name, stream: "stderr", tee: os.Stderr}
		}

		exec := executor.New(step)
		return exec.Execute(ctx, step, executor.ExecConfig{
			WorkDir:   ".",
			InputDir:  outputDir,
			OutputDir: stepOutputDir,
			Params:    mergeParams(step.Params, opts.Params),
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

// Parse parses YAML only (for validation without execution)
func (p *Piper) Parse(yamlBytes []byte) (*pipeline.Pipeline, error) {
	return pipeline.Parse(yamlBytes)
}

// ParseFile parses a file only
func (p *Piper) ParseFile(path string) (*pipeline.Pipeline, error) {
	return pipeline.ParseFile(path)
}

// Handler returns the piper HTTP API handler.
// Library users can mount it on their own router.
//
//	mux.Handle("/piper/", http.StripPrefix("/piper", p.Handler(nil)))
func (p *Piper) Handler(extra http.Handler) http.Handler {
	return newAPIHandler(p, extra, p.store)
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

// SetDispatcher registers an external execution environment such as a K8s Job launcher.
// When set, Dispatch is called immediately whenever a task becomes ready.
// Setting nil reverts to worker polling mode.
func (p *Piper) SetDispatcher(d proto.Dispatcher) {
	p.queue.setDispatcher(d)
}

func (p *Piper) Config() Config {
	return p.cfg
}

// DeployService parses a ModelService YAML and deploys it.
// It resolves the artifact (downloading if needed) and starts the runtime process or K8s Deployment.
func (p *Piper) DeployService(ctx context.Context, yamlBytes []byte) (*store.Service, error) {
	var svc serving.ModelService
	if err := yaml.Unmarshal(yamlBytes, &svc); err != nil {
		return nil, fmt.Errorf("parse ModelService YAML: %w", err)
	}
	if svc.Metadata.Name == "" {
		return nil, fmt.Errorf("ModelService metadata.name is required")
	}

	// Resolve artifact directory
	artifactDir, runID, err := p.resolveArtifactDir(ctx, svc)
	if err != nil {
		return nil, err
	}

	if err := p.serving.manager.Deploy(ctx, svc, artifactDir); err != nil {
		return nil, fmt.Errorf("deploy service: %w", err)
	}

	// Persist YAML and run_id
	rec, err := p.store.GetService(svc.Metadata.Name)
	if err != nil || rec == nil {
		return nil, fmt.Errorf("get service after deploy: %w", err)
	}
	rec.YAML = string(yamlBytes)
	rec.RunID = runID
	if err := p.store.UpdateService(rec); err != nil {
		return nil, fmt.Errorf("update service record: %w", err)
	}
	return rec, nil
}

// StopService stops the named service.
func (p *Piper) StopService(ctx context.Context, name string) error {
	return p.serving.manager.Stop(ctx, name)
}

// RestartService re-deploys the named service using its stored YAML.
func (p *Piper) RestartService(ctx context.Context, name string) error {
	rec, err := p.store.GetService(name)
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
func (p *Piper) ListServices(_ context.Context) ([]*store.Service, error) {
	return p.store.ListServices()
}

// GetService returns a single service by name.
func (p *Piper) GetService(_ context.Context, name string) (*store.Service, error) {
	return p.store.GetService(name)
}

// resolveArtifactDir finds the local directory (or S3 URI) for the artifact
// referenced by the ModelService's from_artifact spec.
func (p *Piper) resolveArtifactDir(_ context.Context, svc serving.ModelService) (artifactDir, runID string, err error) {
	ref := svc.Spec.Model.FromArtifact
	if ref == nil {
		return "", "", fmt.Errorf("spec.model.from_artifact is required")
	}

	if ref.Run == "latest" || ref.Run == "" {
		run, err := p.store.GetLatestSuccessfulRun(ref.Pipeline)
		if err != nil {
			return "", "", fmt.Errorf("lookup latest run for pipeline %q: %w", ref.Pipeline, err)
		}
		if run == nil {
			return "", "", fmt.Errorf("no successful run found for pipeline %q", ref.Pipeline)
		}
		runID = run.ID
	} else {
		runID = ref.Run
	}

	// Local artifact path: outputDir/runID/stepName/artifactName (or subfolder)
	modelDir := p.cfg.Serving.ModelDir
	if modelDir == "" {
		modelDir = filepath.Join(p.cfg.OutputDir, "models")
	}
	artifactDir = serving.ArtifactLocalPath(p.cfg.OutputDir, runID, ref.Step, ref.Artifact)
	_ = modelDir
	return artifactDir, runID, nil
}

func (p *Piper) SourceConfig() source.Config {
	return p.sourceConfig()
}

// SetServingK8sClientset builds a k8s clientset from the given kubeconfig path
// and injects it into the serving manager. Call this after New() when the
// kubeconfig path is only available at runtime (e.g. via CLI flag).
func (p *Piper) SetServingK8sClientset(kubeconfig string) error {
	cs, err := buildK8sClientset(K8sConfig{Kubeconfig: kubeconfig})
	if err != nil {
		return fmt.Errorf("build k8s clientset: %w", err)
	}
	p.serving.manager.SetK8sClientset(cs)
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

// mergeParams merges step-level params (base) with run-level params (override).
// Run-level params take precedence, allowing runtime hyperparameter injection
// without modifying the pipeline YAML.
func mergeParams(stepParams, runParams map[string]any) map[string]any {
	if len(runParams) == 0 {
		return stepParams
	}
	merged := make(map[string]any, len(stepParams)+len(runParams))
	for k, v := range stepParams {
		merged[k] = v
	}
	for k, v := range runParams {
		merged[k] = v // run-level overrides step-level
	}
	return merged
}
