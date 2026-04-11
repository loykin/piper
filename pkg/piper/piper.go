package piper

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/piper/piper/pkg/executor"
	"github.com/piper/piper/pkg/logstore"
	"github.com/piper/piper/pkg/pipeline"
	"github.com/piper/piper/pkg/proto"
	"github.com/piper/piper/pkg/source"
	"github.com/piper/piper/pkg/store"
)

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
	p := &Piper{cfg: cfg, store: st, logs: st.LogStore(), queue: newQueue(st), registry: newWorkerRegistry(st)}
	go p.runCleanup()
	return p, nil
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
	return p.runPipelineWithRunID(ctx, pl, "")
}

func (p *Piper) runPipelineWithRunID(ctx context.Context, pl *pipeline.Pipeline, runID string) (*pipeline.RunResult, error) {
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
			Params:    step.Params,
			SourceCfg: srcCfg,
			Stdout:    stdoutW,
			Stderr:    stderrW,
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

func (p *Piper) SourceConfig() source.Config {
	return p.sourceConfig()
}
