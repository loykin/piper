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
	"github.com/piper/piper/pkg/pipeline"
	"github.com/piper/piper/pkg/proto"
	"github.com/piper/piper/pkg/source"
	"github.com/piper/piper/pkg/store"
)

// Piper는 라이브러리 진입점.
// data-voyager 같은 프로젝트에서 임베딩해서 사용.
//
//	p := piper.New(piper.DefaultConfig())
//	result, err := p.RunFile(ctx, "train.yaml")
type Piper struct {
	cfg      Config
	store    *store.Store
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
	p := &Piper{cfg: cfg, store: st, queue: newQueue(st), registry: newWorkerRegistry(st)}
	go p.runCleanup()
	return p, nil
}

// runCleanup은 주기적으로 만료된 worker를 정리한다.
func (p *Piper) runCleanup() {
	ticker := time.NewTicker(workerTTL / 2)
	defer ticker.Stop()
	for range ticker.C {
		p.registry.cleanup()
	}
}

// Close는 store를 닫는다
func (p *Piper) Close() error {
	return p.store.Close()
}

// openStore는 Config 우선순위에 따라 Store를 생성한다
//
//	우선순위: DB(주입) > DBDriver+DBDSN > DBPath(sqlite)
func openStore(cfg Config) (*store.Store, error) {
	// 1. 외부 *sql.DB 직접 주입
	if cfg.DB != nil {
		return store.New(cfg.DB)
	}
	// 2. 드라이버 + DSN (postgres 등)
	if cfg.DBDriver != "" && cfg.DBDSN != "" {
		return store.NewWithDSN(cfg.DBDriver, cfg.DBDSN)
	}
	// 3. sqlite 파일 경로 (기본)
	dbPath := cfg.DBPath
	if dbPath == "" {
		dbPath = cfg.OutputDir + "/piper.db"
	}
	return store.Open(dbPath)
}

// RunFile은 YAML 파일 경로를 받아 파이프라인을 로컬에서 실행한다
func (p *Piper) RunFile(ctx context.Context, path string) (*pipeline.RunResult, error) {
	pl, err := pipeline.ParseFile(path)
	if err != nil {
		return nil, err
	}
	return p.RunPipeline(ctx, pl)
}

// Run은 YAML 바이트를 받아 파이프라인을 로컬에서 실행한다
func (p *Piper) Run(ctx context.Context, yamlBytes []byte) (*pipeline.RunResult, error) {
	pl, err := pipeline.Parse(yamlBytes)
	if err != nil {
		return nil, err
	}
	return p.RunPipeline(ctx, pl)
}

// RunPipeline은 파싱된 Pipeline 구조체를 직접 실행한다
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

		// 로그 캡처: store에 저장
		var stdoutW, stderrW io.Writer = os.Stdout, os.Stderr
		if runID != "" {
			stdoutW = &storeLogWriter{store: p.store, runID: runID, stepName: step.Name, stream: "stdout", tee: os.Stdout}
			stderrW = &storeLogWriter{store: p.store, runID: runID, stepName: step.Name, stream: "stderr", tee: os.Stderr}
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

// Parse는 YAML을 파싱만 한다 (실행 없이 검증 용도)
func (p *Piper) Parse(yamlBytes []byte) (*pipeline.Pipeline, error) {
	return pipeline.Parse(yamlBytes)
}

// ParseFile은 파일을 파싱만 한다
func (p *Piper) ParseFile(path string) (*pipeline.Pipeline, error) {
	return pipeline.ParseFile(path)
}

// Handler는 piper HTTP API 핸들러를 반환한다.
// 라이브러리 사용자가 자신의 라우터에 마운트할 수 있다.
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

// SetDispatcher는 K8s Job launcher 등 외부 실행 환경을 등록한다.
// 설정 시 task가 ready 상태가 되는 즉시 Dispatch가 호출된다.
// nil을 설정하면 worker 폴링 모드로 돌아간다.
func (p *Piper) SetDispatcher(d proto.Dispatcher) {
	p.queue.setDispatcher(d)
}

func (p *Piper) Config() Config {
	return p.cfg
}

func (p *Piper) SourceConfig() source.Config {
	return p.sourceConfig()
}
