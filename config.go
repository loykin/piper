package piper

import (
	"database/sql"
	"fmt"
	"time"

	storemod "github.com/piper/piper/internal/store"
	"github.com/piper/piper/pkg/pipeline"
)

// Config is the global piper configuration. Accepts a struct and can be embedded.
type Config struct {
	// Execution
	// MaxRetries is the number of retry attempts after the first failure.
	// 0 means no retries (fail immediately). Negative values use the default (2).
	MaxRetries  int           `yaml:"max_retries"  mapstructure:"max_retries"`
	RetryDelay  time.Duration `yaml:"retry_delay"  mapstructure:"retry_delay"`
	Concurrency int           `yaml:"concurrency"  mapstructure:"concurrency"`
	OutputDir   string        `yaml:"output_dir"   mapstructure:"output_dir"`
	// DB configuration — specify only one. Priority: Repos > DB > DBDriver+DBDSN > DBPath.
	DBPath string  `yaml:"db_path"   mapstructure:"db_path"` // sqlite file path (default: output_dir/piper.db)
	DB     *sql.DB `yaml:"-" mapstructure:"-"`               // directly injected sqlite *sql.DB
	// DBDriver selects the database driver: "sqlite" (default) or "postgres".
	DBDriver string `yaml:"db_driver" mapstructure:"db_driver"`
	// DBDSN is the connection string for non-SQLite databases.
	// For PostgreSQL: "host=... port=5432 dbname=... user=... password=... sslmode=disable"
	DBDSN string `yaml:"db_dsn" mapstructure:"db_dsn"`
	// Repos is a fully-constructed store injected by the caller.
	// When set, all other DB fields are ignored and piper skips migrations.
	// Use store.NewExternalRepos() to build one from your own repository implementations.
	Repos *storemod.Repos `yaml:"-" mapstructure:"-"`

	// Hooks — all extension points. nil means no-op.
	Hooks Hooks `yaml:"-" mapstructure:"-"`

	// Git source
	Git GitConfig `yaml:"git" mapstructure:"git"`

	// S3 source
	S3 S3Config `yaml:"s3" mapstructure:"s3"`

	// Server (not required in embedded mode)
	Server ServerConfig `yaml:"server" mapstructure:"server"`

	// Retention controls automatic cleanup. Zero values disable cleanup.
	Retention RetentionConfig `yaml:"retention" mapstructure:"retention"`

	// Schedule controls cron/once scheduling behavior.
	Schedule ScheduleConfig `yaml:"schedule" mapstructure:"schedule"`

	// K8s — when configured, steps run as K8s Jobs.
	// Create a Launcher with pkg/k8s.New(cfg.K8s), then register it with p.SetBackend(launcher).
	K8s K8sConfig `yaml:"k8s" mapstructure:"k8s"`

	// Serving — model serving configuration.
	Serving ServingConfig `yaml:"serving" mapstructure:"serving"`

	// NotebookWorker — embedded/standalone notebook worker configuration.
	NotebookWorker NotebookWorkerConfig `yaml:"notebook_worker" mapstructure:"notebook_worker"`

	// NotebookK8s — K8s notebook driver configuration.
	// When WorkerImage is non-empty and a K8s clientset is available, notebooks
	// run as StatefulSets instead of delegating to a notebook-worker daemon.
	NotebookK8s NotebookK8sConfig `yaml:"notebook_k8s" mapstructure:"notebook_k8s"`
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
	Addr  string    `yaml:"addr"    mapstructure:"addr"`
	Token string    `yaml:"token"   mapstructure:"token"`
	TLS   TLSConfig `yaml:"tls"     mapstructure:"tls"`
}

type TLSConfig struct {
	Enabled  bool   `yaml:"enabled"   mapstructure:"enabled"`
	CertFile string `yaml:"cert_file" mapstructure:"cert_file"`
	KeyFile  string `yaml:"key_file"  mapstructure:"key_file"`
}

type RetentionConfig struct {
	RunTTL      time.Duration `yaml:"run_ttl"      mapstructure:"run_ttl"`
	ArtifactTTL time.Duration `yaml:"artifact_ttl" mapstructure:"artifact_ttl"`
}

type ScheduleConfig struct {
	// MisfirePolicy controls cron schedules that are overdue when the scheduler wakes up.
	// Supported values: "skip" (default), "run_once".
	MisfirePolicy string `yaml:"misfire_policy" mapstructure:"misfire_policy"`
	// MisfireGracePeriod is the delay tolerated before a due cron run is considered missed.
	MisfireGracePeriod time.Duration `yaml:"misfire_grace_period" mapstructure:"misfire_grace_period"`
}

