package config

import "time"

type RootConfig struct {
	Version int           `mapstructure:"version" yaml:"version"`
	Log     LogConfig     `mapstructure:"log" yaml:"log"`
	Storage StorageConfig `mapstructure:"storage" yaml:"storage"`
	Source  SourceConfig  `mapstructure:"source" yaml:"source"`
	Server  ServerConfig  `mapstructure:"server" yaml:"server"`
	Workers WorkersConfig `mapstructure:"workers" yaml:"workers"`
}

type LogConfig struct {
	Format string `mapstructure:"format" yaml:"format"`
	Level  string `mapstructure:"level" yaml:"level"`
}

type StorageConfig struct {
	URL      string `mapstructure:"url" yaml:"url"`
	Disabled bool   `mapstructure:"disabled" yaml:"disabled"`
	Token    string `mapstructure:"token" yaml:"token"`
}

type SourceConfig struct {
	Git GitConfig `mapstructure:"git" yaml:"git"`
}

type GitConfig struct {
	User  string `mapstructure:"user" yaml:"user"`
	Token string `mapstructure:"token" yaml:"token"`
}

type ServerConfig struct {
	HTTPAddr       string          `mapstructure:"http_addr" yaml:"http_addr"`
	AgentAddr      string          `mapstructure:"agent_addr" yaml:"agent_addr"`
	WorkerToken    string          `mapstructure:"worker_token" yaml:"worker_token"`
	AuthSigningKey string          `mapstructure:"auth_signing_key" yaml:"auth_signing_key"`
	TLS            TLSConfig       `mapstructure:"tls" yaml:"tls"`
	DB             DBConfig        `mapstructure:"db" yaml:"db"`
	Run            RunConfig       `mapstructure:"run" yaml:"run"`
	Retention      RetentionConfig `mapstructure:"retention" yaml:"retention"`
	Schedule       ScheduleConfig  `mapstructure:"schedule" yaml:"schedule"`
	Serving        ServerServing   `mapstructure:"serving" yaml:"serving"`
	Notebook       ServerNotebook  `mapstructure:"notebook" yaml:"notebook"`
	Local          LocalConfig     `mapstructure:"local" yaml:"local"`
}

type TLSConfig struct {
	Enabled  bool   `mapstructure:"enabled" yaml:"enabled"`
	CertFile string `mapstructure:"cert_file" yaml:"cert_file"`
	KeyFile  string `mapstructure:"key_file" yaml:"key_file"`
}

type DBConfig struct {
	Driver string `mapstructure:"driver" yaml:"driver"`
	DSN    string `mapstructure:"dsn" yaml:"dsn"`
	Path   string `mapstructure:"path" yaml:"path"`
}

type RunConfig struct {
	OutputDir   string        `mapstructure:"output_dir" yaml:"output_dir"`
	Retries     int           `mapstructure:"retries" yaml:"retries"`
	RetryDelay  time.Duration `mapstructure:"retry_delay" yaml:"retry_delay"`
	Concurrency int           `mapstructure:"concurrency" yaml:"concurrency"`
}

type RetentionConfig struct {
	RunTTL      time.Duration `mapstructure:"run_ttl" yaml:"run_ttl"`
	ArtifactTTL time.Duration `mapstructure:"artifact_ttl" yaml:"artifact_ttl"`
}

type ScheduleConfig struct {
	MisfirePolicy      string        `mapstructure:"misfire_policy" yaml:"misfire_policy"`
	MisfireGracePeriod time.Duration `mapstructure:"misfire_grace_period" yaml:"misfire_grace_period"`
}

type ServerServing struct {
	ModelDir string `mapstructure:"model_dir" yaml:"model_dir"`
	Delegate bool   `mapstructure:"delegate" yaml:"delegate"`
}

type ServerNotebook struct {
	Delegate bool              `mapstructure:"delegate" yaml:"delegate"`
	K8s      K8sNotebookConfig `mapstructure:"k8s" yaml:"k8s"`
}

type LocalConfig struct {
	Enabled  bool `mapstructure:"enabled" yaml:"enabled"`
	Pipeline bool `mapstructure:"pipeline" yaml:"pipeline"`
	Notebook bool `mapstructure:"notebook" yaml:"notebook"`
	Serving  bool `mapstructure:"serving" yaml:"serving"`
}

type WorkersConfig struct {
	Common   WorkerCommonConfig   `mapstructure:"common" yaml:"common"`
	Pipeline PipelineWorkerConfig `mapstructure:"pipeline" yaml:"pipeline"`
	Notebook NotebookWorkerConfig `mapstructure:"notebook" yaml:"notebook"`
	Serving  ServingWorkerConfig  `mapstructure:"serving" yaml:"serving"`
	K8s      K8sWorkerConfig      `mapstructure:"k8s" yaml:"k8s"`
}

type WorkerCommonConfig struct {
	MasterURL    string `mapstructure:"master_url" yaml:"master_url"`
	AgentAddr    string `mapstructure:"agent_addr" yaml:"agent_addr"`
	WorkerToken  string `mapstructure:"worker_token" yaml:"worker_token"`
	StorageToken string `mapstructure:"storage_token" yaml:"storage_token"`
	Hostname     string `mapstructure:"hostname" yaml:"hostname"`
}

