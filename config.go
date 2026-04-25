package piper

import (
	"database/sql"
	"time"
)

// Config is the global piper configuration. Accepts a struct and can be embedded.
type Config struct {
	// Execution
	MaxRetries  int           `yaml:"max_retries"  mapstructure:"max_retries"`
	RetryDelay  time.Duration `yaml:"retry_delay"  mapstructure:"retry_delay"`
	Concurrency int           `yaml:"concurrency"  mapstructure:"concurrency"`
	OutputDir   string        `yaml:"output_dir"   mapstructure:"output_dir"`
	// DB configuration — specify only one. Priority: DB > DBDriver+DBDSN > DBPath
	DBPath   string  `yaml:"db_path"   mapstructure:"db_path"`   // sqlite file path (default: output_dir/piper.db)
	DBDriver string  `yaml:"db_driver" mapstructure:"db_driver"` // external DB driver, e.g. "postgres"
	DBDSN    string  `yaml:"db_dsn"    mapstructure:"db_dsn"`    // external DB DSN
	DB       *sql.DB `yaml:"-" mapstructure:"-"`                 // directly injected *sql.DB

	// Hooks — all extension points. nil means no-op.
	Hooks Hooks `yaml:"-" mapstructure:"-"`

	// Git source
	Git GitConfig `yaml:"git" mapstructure:"git"`

	// S3 source
	S3 S3Config `yaml:"s3" mapstructure:"s3"`

	// Server (not required in embedded mode)
	Server ServerConfig `yaml:"server" mapstructure:"server"`

	// K8s — when configured, steps run as K8s Jobs.
	// Create a Launcher with pkg/k8s.New(cfg.K8s), then register it with p.SetDispatcher(launcher).
	K8s K8sConfig `yaml:"k8s" mapstructure:"k8s"`

	// Serving — model serving configuration.
	Serving ServingConfig `yaml:"serving" mapstructure:"serving"`
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

// K8sConfig holds the configuration required for K8s Job execution.
// K8s mode is disabled when AgentImage is empty.
type K8sConfig struct {
	// AgentImage: image containing the piper-agent binary (used as initContainer)
	AgentImage string `yaml:"agent_image" mapstructure:"agent_image"`

	// Namespace: K8s namespace in which to create Jobs (default: "default")
	Namespace string `yaml:"namespace" mapstructure:"namespace"`

	// InCluster: set to true when running inside a Pod
	InCluster bool `yaml:"in_cluster" mapstructure:"in_cluster"`

	// Kubeconfig: path to kubeconfig file for out-of-cluster execution
	Kubeconfig string `yaml:"kubeconfig" mapstructure:"kubeconfig"`

	// MasterURL: URL used by K8s Pods to reach the piper server
	// e.g. "http://piper-server.default.svc.cluster.local:8080"
	MasterURL string `yaml:"master_url" mapstructure:"master_url"`

	// Token: auth token for the piper server
	Token string `yaml:"token" mapstructure:"token"`

	// DefaultImage: fallback container image when a step has no image configured
	DefaultImage string `yaml:"default_image" mapstructure:"default_image"`

	// TTLAfterFinished: seconds after which a finished Job is automatically deleted. 0 means no auto-deletion.
	TTLAfterFinished int32 `yaml:"ttl_after_finished" mapstructure:"ttl_after_finished"`
}

// ServingConfig holds configuration for model serving (ModelService).
type ServingConfig struct {
	// ModelDir is the local directory where model artifacts are downloaded before serving.
	// Defaults to output_dir/models.
	ModelDir string `yaml:"model_dir" mapstructure:"model_dir"`
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