// K8sConfig holds the configuration required for K8s Job execution.
// K8s mode is disabled when AgentImage is empty.
type K8sConfig struct {
	// Worker delegates pipeline K8s Job lifecycle to a cluster-local K8s worker.
	Worker bool `yaml:"worker" mapstructure:"worker"`

	// AgentImage: image containing the piper CLI binary (used as initContainer)
	AgentImage string `yaml:"agent_image" mapstructure:"agent_image"`

	// AgentImagePullPolicy: image pull policy for the agent initContainer.
	// Valid values: Always, IfNotPresent, Never. Defaults to Always when empty.
	AgentImagePullPolicy string `yaml:"agent_image_pull_policy" mapstructure:"agent_image_pull_policy"`

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

	// Worker delegates serving lifecycle to a worker selected through the unified registry.
	Worker bool `yaml:"worker" mapstructure:"worker"`
}

// NotebookWorkerConfig holds configuration for the embedded/standalone notebook worker.
type NotebookWorkerConfig struct {
	// NotebooksRoot is the base directory under which per-notebook work directories are created.
	// Each notebook runs in {notebooks_root}/{name}. Defaults to "./notebooks".
	NotebooksRoot string `yaml:"notebooks_root" mapstructure:"notebooks_root"`

	// PortRange is the inclusive range from which jupyter ports are auto-allocated.
	// Format: "START-END", e.g. "8888-9900". Defaults to "8888-9900".
	PortRange string `yaml:"port_range" mapstructure:"port_range"`
}

// NotebookK8sConfig holds configuration for the K8s notebook driver.
// K8s notebook mode is enabled when WorkerImage is non-empty and a K8s clientset is available.
type NotebookK8sConfig struct {
	// Worker delegates notebook lifecycle to a cluster-local K8s worker instead of
	// letting piper-server call the K8s API directly.
	Worker bool `yaml:"worker" mapstructure:"worker"`

	// Namespace is the K8s namespace for notebook StatefulSets and PVCs.
	Namespace string `yaml:"namespace" mapstructure:"namespace"`

	// WorkerImage is the Jupyter container image (e.g. jupyter/scipy-notebook:latest).
	// Setting this field enables the K8s notebook driver.
	WorkerImage string `yaml:"worker_image" mapstructure:"worker_image"`

	// StorageClass is the PVC storage class. Empty string uses the cluster default.
	StorageClass string `yaml:"storage_class" mapstructure:"storage_class"`

	// StorageSize is the PVC size (e.g. "10Gi"). Defaults to "10Gi".
	StorageSize string `yaml:"storage_size" mapstructure:"storage_size"`

	// PodDefaults sets cluster-wide defaults for notebook pods.
	// Individual notebook specs can override these per-field.
	PodDefaults NotebookPodDefaults `yaml:"pod_defaults" mapstructure:"pod_defaults"`
}

// NotebookPodDefaults holds cluster-wide default resource/scheduling settings for notebook pods.
type NotebookPodDefaults struct {
	Resources    pipeline.Resources    `yaml:"resources"     mapstructure:"resources"`
	NodeSelector map[string]string     `yaml:"node_selector" mapstructure:"node_selector"`
	Tolerations  []pipeline.Toleration `yaml:"tolerations"   mapstructure:"tolerations"`
	Annotations  map[string]string     `yaml:"annotations"   mapstructure:"annotations"`
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
		Schedule: ScheduleConfig{
			MisfirePolicy:      "skip",
			MisfireGracePeriod: time.Minute,
		},
	}
}

func (c Config) Validate() error {
	if c.Server.TLS.Enabled {
		if c.Server.TLS.CertFile == "" || c.Server.TLS.KeyFile == "" {
			return fmt.Errorf("server.tls enabled but cert_file or key_file is not set")
		}
	}

	if c.S3.Bucket != "" {
		if c.S3.Endpoint == "" {
			return fmt.Errorf("source.s3.bucket requires source.s3.endpoint")
		}
		if c.S3.AccessKey == "" || c.S3.SecretKey == "" {
			return fmt.Errorf("source.s3.bucket requires source.s3.access_key and source.s3.secret_key")
		}
	}

	switch c.Schedule.MisfirePolicy {
	case "", "skip", "run_once":
	default:
		return fmt.Errorf("schedule.misfire_policy must be one of: skip, run_once")
	}
	if c.Schedule.MisfireGracePeriod < 0 {
		return fmt.Errorf("schedule.misfire_grace_period must not be negative")
	}

	if c.K8s.AgentImage != "" {
		if c.K8s.MasterURL == "" {
			return fmt.Errorf("k8s.agent_image requires k8s.master_url")
		}
		if c.K8s.InCluster && c.K8s.Kubeconfig != "" {
			return fmt.Errorf("k8s.in_cluster and k8s.kubeconfig are mutually exclusive")
		}
		if c.K8s.Token == "" && c.Server.Token != "" {
			return fmt.Errorf("k8s.token is required when server.token is set")
		}
	}

	return nil
}