type PipelineWorkerConfig struct {
	ID          string               `mapstructure:"id" yaml:"id"`
	Label       string               `mapstructure:"label" yaml:"label"`
	Runtime     string               `mapstructure:"runtime" yaml:"runtime"`
	Concurrency int                  `mapstructure:"concurrency" yaml:"concurrency"`
	OutputDir   string               `mapstructure:"output_dir" yaml:"output_dir"`
	MetaDir     string               `mapstructure:"meta_dir" yaml:"meta_dir"`
	Docker      PipelineDockerConfig `mapstructure:"docker" yaml:"docker"`
}

type PipelineDockerConfig struct {
	DefaultImage string `mapstructure:"default_image" yaml:"default_image"`
	Network      string `mapstructure:"network" yaml:"network"`
}

type NotebookWorkerConfig struct {
	ID            string               `mapstructure:"id" yaml:"id"`
	GPUs          []string             `mapstructure:"gpus" yaml:"gpus"`
	Mode          string               `mapstructure:"mode" yaml:"mode"`
	NotebooksRoot string               `mapstructure:"notebooks_root" yaml:"notebooks_root"`
	PortRange     string               `mapstructure:"port_range" yaml:"port_range"`
	Docker        NotebookDockerConfig `mapstructure:"docker" yaml:"docker"`
}

type NotebookDockerConfig struct {
	Image        string                 `mapstructure:"image" yaml:"image"`
	Network      string                 `mapstructure:"network" yaml:"network"`
	CPUs         string                 `mapstructure:"cpus" yaml:"cpus"`
	Memory       string                 `mapstructure:"memory" yaml:"memory"`
	ShmSize      string                 `mapstructure:"shm_size" yaml:"shm_size"`
	ReadOnlyRoot bool                   `mapstructure:"read_only_root" yaml:"read_only_root"`
	Tmpfs        []string               `mapstructure:"tmpfs" yaml:"tmpfs"`
	User         string                 `mapstructure:"user" yaml:"user"`
	Volumes      []NotebookDockerVolume `mapstructure:"volumes" yaml:"volumes"`
	ExtraArgs    []string               `mapstructure:"extra_args" yaml:"extra_args"`
}

type NotebookDockerVolume struct {
	Name          string `mapstructure:"name" yaml:"name"`
	HostPath      string `mapstructure:"host_path" yaml:"host_path"`
	ContainerPath string `mapstructure:"container_path" yaml:"container_path"`
	ReadOnly      bool   `mapstructure:"read_only" yaml:"read_only"`
}

type ServingWorkerConfig struct {
	ID     string              `mapstructure:"id" yaml:"id"`
	GPUs   []string            `mapstructure:"gpus" yaml:"gpus"`
	Mode   string              `mapstructure:"mode" yaml:"mode"`
	Docker ServingDockerConfig `mapstructure:"docker" yaml:"docker"`
}

type ServingDockerConfig struct {
	Image   string `mapstructure:"image" yaml:"image"`
	Network string `mapstructure:"network" yaml:"network"`
}

type K8sWorkerConfig struct {
	ID              string            `mapstructure:"id" yaml:"id"`
	Cluster         string            `mapstructure:"cluster" yaml:"cluster"`
	Namespaces      []string          `mapstructure:"namespaces" yaml:"namespaces"`
	Enabled         []string          `mapstructure:"enabled" yaml:"enabled"`
	Kubeconfig      string            `mapstructure:"kubeconfig" yaml:"kubeconfig"`
	InCluster       bool              `mapstructure:"in_cluster" yaml:"in_cluster"`
	ResultOutboxDir string            `mapstructure:"result_outbox_dir" yaml:"result_outbox_dir"`
	Pipeline        K8sPipelineConfig `mapstructure:"pipeline" yaml:"pipeline"`
	Notebook        K8sNotebookConfig `mapstructure:"notebook" yaml:"notebook"`
	Serving         K8sServingConfig  `mapstructure:"serving" yaml:"serving"`
}

type K8sPipelineConfig struct {
	Namespace            string `mapstructure:"namespace" yaml:"namespace"`
	WorkerImage          string `mapstructure:"worker_image" yaml:"worker_image"`
	DefaultImage         string `mapstructure:"default_image" yaml:"default_image"`
	AgentImagePullPolicy string `mapstructure:"agent_image_pull_policy" yaml:"agent_image_pull_policy"`
}

type K8sNotebookConfig struct {
	Namespace    string         `mapstructure:"namespace" yaml:"namespace"`
	Image        string         `mapstructure:"image" yaml:"image"`
	StorageClass string         `mapstructure:"storage_class" yaml:"storage_class"`
	StorageSize  string         `mapstructure:"storage_size" yaml:"storage_size"`
	PodDefaults  map[string]any `mapstructure:"pod_defaults" yaml:"pod_defaults"`
}

type K8sServingConfig struct {
	Namespace string `mapstructure:"namespace" yaml:"namespace"`
}
