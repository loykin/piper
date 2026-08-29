package config

import "time"

type RootConfig struct {
	Version           int                     `mapstructure:"version" yaml:"version"`
	Log               LogConfig               `mapstructure:"log" yaml:"log"`
	Storage           StorageConfig           `mapstructure:"storage" yaml:"storage"`
	Stats             StatsConfig             `mapstructure:"stats" yaml:"stats"`
	Source            SourceConfig            `mapstructure:"source" yaml:"source"`
	Server            ServerConfig            `mapstructure:"server" yaml:"server"`
	Runtime           RuntimeConfig           `mapstructure:"runtime" yaml:"runtime"`
	Notebook          NotebookConfig          `mapstructure:"notebook" yaml:"notebook"`
	NotebookExecution NotebookExecutionConfig `mapstructure:"notebook_execution" yaml:"notebook_execution"`
	Integrations      IntegrationsConfig      `mapstructure:"integrations" yaml:"integrations"`
	MCP               MCPConfig               `mapstructure:"mcp" yaml:"mcp"`
	Deployment        DeploymentConfig        `mapstructure:"deployment" yaml:"deployment"`
	Home              HomeConfig              `mapstructure:"home" yaml:"home"`
}

// NotebookConfig configures the notebook direct-runtime workspace layout.
// Only meaningful when runtime.type is docker or baremetal (K8s notebooks
// use a PVC and a fixed in-pod port instead).
type NotebookConfig struct {
	NotebooksRoot string `mapstructure:"notebooks_root" yaml:"notebooks_root"`
	PortRange     string `mapstructure:"port_range" yaml:"port_range"`
}

type NotebookExecutionConfig struct {
	MCPPolicy             string        `mapstructure:"mcp_policy" yaml:"mcp_policy"`
	MaxRunningPerNotebook int           `mapstructure:"max_running_per_notebook" yaml:"max_running_per_notebook"`
	MaxKernelsPerNotebook int           `mapstructure:"max_kernels_per_notebook" yaml:"max_kernels_per_notebook"`
	MaxQueuedPerProject   int           `mapstructure:"max_queued_per_project" yaml:"max_queued_per_project"`
	KernelIdleTTL         time.Duration `mapstructure:"kernel_idle_ttl" yaml:"kernel_idle_ttl"`
	CellTimeout           time.Duration `mapstructure:"cell_timeout" yaml:"cell_timeout"`
	ExecutionTimeout      time.Duration `mapstructure:"execution_timeout" yaml:"execution_timeout"`
	InlineOutputBytes     int           `mapstructure:"inline_output_bytes" yaml:"inline_output_bytes"`
	FileReadBytes         int           `mapstructure:"file_read_bytes" yaml:"file_read_bytes"`
}

type IntegrationsConfig struct {
	MLflow MLflowConfig `mapstructure:"mlflow" yaml:"mlflow"`
}

type MLflowConfig struct {
	Enabled               bool          `mapstructure:"enabled" yaml:"enabled"`
	DispatcherConcurrency int           `mapstructure:"dispatcher_concurrency" yaml:"dispatcher_concurrency"`
	BatchSize             int           `mapstructure:"batch_size" yaml:"batch_size"`
	RequestTimeout        time.Duration `mapstructure:"request_timeout" yaml:"request_timeout"`
	MaxAttemptsBeforeDead int           `mapstructure:"max_attempts_before_dead" yaml:"max_attempts_before_dead"`
	LeaseDuration         time.Duration `mapstructure:"lease_duration" yaml:"lease_duration"`
	PollInterval          time.Duration `mapstructure:"poll_interval" yaml:"poll_interval"`
	AllowInsecureHTTP     bool          `mapstructure:"allow_insecure_http" yaml:"allow_insecure_http"`
	AllowedHosts          []string      `mapstructure:"allowed_hosts" yaml:"allowed_hosts"`
	AllowedCIDRs          []string      `mapstructure:"allowed_cidrs" yaml:"allowed_cidrs"`
}

type MCPConfig struct {
	Enabled        bool          `mapstructure:"enabled" yaml:"enabled"`
	AllowedOrigins []string      `mapstructure:"allowed_origins" yaml:"allowed_origins"`
	AllowedHosts   []string      `mapstructure:"allowed_hosts" yaml:"allowed_hosts"`
	SessionTTL     time.Duration `mapstructure:"session_ttl" yaml:"session_ttl"`
}

// DeploymentConfig selects how this installation participates in the
// Home/Member topology (fed.md §13.5). Mode "" behaves exactly like "home"
// — today's default: full UI, plus an optional in-process runtime
// (runtime.type, §13.2) acting as that Home's own Local Member.
type DeploymentConfig struct {
	Mode string `mapstructure:"mode" yaml:"mode"` // "" | "home" | "member"
	// MemberID is this installation's stable identity when mode is
	// "member". Auto-generated from hostname when empty.
	MemberID string `mapstructure:"member_id" yaml:"member_id"`
}

