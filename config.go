package piper

import (
	"database/sql"
	"fmt"
	"time"

	"github.com/gin-gonic/gin"
	storemod "github.com/piper/piper/internal/store"
	"github.com/piper/piper/pkg/security"
)

// Config is the global piper configuration. Accepts a struct and can be embedded.
type Config struct {
	OutputDir string `yaml:"output_dir"   mapstructure:"output_dir"`
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

	// Serving — model serving configuration.
	Serving ServingConfig `yaml:"serving" mapstructure:"serving"`

	// NotebookWorker — embedded bare-metal notebook worker configuration.
	NotebookWorker NotebookWorkerConfig `yaml:"notebook_worker" mapstructure:"notebook_worker"`
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
}

type AuthFactory func(AuthDependencies) (AuthConfig, error)

type ServerConfig struct {
	Addr        string    `yaml:"addr"         mapstructure:"addr"`
	WorkerToken string    `yaml:"worker_token" mapstructure:"worker_token"` // separate token for worker/agent auth
	TLS         TLSConfig `yaml:"tls"          mapstructure:"tls"`
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

// ServingConfig holds configuration for model serving (ModelService).
type ServingConfig struct {
	// ModelDir is the local directory where model artifacts are downloaded before serving.
	// Defaults to output_dir/models.
	ModelDir string `yaml:"model_dir" mapstructure:"model_dir"`
}

// NotebookWorkerConfig holds paths for the embedded bare-metal notebook worker.
type NotebookWorkerConfig struct {
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
			MisfirePolicy:      "skip",
			MisfireGracePeriod: time.Minute,
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

	return nil
}
