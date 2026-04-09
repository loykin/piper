package piper

import (
	"database/sql"
	"time"
)

// Config는 piper 전체 설정. 구조체로 받아서 임베딩 가능.
type Config struct {
	// 실행
	MaxRetries  int           `yaml:"max_retries"  mapstructure:"max_retries"`
	RetryDelay  time.Duration `yaml:"retry_delay"  mapstructure:"retry_delay"`
	Concurrency int           `yaml:"concurrency"  mapstructure:"concurrency"`
	OutputDir   string        `yaml:"output_dir"   mapstructure:"output_dir"`
	// DB 설정 — 셋 중 하나만 지정. 우선순위: DB > DBDriver+DBDSN > DBPath
	DBPath   string  `yaml:"db_path"   mapstructure:"db_path"`   // sqlite 파일 경로 (기본: output_dir/piper.db)
	DBDriver string  `yaml:"db_driver" mapstructure:"db_driver"` // "postgres" 등 외부 DB 드라이버
	DBDSN    string  `yaml:"db_dsn"    mapstructure:"db_dsn"`    // 외부 DB DSN
	DB       *sql.DB `yaml:"-" mapstructure:"-"`                 // 직접 주입 (*sql.DB)

	// Hooks — 모든 확장 포인트. nil이면 no-op.
	Hooks Hooks `yaml:"-" mapstructure:"-"`

	// Git 소스
	Git GitConfig `yaml:"git" mapstructure:"git"`

	// S3 소스
	S3 S3Config `yaml:"s3" mapstructure:"s3"`

	// 서버 (임베딩 모드에서는 불필요)
	Server ServerConfig `yaml:"server" mapstructure:"server"`

	// K8s — 설정 시 K8s Job으로 step을 실행한다.
	// pkg/k8s.New(cfg.K8s)로 Launcher를 생성한 뒤 p.SetDispatcher(launcher)로 등록.
	K8s K8sConfig `yaml:"k8s" mapstructure:"k8s"`
}

type GitConfig struct {
	User  string `yaml:"user"  mapstructure:"user"`
	Token string `yaml:"token" mapstructure:"token"`
}

type S3Config struct {
	Endpoint  string `yaml:"endpoint"   mapstructure:"endpoint"`
	AccessKey string `yaml:"access_key" mapstructure:"access_key"`
	SecretKey string `yaml:"secret_key" mapstructure:"secret_key"`
	Bucket    string `yaml:"bucket"     mapstructure:"bucket"`
	UseSSL    bool   `yaml:"use_ssl"    mapstructure:"use_ssl"`
}

type ServerConfig struct {
	Addr string    `yaml:"addr"    mapstructure:"addr"`
	TLS  TLSConfig `yaml:"tls"     mapstructure:"tls"`
}

type TLSConfig struct {
	Enabled  bool   `yaml:"enabled"   mapstructure:"enabled"`
	CertFile string `yaml:"cert_file" mapstructure:"cert_file"`
	KeyFile  string `yaml:"key_file"  mapstructure:"key_file"`
}

// K8sConfig는 K8s Job 실행에 필요한 설정.
// AgentImage가 비어 있으면 K8s 모드 비활성.
type K8sConfig struct {
	// AgentImage: piper-agent 바이너리가 포함된 이미지 (initContainer용)
	AgentImage string `yaml:"agent_image" mapstructure:"agent_image"`

	// Namespace: Job을 생성할 K8s 네임스페이스 (기본: "default")
	Namespace string `yaml:"namespace" mapstructure:"namespace"`

	// InCluster: Pod 내부 실행 시 true
	InCluster bool `yaml:"in_cluster" mapstructure:"in_cluster"`

	// Kubeconfig: out-of-cluster 실행 시 kubeconfig 파일 경로
	Kubeconfig string `yaml:"kubeconfig" mapstructure:"kubeconfig"`

	// MasterURL: K8s Pod에서 piper server에 접근할 URL
	// 예: "http://piper-server.default.svc.cluster.local:8080"
	MasterURL string `yaml:"master_url" mapstructure:"master_url"`

	// Token: piper server 인증 토큰
	Token string `yaml:"token" mapstructure:"token"`

	// DefaultImage: step에 image가 없을 때 사용할 기본 컨테이너 이미지
	DefaultImage string `yaml:"default_image" mapstructure:"default_image"`

	// TTLAfterFinished: Job 완료 후 자동 삭제 시간(초). 0이면 자동 삭제 안 함.
	TTLAfterFinished int32 `yaml:"ttl_after_finished" mapstructure:"ttl_after_finished"`
}

func DefaultConfig() Config {
	return Config{
		MaxRetries:  2,
		RetryDelay:  5 * time.Second,
		Concurrency: 4,
		OutputDir:   "./piper-outputs",
		Server: ServerConfig{
			Addr: ":8080",
		},
	}
}