// HomeConfig describes the federation edge. Member mode uses ID, URL, and
// EnrollmentToken to dial Home. Home mode uses ID, TunnelAddr, Members, and
// Projects to accept Members and route each project's Run execution owner.
type HomeConfig struct {
	ID              string            `mapstructure:"id" yaml:"id"`
	URL             string            `mapstructure:"url" yaml:"url"`
	EnrollmentToken string            `mapstructure:"enrollment_token" yaml:"enrollment_token"`
	TunnelAddr      string            `mapstructure:"tunnel_addr" yaml:"tunnel_addr"`
	Members         map[string]string `mapstructure:"members" yaml:"members"`
	Projects        map[string]string `mapstructure:"projects" yaml:"projects"`
}

// RuntimeConfig selects execution owned directly by the Piper server —
// required; there is no remote execution fallback. The Namespaces through
// PipelineRunner fields are k8s-only; Docker/Baremetal carry their own
// runtime-specific fields in their own sub-structs.
type RuntimeConfig struct {
	Type           string                  `mapstructure:"type" yaml:"type"`
	Namespaces     []string                `mapstructure:"namespaces" yaml:"namespaces"`
	Kubeconfig     string                  `mapstructure:"kubeconfig" yaml:"kubeconfig"`
	InCluster      bool                    `mapstructure:"in_cluster" yaml:"in_cluster"`
	WorkloadURL    string                  `mapstructure:"workload_url" yaml:"workload_url"`
	PipelineRunner K8sPipelineRunnerConfig `mapstructure:"pipeline_runner" yaml:"pipeline_runner"`
	Docker         *RuntimeDockerConfig    `mapstructure:"docker" yaml:"docker,omitempty"`
	Baremetal      *RuntimeBaremetalConfig `mapstructure:"baremetal" yaml:"baremetal,omitempty"`
}

// RuntimeDockerConfig configures runtime.type: docker (direct, in-process
// Docker pipeline execution — no worker tunnel involved).
type RuntimeDockerConfig struct {
	Network     string `mapstructure:"network" yaml:"network"`
	Concurrency int    `mapstructure:"concurrency" yaml:"concurrency"`
	WorkloadURL string `mapstructure:"workload_url" yaml:"workload_url"`
}

// RuntimeBaremetalConfig configures runtime.type: baremetal (direct,
// in-process subprocess pipeline execution — no worker tunnel involved).
type RuntimeBaremetalConfig struct {
	MetaDir     string `mapstructure:"meta_dir" yaml:"meta_dir"`
	Concurrency int    `mapstructure:"concurrency" yaml:"concurrency"`
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

type StatsConfig struct {
	Spool   StatsSpoolConfig   `mapstructure:"spool" yaml:"spool"`
	Logs    StatsBackendConfig `mapstructure:"logs" yaml:"logs"`
	Metrics StatsBackendConfig `mapstructure:"metrics" yaml:"metrics"`
}

type StatsSpoolConfig struct {
	Dir      string `mapstructure:"dir" yaml:"dir"`
	MaxBytes int64  `mapstructure:"max_bytes" yaml:"max_bytes"`
}

type StatsBackendConfig struct {
	URL             string        `mapstructure:"url" yaml:"url"`
	CredentialRef   string        `mapstructure:"credential_ref" yaml:"credential_ref"`
	Retention       time.Duration `mapstructure:"retention" yaml:"retention"`
	ManageRetention bool          `mapstructure:"manage_retention" yaml:"manage_retention"`
}

type SourceConfig struct {
	Git GitConfig `mapstructure:"git" yaml:"git"`
}

type GitConfig struct {
	User  string `mapstructure:"user" yaml:"user"`
	Token string `mapstructure:"token" yaml:"token"`
}

type ServerConfig struct {
	HTTPAddr                 string          `mapstructure:"http_addr" yaml:"http_addr"`
	WorkloadToken            string          `mapstructure:"workload_token" yaml:"workload_token"`
	AuthSigningKey           string          `mapstructure:"auth_signing_key" yaml:"auth_signing_key"`
	AllowInsecureTrustedMode bool            `mapstructure:"allow_insecure_trusted_mode" yaml:"allow_insecure_trusted_mode"`
	SecretEncryptionKey      string          `mapstructure:"secret_encryption_key" yaml:"secret_encryption_key"`
	AllowInsecureDevKey      bool            `mapstructure:"allow_insecure_dev_key" yaml:"allow_insecure_dev_key"`
	TLS                      TLSConfig       `mapstructure:"tls" yaml:"tls"`
	DB                       DBConfig        `mapstructure:"db" yaml:"db"`
	DataDir                  string          `mapstructure:"data_dir" yaml:"data_dir"`
	Retention                RetentionConfig `mapstructure:"retention" yaml:"retention"`
	Schedule                 ScheduleConfig  `mapstructure:"schedule" yaml:"schedule"`
	Serving                  ServerServing   `mapstructure:"serving" yaml:"serving"`
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

type K8sPipelineRunnerConfig struct {
	Image           string `mapstructure:"image" yaml:"image"`
	ImagePullPolicy string `mapstructure:"image_pull_policy" yaml:"image_pull_policy"`
}
