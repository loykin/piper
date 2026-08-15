package piper

import (
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/loykin/dbstore"
	storemod "github.com/loykin/piper/internal/store"
	"github.com/loykin/piper/pkg/security"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

const (
	RuntimeK8s       = "k8s"
	RuntimeDocker    = "docker"
	RuntimeBaremetal = "baremetal"
)

// Config is the global piper configuration. Accepts a struct and can be embedded.
type Config struct {
	OutputDir string `yaml:"output_dir"   mapstructure:"output_dir"`
	// DB configuration — specify only one. Priority: Repos > DBDriver+DBDSN > DBPath.
	DBPath string `yaml:"db_path"   mapstructure:"db_path"` // sqlite file path (default: output_dir/piper.db)
	// DBDriver selects the database driver: "sqlite" (default) or "postgres".
	DBDriver string `yaml:"db_driver" mapstructure:"db_driver"`
	// DBDSN is the connection string for non-SQLite databases.
	// For PostgreSQL: "host=... port=5432 dbname=... user=... password=... sslmode=disable"
	DBDSN string `yaml:"db_dsn" mapstructure:"db_dsn"`
	// Repos is a fully-constructed store injected by the caller.
	// When set, all other DB fields are ignored and piper skips migrations.
	// Use piper.NewExternalRepos() to build one from your own repository implementations.
	Repos *storemod.Repos `yaml:"-" mapstructure:"-"`

	// Auth composes authentication and authorization capabilities.
	// Trusted mode must be enabled explicitly.
	Auth AuthConfig `yaml:"-" mapstructure:"-"`

	// Hooks — all extension points. nil means no-op.
	Hooks Hooks `yaml:"-" mapstructure:"-"`

	// Git source
	Git GitConfig `yaml:"git" mapstructure:"git"`

	// Storage selects the artifact store backend.
	// When empty, falls back to the built-in file server.
	Storage StorageConfig `yaml:"storage" mapstructure:"storage"`

	// Server (not required in embedded mode)
	Server ServerConfig `yaml:"server" mapstructure:"server"`

	// Retention controls automatic cleanup. Zero values disable cleanup.
	Retention RetentionConfig `yaml:"retention" mapstructure:"retention"`

	// Queue controls the master's run/step retry and restart-recovery policy.
	Queue QueueConfig `yaml:"queue" mapstructure:"queue"`

	// Schedule controls cron/once scheduling behavior.
	Schedule ScheduleConfig `yaml:"schedule" mapstructure:"schedule"`

	// Serving — model serving configuration.
	Serving ServingConfig `yaml:"serving" mapstructure:"serving"`

	// Notebook configures direct-runtime workspace and port allocation.
	Notebook NotebookRuntimeConfig `yaml:"notebook" mapstructure:"notebook"`

	// Runtime selects the required in-process execution backend.
	Runtime RuntimeConfig `yaml:"runtime" mapstructure:"runtime"`
}

type RuntimeConfig struct {
	Type      string                 `yaml:"type" mapstructure:"type"`
	K8s       K8sRuntimeConfig       `yaml:"k8s" mapstructure:"k8s"`
	Docker    DockerRuntimeConfig    `yaml:"docker" mapstructure:"docker"`
	Baremetal BaremetalRuntimeConfig `yaml:"baremetal" mapstructure:"baremetal"`
}

type K8sRuntimeConfig struct {
	Client kubernetes.Interface `yaml:"-" mapstructure:"-"`
	// RestConfig is required to exec into notebook pods for workspace file
	// access (notebook.WorkspaceReader) — kubernetes.Interface alone can't
	// build the SPDY executor that needs.
	RestConfig          *rest.Config `yaml:"-" mapstructure:"-"`
	Namespaces          []string     `yaml:"namespaces" mapstructure:"namespaces"`
	PipelineRunnerImage string       `yaml:"pipeline_runner_image" mapstructure:"pipeline_runner_image"`
	ImagePullPolicy     string       `yaml:"image_pull_policy" mapstructure:"image_pull_policy"`
	TTLAfterFinished    *int32       `yaml:"ttl_after_finished" mapstructure:"ttl_after_finished"`
	// WorkloadURL is the URL Kubernetes workloads use to reach Piper's built-in
	// artifact endpoint when storage resolves to file://.
	WorkloadURL string `yaml:"workload_url" mapstructure:"workload_url"`
}

// DockerRuntimeConfig configures direct, in-process Docker pipeline
// execution (runtime.type: docker). Unlike K8s (bounded by the cluster
// scheduler), containers run directly on the Piper host, so Concurrency is
// required.
type DockerRuntimeConfig struct {
	Network     string `yaml:"network" mapstructure:"network"`
	Concurrency int    `yaml:"concurrency" mapstructure:"concurrency"`
	// WorkloadURL is the URL Docker containers use to reach Piper's built-in
	// artifact endpoint when storage resolves to file:// — a container
	// cannot reach the host's local filesystem directly, the same boundary
	// K8s.WorkloadURL exists for.
	WorkloadURL string `yaml:"workload_url" mapstructure:"workload_url"`
}

// BaremetalRuntimeConfig configures direct, in-process baremetal (subprocess)
// pipeline execution (runtime.type: baremetal). Like Docker, subprocesses run
// directly on the Piper host, so Concurrency is required. Unlike Docker/K8s,
// baremetal shares the host filesystem directly, so it has no WorkloadURL —
// file:// artifact storage is used as-is.
type BaremetalRuntimeConfig struct {
	MetaDir     string `yaml:"meta_dir" mapstructure:"meta_dir"`
	Concurrency int    `yaml:"concurrency" mapstructure:"concurrency"`
}

type GitConfig struct {
	User  string `yaml:"user"  mapstructure:"user"`
	Token string `yaml:"token" mapstructure:"token"`
}

// StorageConfig holds artifact store configuration.
type StorageConfig struct {
	// URL selects the storage backend.
	// Supported schemes: s3://, gs://, azblob://, file://, http://, https://
	// When empty, falls back to the built-in file server.
	URL string `yaml:"url" mapstructure:"url" json:"url"`

	// Disabled turns off the artifact store entirely.
	// When true, Piper runs without blobstore-backed artifact storage.
	Disabled bool `yaml:"disabled" mapstructure:"disabled" json:"disabled"`

	// Token is an optional Bearer token for HTTP-based stores.
	Token string `yaml:"token" mapstructure:"token" json:"token"`

	// CredentialRef names a system-scoped s3 credential that supplies the
	// access key material for an s3:// URL. The URL itself carries only the
	// non-secret bucket/endpoint/region; the credential injects
	// accessKey/secretKey/sessionToken at startup.
	CredentialRef string `yaml:"credentialRef" mapstructure:"credentialRef" json:"credentialRef"`
}

// LoginRouteProvider registers the login/session endpoints for an auth scheme.
// OIDC and host-application integrations can provide their own routes.
type LoginRouteProvider interface {
	RegisterPublicRoutes(rg *gin.RouterGroup)
	RegisterAuthenticatedRoutes(rg *gin.RouterGroup)
	LoginMode() string
	LoginURL() string
}

// AuthConfig composes independent authentication and identity capabilities.
type AuthConfig struct {
	// Trusted explicitly enables no-auth mode. It cannot be combined with
	// authentication or authorization capabilities.
	Trusted bool

	LoginRoutes          LoginRouteProvider
	Authenticator        security.Authenticator
	Authorizer           security.Authorizer
	UserDirectory        security.UserDirectory
	UserManager          security.UserManager
	ProjectMemberManager security.ProjectMemberManager

	// Factory creates capabilities after Piper has opened its repositories.
	Factory AuthFactory
}

type AuthDependencies struct {
	DB            *sql.DB
	Driver        string
	SecureCookies bool
	Executor      *dbstore.Executor
}

type AuthFactory func(AuthDependencies) (AuthConfig, error)

type ServerConfig struct {
	Addr                string    `yaml:"addr"                   mapstructure:"addr"`
	WorkloadToken       string    `yaml:"workload_token"         mapstructure:"workload_token"` // guards the built-in /store endpoint for Docker/K8s workload access
	SecretEncryptionKey string    `yaml:"secret_encryption_key"  mapstructure:"secret_encryption_key"`
	AllowInsecureDevKey bool      `yaml:"allow_insecure_dev_key" mapstructure:"allow_insecure_dev_key"`
	TLS                 TLSConfig `yaml:"tls"                    mapstructure:"tls"`
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

// QueueConfig controls the master's run/step state machine: retry policy and
// the grace period a "running" step gets after a server restart before it's
// treated as failed/retried instead of being re-dispatched immediately.
type QueueConfig struct {
	// MaxAttempts is the total attempts per step, including the first try.
	// Zero/unset means 1 (no automatic retry).
	MaxAttempts int `yaml:"max_attempts"   mapstructure:"max_attempts"`
	// RetryDelay is the delay before a retried step becomes ready again.
	RetryDelay time.Duration `yaml:"retry_delay"    mapstructure:"retry_delay"`
	// RecoveryGrace is how long a step that was "running" when the server
	// crashed waits for the owned runtime to recover it before being failed or
	// retried. Zero/unset means a built-in default.
	RecoveryGrace time.Duration `yaml:"recovery_grace" mapstructure:"recovery_grace"`
}

type ScheduleConfig struct {
	// MisfirePolicy controls cron schedules that are overdue when the scheduler wakes up.
	// Supported values: "skip" (default), "run_once".
	MisfirePolicy string `yaml:"misfire_policy" mapstructure:"misfire_policy"`
	// MisfireGracePeriod is the delay tolerated before a due cron run is considered missed.
	MisfireGracePeriod time.Duration `yaml:"misfire_grace_period" mapstructure:"misfire_grace_period"`
}

// ServingConfig holds configuration for model serving (ModelService).
type ServingConfig struct {
	// ModelDir is the local directory where model artifacts are downloaded before serving.
	// Defaults to output_dir/models.
	ModelDir string `yaml:"model_dir" mapstructure:"model_dir"`
}

// NotebookRuntimeConfig holds paths for direct-runtime notebooks.
type NotebookRuntimeConfig struct {
	// NotebooksRoot is the base directory under which per-notebook work directories are created.
	// Each notebook runs in {notebooks_root}/{name}. Defaults to "./notebooks".
	NotebooksRoot string `yaml:"notebooks_root" mapstructure:"notebooks_root"`

	// PortRange is the inclusive range from which jupyter ports are auto-allocated.
	// Format: "START-END", e.g. "8888-9900". Defaults to "8888-9900".
	PortRange string `yaml:"port_range" mapstructure:"port_range"`
}

func DefaultConfig() Config {
	return Config{
		OutputDir: "./piper-outputs",
		Auth: AuthConfig{
			Trusted: true,
		},
		Server: ServerConfig{
			Addr: ":8080",
		},
		Schedule: ScheduleConfig{
			MisfirePolicy:      "run_once",
			MisfireGracePeriod: 5 * time.Minute,
		},
	}
}

func (c Config) Validate() error {
	hasCapabilities := c.Auth.LoginRoutes != nil ||
		c.Auth.Authenticator != nil ||
		c.Auth.Authorizer != nil ||
		c.Auth.UserDirectory != nil ||
		c.Auth.UserManager != nil ||
		c.Auth.ProjectMemberManager != nil
	if c.Auth.Factory != nil && hasCapabilities {
		return fmt.Errorf("auth capabilities and factory are mutually exclusive")
	}
	if c.Auth.Trusted && (c.Auth.Factory != nil || hasCapabilities) {
		return fmt.Errorf("trusted auth mode cannot be combined with auth capabilities")
	}
	if !c.Auth.Trusted && c.Auth.Factory == nil {
		if c.Auth.Authenticator == nil {
			return fmt.Errorf("auth authenticator is required outside trusted mode")
		}
		if c.Auth.Authorizer == nil {
			return fmt.Errorf("auth authorizer is required outside trusted mode")
		}
	}
	if c.Auth.UserManager != nil && c.Auth.UserDirectory == nil {
		return fmt.Errorf("auth user directory is required when user manager is configured")
	}
	if c.Server.TLS.Enabled {
		if c.Server.TLS.CertFile == "" || c.Server.TLS.KeyFile == "" {
			return fmt.Errorf("server.tls enabled but cert_file or key_file is not set")
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
	switch c.Runtime.Type {
	case "":
	case RuntimeK8s:
		if c.Runtime.K8s.Client == nil {
			return fmt.Errorf("runtime.k8s client is required")
		}
		if len(c.Runtime.K8s.Namespaces) == 0 {
			return fmt.Errorf("runtime.k8s.namespaces must contain at least one allowed namespace")
		}
		seen := make(map[string]struct{}, len(c.Runtime.K8s.Namespaces))
		for _, namespace := range c.Runtime.K8s.Namespaces {
			if strings.TrimSpace(namespace) == "" {
				return fmt.Errorf("runtime.k8s.namespaces must not contain an empty namespace")
			}
			if _, exists := seen[namespace]; exists {
				return fmt.Errorf("runtime.k8s.namespaces contains duplicate namespace %q", namespace)
			}
			seen[namespace] = struct{}{}
		}
		switch c.Runtime.K8s.ImagePullPolicy {
		case "", "Always", "IfNotPresent", "Never":
		default:
			return fmt.Errorf("runtime.k8s.image_pull_policy must be Always, IfNotPresent, or Never")
		}
		if strings.HasPrefix(resolveStorageURL(c), "file://") && strings.TrimSpace(c.Runtime.K8s.WorkloadURL) == "" {
			return fmt.Errorf("runtime.k8s.workload_url is required when using the built-in file artifact store")
		}
	case RuntimeDocker:
		if c.Runtime.Docker.Concurrency < 0 {
			return fmt.Errorf("runtime.docker.concurrency must not be negative")
		}
		if strings.HasPrefix(resolveStorageURL(c), "file://") && strings.TrimSpace(c.Runtime.Docker.WorkloadURL) == "" {
			return fmt.Errorf("runtime.docker.workload_url is required when using the built-in file artifact store")
		}
	case RuntimeBaremetal:
		if c.Runtime.Baremetal.Concurrency < 0 {
			return fmt.Errorf("runtime.baremetal.concurrency must not be negative")
		}
	default:
		return fmt.Errorf("runtime.type must be k8s, docker, baremetal, or empty")
	}

	return nil
}
