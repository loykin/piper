package commands

import (
	"bytes"
	"fmt"
	"os"
	"time"

	piper "github.com/piper/piper"
	"github.com/piper/piper/pkg/k8s"
	"github.com/spf13/viper"
	"gopkg.in/yaml.v3"
)

// configFile mirrors the piper.yaml file structure exactly.
// Parsed with KnownFields(true) on startup to catch unknown or misspelled keys.
type configFile struct {
	Run       runSection       `yaml:"run"       mapstructure:"run"`
	Source    sourceSection    `yaml:"source"    mapstructure:"source"`
	Server    serverSection    `yaml:"server"    mapstructure:"server"`
	K8s       k8sSection       `yaml:"k8s"       mapstructure:"k8s"`
	Retention retentionSection `yaml:"retention" mapstructure:"retention"`
	Schedule  scheduleSection  `yaml:"schedule"  mapstructure:"schedule"`
	Log       logSection       `yaml:"log"       mapstructure:"log"`
}

type runSection struct {
	OutputDir   string        `yaml:"output_dir"   mapstructure:"output_dir"`
	Retries     int           `yaml:"retries"      mapstructure:"retries"`
	RetryDelay  time.Duration `yaml:"retry_delay"  mapstructure:"retry_delay"`
	Concurrency int           `yaml:"concurrency"  mapstructure:"concurrency"`
}

type sourceSection struct {
	S3  s3Section  `yaml:"s3"  mapstructure:"s3"`
	Git gitSection `yaml:"git" mapstructure:"git"`
}

type s3Section struct {
	Endpoint  string `yaml:"endpoint"   mapstructure:"endpoint"`
	AccessKey string `yaml:"access_key" mapstructure:"access_key"`
	SecretKey string `yaml:"secret_key" mapstructure:"secret_key"`
	Bucket    string `yaml:"bucket"     mapstructure:"bucket"`
	UseSSL    bool   `yaml:"use_ssl"    mapstructure:"use_ssl"`
}

type gitSection struct {
	Token string `yaml:"token" mapstructure:"token"`
	User  string `yaml:"user"  mapstructure:"user"`
}

type serverSection struct {
	Addr  string     `yaml:"addr"  mapstructure:"addr"`
	Token string     `yaml:"token" mapstructure:"token"`
	TLS   tlsSection `yaml:"tls"   mapstructure:"tls"`
}

type tlsSection struct {
	Enabled  bool   `yaml:"enabled"   mapstructure:"enabled"`
	CertFile string `yaml:"cert_file" mapstructure:"cert_file"`
	KeyFile  string `yaml:"key_file"  mapstructure:"key_file"`
}

type k8sSection struct {
	AgentImage           string `yaml:"agent_image"             mapstructure:"agent_image"`
	AgentImagePullPolicy string `yaml:"agent_image_pull_policy" mapstructure:"agent_image_pull_policy"`
	Namespace            string `yaml:"namespace"               mapstructure:"namespace"`
	InCluster            bool   `yaml:"in_cluster"              mapstructure:"in_cluster"`
	Kubeconfig           string `yaml:"kubeconfig"              mapstructure:"kubeconfig"`
	MasterURL            string `yaml:"master_url"              mapstructure:"master_url"`
	Token                string `yaml:"token"                   mapstructure:"token"`
	DefaultImage         string `yaml:"default_image"           mapstructure:"default_image"`
	TTLAfterFinished     int32  `yaml:"ttl_after_finished"      mapstructure:"ttl_after_finished"`
}

type retentionSection struct {
	RunTTL      time.Duration `yaml:"run_ttl"      mapstructure:"run_ttl"`
	ArtifactTTL time.Duration `yaml:"artifact_ttl" mapstructure:"artifact_ttl"`
}

type scheduleSection struct {
	MisfirePolicy      string        `yaml:"misfire_policy"       mapstructure:"misfire_policy"`
	MisfireGracePeriod time.Duration `yaml:"misfire_grace_period" mapstructure:"misfire_grace_period"`
}

type logSection struct {
	Format string `yaml:"format" mapstructure:"format"`
}

// StrictParseConfigFile parses the YAML file at path with KnownFields(true)
// to detect unknown or misspelled keys. Returns nil if the file does not exist.
func StrictParseConfigFile(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("read config file: %w", err)
	}
	var cf configFile
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(&cf); err != nil {
		return fmt.Errorf("config file %s: %w", path, err)
	}
	return nil
}

