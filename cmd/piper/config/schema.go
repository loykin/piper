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
	WorkerToken    string          `mapstructure:"worker_token" yaml:"worker_token"`
	AuthSigningKey string          `mapstructure:"auth_signing_key" yaml:"auth_signing_key"`
	TLS            TLSConfig       `mapstructure:"tls" yaml:"tls"`
	DB             DBConfig        `mapstructure:"db" yaml:"db"`
	DataDir        string          `mapstructure:"data_dir" yaml:"data_dir"`
	Retention      RetentionConfig `mapstructure:"retention" yaml:"retention"`
	Schedule       ScheduleConfig  `mapstructure:"schedule" yaml:"schedule"`
	Serving        ServerServing   `mapstructure:"serving" yaml:"serving"`
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
	MasterURL    string            `mapstructure:"master_url" yaml:"master_url"`
	WorkerToken  string            `mapstructure:"worker_token" yaml:"worker_token"`
	StorageToken string            `mapstructure:"storage_token" yaml:"storage_token"`
	Hostname     string            `mapstructure:"hostname" yaml:"hostname"`
	StateDir     string            `mapstructure:"state_dir" yaml:"state_dir"`
	Labels       map[string]string `mapstructure:"labels" yaml:"labels"`
}

type PipelineWorkerConfig struct {
	Label       string               `mapstructure:"label" yaml:"label"`
	Runtime     string               `mapstructure:"runtime" yaml:"runtime"`
	Concurrency int                  `mapstructure:"concurrency" yaml:"concurrency"`
	OutputDir   string               `mapstructure:"output_dir" yaml:"output_dir"`
	MetaDir     string               `mapstructure:"meta_dir" yaml:"meta_dir"`
	Docker      PipelineDockerConfig `mapstructure:"docker" yaml:"docker"`
}

type PipelineDockerConfig struct {
	Network string `mapstructure:"network" yaml:"network"`
}

type NotebookWorkerConfig struct {
	GPUs          []string             `mapstructure:"gpus" yaml:"gpus"`
	Mode          string               `mapstructure:"mode" yaml:"mode"`
	NotebooksRoot string               `mapstructure:"notebooks_root" yaml:"notebooks_root"`
	PortRange     string               `mapstructure:"port_range" yaml:"port_range"`
	Docker        NotebookDockerConfig `mapstructure:"docker" yaml:"docker"`
}

type NotebookDockerConfig struct {
	Network string                 `mapstructure:"network" yaml:"network"`
	Volumes []NotebookDockerVolume `mapstructure:"volumes" yaml:"volumes"`
}

type NotebookDockerVolume struct {
	Name          string `mapstructure:"name" yaml:"name"`
	HostPath      string `mapstructure:"host_path" yaml:"host_path"`
	ContainerPath string `mapstructure:"container_path" yaml:"container_path"`
	ReadOnly      bool   `mapstructure:"read_only" yaml:"read_only"`
}

type ServingWorkerConfig struct {
	GPUs   []string            `mapstructure:"gpus" yaml:"gpus"`
	Mode   string              `mapstructure:"mode" yaml:"mode"`
	Docker ServingDockerConfig `mapstructure:"docker" yaml:"docker"`
}

type ServingDockerConfig struct {
	Network string `mapstructure:"network" yaml:"network"`
}

type K8sWorkerConfig struct {
	Cluster         string            `mapstructure:"cluster" yaml:"cluster"`
	Namespaces      []string          `mapstructure:"namespaces" yaml:"namespaces"`
	Enabled         []string          `mapstructure:"enabled" yaml:"enabled"`
	Kubeconfig      string            `mapstructure:"kubeconfig" yaml:"kubeconfig"`
	InCluster       bool              `mapstructure:"in_cluster" yaml:"in_cluster"`
	ResultOutboxDir string            `mapstructure:"result_outbox_dir" yaml:"result_outbox_dir"`
	Pipeline        K8sPipelineConfig `mapstructure:"pipeline" yaml:"pipeline"`
	Notebook        K8sNotebookConfig `mapstructure:"notebook" yaml:"notebook"`
}

type K8sPipelineConfig struct {
	RunnerImage           string `mapstructure:"runner_image" yaml:"runner_image"`
	RunnerImagePullPolicy string `mapstructure:"runner_image_pull_policy" yaml:"runner_image_pull_policy"`
}

type K8sNotebookConfig struct {
	InfrastructureImage string `mapstructure:"infrastructure_image" yaml:"infrastructure_image"`
}
