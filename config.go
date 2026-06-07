package piper

import (
	"database/sql"
	"fmt"
	"time"

	storemod "github.com/piper/piper/internal/store"
	corev1 "k8s.io/api/core/v1"
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

	// Storage selects the artifact store backend.
	// When empty, falls back to S3 (if S3.Bucket is set) or the built-in file server.
	Storage StorageConfig `yaml:"storage" mapstructure:"storage"`

	// S3 keeps compatibility with existing piper.yaml files.
	// Prefer Storage.URL for new configurations.
	S3 S3Config `yaml:"s3" mapstructure:"s3"`

	// Server (not required in embedded mode)
	Server ServerConfig `yaml:"server" mapstructure:"server"`

	// Retention controls automatic cleanup. Zero values disable cleanup.
	Retention RetentionConfig `yaml:"retention" mapstructure:"retention"`

	// Schedule controls cron/once scheduling behavior.
	Schedule ScheduleConfig `yaml:"schedule" mapstructure:"schedule"`

	// Pipeline controls pipeline dispatch mode.
	Pipeline PipelineConfig `yaml:"pipeline" mapstructure:"pipeline"`

	// K8s controls whether pipeline execution is delegated to a cluster-local K8s worker.
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

// StorageConfig holds artifact store configuration.
type StorageConfig struct {
	// URL selects the storage backend.
	// Supported schemes: s3://, gs://, azblob://, file://, http://, https://
	// When empty, falls back to S3Config (backward compat) or the built-in file server.
	URL string `yaml:"url" mapstructure:"url"`

	// Disabled turns off the artifact store entirely.
	// When true, Piper runs without blobstore-backed artifact storage.
	Disabled bool `yaml:"disabled" mapstructure:"disabled"`

	// Token is an optional Bearer token for HTTP-based stores.
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
	Addr      string    `yaml:"addr"       mapstructure:"addr"`
	AgentAddr string    `yaml:"agent_addr" mapstructure:"agent_addr"` // gRPC agent server, e.g. ":9090"
	Token     string    `yaml:"token"      mapstructure:"token"`
	TLS       TLSConfig `yaml:"tls"        mapstructure:"tls"`
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

// PipelineConfig controls pipeline task dispatch behaviour.
type PipelineConfig struct {
	// DispatchMode selects how pipeline tasks are delivered to workers.
	//   "agent"   – tasks are pushed to gRPC-connected workers (baremetal or K8s).
	//   "polling" – tasks sit in a queue; workers pull via /api/tasks/next.
	// Default: "polling" for backward compatibility.
	// Set to "agent" for new installations using gRPC pipeline workers.
	DispatchMode string `yaml:"dispatch_mode" mapstructure:"dispatch_mode"`
}

// K8sConfig holds server-side K8s worker dispatch configuration.
type K8sConfig struct {
	// Worker delegates pipeline K8s Job lifecycle to a cluster-local K8s worker.
	Worker bool `yaml:"worker" mapstructure:"worker"`
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

	// Mode selects the bare-metal notebook runtime: process or docker.
	Mode string `yaml:"mode" mapstructure:"mode"`

	// Docker configures Docker-backed notebook isolation when Mode is docker.
	Docker NotebookWorkerDockerConfig `yaml:"docker" mapstructure:"docker"`
}

type NotebookWorkerDockerConfig struct {
	Image        string                       `yaml:"image"          mapstructure:"image"`
	Network      string                       `yaml:"network"        mapstructure:"network"`
	CPUs         string                       `yaml:"cpus"           mapstructure:"cpus"`
	Memory       string                       `yaml:"memory"         mapstructure:"memory"`
	ShmSize      string                       `yaml:"shm_size"       mapstructure:"shm_size"`
	ReadOnlyRoot bool                         `yaml:"read_only_root" mapstructure:"read_only_root"`
	Tmpfs        []string                     `yaml:"tmpfs"          mapstructure:"tmpfs"`
	User         string                       `yaml:"user"           mapstructure:"user"`
	Volumes      []NotebookWorkerDockerVolume `yaml:"volumes"        mapstructure:"volumes"`
	ExtraArgs    []string                     `yaml:"extra_args"     mapstructure:"extra_args"`
}

type NotebookWorkerDockerVolume struct {
	Name          string `yaml:"name"           mapstructure:"name"`
	HostPath      string `yaml:"host_path"      mapstructure:"host_path"`
	ContainerPath string `yaml:"container_path" mapstructure:"container_path"`
	ReadOnly      bool   `yaml:"read_only"      mapstructure:"read_only"`
}

// NotebookK8sConfig holds server-side K8s notebook worker dispatch configuration.
type NotebookK8sConfig struct {
	// Worker delegates notebook lifecycle to a cluster-local K8s worker instead of
	// letting piper-server call the K8s API directly.
	Worker bool `yaml:"worker" mapstructure:"worker"`

	// Namespace is a placement hint passed to the K8s worker for notebook StatefulSets and PVCs.
	Namespace string `yaml:"namespace" mapstructure:"namespace"`

	// WorkerImage is the default Jupyter container image (e.g. jupyter/scipy-notebook:latest).
	WorkerImage string `yaml:"worker_image" mapstructure:"worker_image"`

	// StorageClass is the PVC storage class. Empty string uses the cluster default.
	StorageClass string `yaml:"storage_class" mapstructure:"storage_class"`

	// StorageSize is the PVC size (e.g. "10Gi"). Defaults to "10Gi".
	StorageSize string `yaml:"storage_size" mapstructure:"storage_size"`

	// PodDefaults sets cluster-wide defaults applied to every notebook pod.
	// Uses native corev1.PodTemplateSpec format — same as a Kubernetes manifest.
	// Per-notebook spec.k8s.pod_template overlays on top; piper required fields are always last.
	PodDefaults corev1.PodTemplateSpec `yaml:"pod_defaults" mapstructure:"pod_defaults"`
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

	if !c.Storage.Disabled && c.Storage.URL == "" && c.S3.Bucket != "" {
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

	switch c.NotebookWorker.Mode {
	case "", "process", "docker":
	default:
		return fmt.Errorf("notebook_worker.mode must be one of: process, docker")
	}

	return nil
}