// toConfig converts the configFile (populated from viper) into a piper.Config.
func (c *configFile) toConfig() piper.Config {
	return piper.Config{
		OutputDir:   c.Run.OutputDir,
		MaxRetries:  c.Run.Retries,
		RetryDelay:  c.Run.RetryDelay,
		Concurrency: c.Run.Concurrency,
		Git: piper.GitConfig{
			Token: c.Source.Git.Token,
			User:  c.Source.Git.User,
		},
		S3: piper.S3Config{
			Endpoint:  c.Source.S3.Endpoint,
			AccessKey: c.Source.S3.AccessKey,
			SecretKey: c.Source.S3.SecretKey,
			Bucket:    c.Source.S3.Bucket,
			UseSSL:    c.Source.S3.UseSSL,
		},
		Server: piper.ServerConfig{
			Addr:  c.Server.Addr,
			Token: c.Server.Token,
			TLS: piper.TLSConfig{
				Enabled:  c.Server.TLS.Enabled,
				CertFile: c.Server.TLS.CertFile,
				KeyFile:  c.Server.TLS.KeyFile,
			},
		},
		Retention: piper.RetentionConfig{
			RunTTL:      c.Retention.RunTTL,
			ArtifactTTL: c.Retention.ArtifactTTL,
		},
		Schedule: piper.ScheduleConfig{
			MisfirePolicy:      c.Schedule.MisfirePolicy,
			MisfireGracePeriod: c.Schedule.MisfireGracePeriod,
		},
		K8s: piper.K8sConfig{
			AgentImage:           c.K8s.AgentImage,
			AgentImagePullPolicy: c.K8s.AgentImagePullPolicy,
			Namespace:            c.K8s.Namespace,
			InCluster:            c.K8s.InCluster,
			Kubeconfig:           c.K8s.Kubeconfig,
			MasterURL:            c.K8s.MasterURL,
			Token:                c.K8s.Token,
			DefaultImage:         c.K8s.DefaultImage,
			TTLAfterFinished:     c.K8s.TTLAfterFinished,
		},
	}
}

// buildConfig loads viper state into a configFile, converts it to piper.Config,
// and validates the result.
func buildConfig() (piper.Config, error) {
	var cf configFile
	if err := viper.Unmarshal(&cf); err != nil {
		return piper.Config{}, fmt.Errorf("config: %w", err)
	}
	cfg := cf.toConfig()
	if err := cfg.Validate(); err != nil {
		return piper.Config{}, err
	}
	return cfg, nil
}

// NewPiper creates a Piper instance from the current viper state and wires up
// the K8s launcher if k8s.agent_image is configured.
// Exported so that library users can pass it to Commands() as the factory.
func NewPiper() (*piper.Piper, error) {
	cfg, err := buildConfig()
	if err != nil {
		return nil, err
	}
	p, err := piper.New(cfg)
	if err != nil {
		return nil, err
	}
	if cfg.K8s.AgentImage != "" {
		ttl := cfg.K8s.TTLAfterFinished
		var ttlPtr *int32
		if ttl > 0 {
			ttlPtr = &ttl
		}
		launcher, err := k8s.New(k8s.Config{
			AgentImage:           cfg.K8s.AgentImage,
			AgentImagePullPolicy: cfg.K8s.AgentImagePullPolicy,
			Namespace:            cfg.K8s.Namespace,
			InCluster:            cfg.K8s.InCluster,
			Kubeconfig:           cfg.K8s.Kubeconfig,
			MasterURL:            cfg.K8s.MasterURL,
			Token:                cfg.K8s.Token,
			DefaultImage:         cfg.K8s.DefaultImage,
			S3Endpoint:           cfg.S3.Endpoint,
			S3AccessKey:          cfg.S3.AccessKey,
			S3SecretKey:          cfg.S3.SecretKey,
			S3Bucket:             cfg.S3.Bucket,
			S3UseSSL:             cfg.S3.UseSSL,
			TTLAfterFinished:     ttlPtr,
		})
		if err != nil {
			_ = p.Close()
			return nil, fmt.Errorf("k8s launcher: %w", err)
		}
		p.SetBackend(launcher)
	}
	return p, nil
}
