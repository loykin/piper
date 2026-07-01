package config

import "time"

type RootConfig struct {
	Version int           `mapstructure:"version" yaml:"version"`
	Log     LogConfig     `mapstructure:"log" yaml:"log"`
	Storage StorageConfig `mapstructure:"storage" yaml:"storage"`
	Source  SourceConfig  `mapstructure:"source" yaml:"source"`
	Server  ServerConfig  `mapstructure:"server" yaml:"server"`
	Worker  WorkerConfig  `mapstructure:"worker" yaml:"worker"`
}

type LogConfig struct {
	Format string `mapstructure:"format" yaml:"format"`
	Level  string `mapstructure:"level" yaml:"level"`
}

type StorageConfig struct {
	URL           string `mapstructure:"url" yaml:"url"`
	Disabled      bool   `mapstructure:"disabled" yaml:"disabled"`
	Token         string `mapstructure:"token" yaml:"token"`
	CredentialRef string `mapstructure:"credentialRef" yaml:"credentialRef"`
}

type SourceConfig struct {
	Git GitConfig `mapstructure:"git" yaml:"git"`
}

type GitConfig struct {
	User  string `mapstructure:"user" yaml:"user"`
	Token string `mapstructure:"token" yaml:"token"`
}

type ServerConfig struct {
	HTTPAddr            string          `mapstructure:"http_addr" yaml:"http_addr"`
	WorkerToken         string          `mapstructure:"worker_token" yaml:"worker_token"`
	AuthSigningKey      string          `mapstructure:"auth_signing_key" yaml:"auth_signing_key"`
	SecretEncryptionKey string          `mapstructure:"secret_encryption_key" yaml:"secret_encryption_key"`
	TLS                 TLSConfig       `mapstructure:"tls" yaml:"tls"`
	DB                  DBConfig        `mapstructure:"db" yaml:"db"`
	DataDir             string          `mapstructure:"data_dir" yaml:"data_dir"`
	Retention           RetentionConfig `mapstructure:"retention" yaml:"retention"`
	Schedule            ScheduleConfig  `mapstructure:"schedule" yaml:"schedule"`
	Serving             ServerServing   `mapstructure:"serving" yaml:"serving"`
	Local               LocalConfig     `mapstructure:"local" yaml:"local"`
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
	Enabled     bool                `mapstructure:"enabled" yaml:"enabled"`
	Pipeline    bool                `mapstructure:"pipeline" yaml:"pipeline"`
	Notebook    bool                `mapstructure:"notebook" yaml:"notebook"`
	Serving     bool                `mapstructure:"serving" yaml:"serving"`
	Concurrency int                 `mapstructure:"concurrency" yaml:"concurrency"`
	NotebookCfg LocalNotebookConfig `mapstructure:"notebook_config" yaml:"notebook_config"`
}

type LocalNotebookConfig struct {
	NotebooksRoot string `mapstructure:"notebooks_root" yaml:"notebooks_root"`
	PortRange     string `mapstructure:"port_range" yaml:"port_range"`
}

// WorkerConfig describes exactly one standalone worker process. Exactly one of
// Baremetal, Docker, or K8s must be configured.
type WorkerConfig struct {
	MasterURL    string                   `mapstructure:"master_url" yaml:"master_url"`
	WorkerToken  string                   `mapstructure:"worker_token" yaml:"worker_token"`
	StorageToken string                   `mapstructure:"storage_token" yaml:"storage_token"`
	Hostname     string                   `mapstructure:"hostname" yaml:"hostname"`
	StateDir     string                   `mapstructure:"state_dir" yaml:"state_dir"`
	Labels       map[string]string        `mapstructure:"labels" yaml:"labels"`
	Baremetal    *BaremetalWorkerConfig   `mapstructure:"baremetal" yaml:"baremetal,omitempty"`
	Docker       *DockerWorkerConfig      `mapstructure:"docker" yaml:"docker,omitempty"`
	K8s          *K8sWorkerConfig         `mapstructure:"k8s" yaml:"k8s,omitempty"`
	Capabilities WorkerCapabilitiesConfig `mapstructure:"capabilities" yaml:"capabilities"`
}

type BaremetalWorkerConfig struct{}

type DockerWorkerConfig struct {
	Network string         `mapstructure:"network" yaml:"network"`
	Volumes []DockerVolume `mapstructure:"volumes" yaml:"volumes"`
}

type WorkerCapabilitiesConfig struct {
	Pipeline *PipelineCapabilityConfig `mapstructure:"pipeline" yaml:"pipeline,omitempty"`
	Notebook *NotebookCapabilityConfig `mapstructure:"notebook" yaml:"notebook,omitempty"`
	Serving  *ServingCapabilityConfig  `mapstructure:"serving" yaml:"serving,omitempty"`
}

type PipelineCapabilityConfig struct {
	Label       string `mapstructure:"label" yaml:"label"`
	Concurrency int    `mapstructure:"concurrency" yaml:"concurrency"`
	OutputDir   string `mapstructure:"output_dir" yaml:"output_dir"`
	MetaDir     string `mapstructure:"meta_dir" yaml:"meta_dir"`
}

type NotebookCapabilityConfig struct {
	NotebooksRoot string `mapstructure:"notebooks_root" yaml:"notebooks_root"`
	PortRange     string `mapstructure:"port_range" yaml:"port_range"`
}

type DockerVolume struct {
	Name          string `mapstructure:"name" yaml:"name"`
	HostPath      string `mapstructure:"host_path" yaml:"host_path"`
	ContainerPath string `mapstructure:"container_path" yaml:"container_path"`
	ReadOnly      bool   `mapstructure:"read_only" yaml:"read_only"`
}

type ServingCapabilityConfig struct{}

type K8sWorkerConfig struct {
	Cluster               string                         `mapstructure:"cluster" yaml:"cluster"`
	Namespaces            []string                       `mapstructure:"namespaces" yaml:"namespaces"`
	Kubeconfig            string                         `mapstructure:"kubeconfig" yaml:"kubeconfig"`
	InCluster             bool                           `mapstructure:"in_cluster" yaml:"in_cluster"`
	ResultOutboxDir       string                         `mapstructure:"result_outbox_dir" yaml:"result_outbox_dir"`
	PipelineRunner        K8sPipelineRunnerConfig        `mapstructure:"pipeline_runner" yaml:"pipeline_runner"`
	NotebookVolumeBrowser K8sNotebookVolumeBrowserConfig `mapstructure:"notebook_volume_browser" yaml:"notebook_volume_browser"`
}

type K8sPipelineRunnerConfig struct {
	Image           string `mapstructure:"image" yaml:"image"`
	ImagePullPolicy string `mapstructure:"image_pull_policy" yaml:"image_pull_policy"`
}

type K8sNotebookVolumeBrowserConfig struct {
	Image string `mapstructure:"image" yaml:"image"`
}
