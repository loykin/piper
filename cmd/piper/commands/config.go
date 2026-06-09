package commands

import (
	"bytes"
	"fmt"
	"os"
	"time"

	piper "github.com/piper/piper"
	"github.com/spf13/viper"
	"gopkg.in/yaml.v3"
	corev1 "k8s.io/api/core/v1"
	sigsyaml "sigs.k8s.io/yaml"
)

// configFile mirrors the piper.yaml file structure exactly.
// Parsed with KnownFields(true) on startup to catch unknown or misspelled keys.
type configFile struct {
	Run            runSection            `yaml:"run"             mapstructure:"run"`
	Source         sourceSection         `yaml:"source"          mapstructure:"source"`
	Storage        storageSection        `yaml:"storage"         mapstructure:"storage"`
	Server         serverSection         `yaml:"server"          mapstructure:"server"`
	Pipeline       pipelineSection       `yaml:"pipeline"        mapstructure:"pipeline"`
	K8s            k8sSection            `yaml:"k8s"             mapstructure:"k8s"`
	Retention      retentionSection      `yaml:"retention"       mapstructure:"retention"`
	Schedule       scheduleSection       `yaml:"schedule"        mapstructure:"schedule"`
	Serving        servingSection        `yaml:"serving"         mapstructure:"serving"`
	Log            logSection            `yaml:"log"             mapstructure:"log"`
	DB             dbSection             `yaml:"db"              mapstructure:"db"`
	NotebookWorker notebookWorkerSection `yaml:"notebook_worker" mapstructure:"notebook_worker"`
	NotebookK8s    notebookK8sSection    `yaml:"notebook_k8s"    mapstructure:"notebook_k8s"`
}

type pipelineSection struct {
	DispatchMode string `yaml:"dispatch_mode" mapstructure:"dispatch_mode"`
}

type storageSection struct {
	URL      string `yaml:"url"      mapstructure:"url"`
	Disabled bool   `yaml:"disabled" mapstructure:"disabled"`
	Token    string `yaml:"token"    mapstructure:"token"`
}

type runSection struct {
	OutputDir   string        `yaml:"output_dir"   mapstructure:"output_dir"`
	Retries     *int          `yaml:"retries"      mapstructure:"retries"`
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
	Worker bool `yaml:"worker" mapstructure:"worker"`
}

type retentionSection struct {
	RunTTL      time.Duration `yaml:"run_ttl"      mapstructure:"run_ttl"`
	ArtifactTTL time.Duration `yaml:"artifact_ttl" mapstructure:"artifact_ttl"`
}

type scheduleSection struct {
	MisfirePolicy      string        `yaml:"misfire_policy"       mapstructure:"misfire_policy"`
	MisfireGracePeriod time.Duration `yaml:"misfire_grace_period" mapstructure:"misfire_grace_period"`
}

type servingSection struct {
	ModelDir string `yaml:"model_dir" mapstructure:"model_dir"`
	Worker   bool   `yaml:"worker"    mapstructure:"worker"`
}

type logSection struct {
	Format string `yaml:"format" mapstructure:"format"`
}

type dbSection struct {
	Driver string `yaml:"driver" mapstructure:"driver"`
	DSN    string `yaml:"dsn"    mapstructure:"dsn"`
}

type notebookWorkerSection struct {
	NotebooksRoot string                      `yaml:"notebooks_root" mapstructure:"notebooks_root"`
	PortRange     string                      `yaml:"port_range"     mapstructure:"port_range"`
	Mode          string                      `yaml:"mode"           mapstructure:"mode"`
	Docker        notebookWorkerDockerSection `yaml:"docker" mapstructure:"docker"`
}

type notebookWorkerDockerSection struct {
	Image        string                       `yaml:"image"          mapstructure:"image"`
	Network      string                       `yaml:"network"        mapstructure:"network"`
	CPUs         string                       `yaml:"cpus"           mapstructure:"cpus"`
	Memory       string                       `yaml:"memory"         mapstructure:"memory"`
	ShmSize      string                       `yaml:"shm_size"       mapstructure:"shm_size"`
	ReadOnlyRoot bool                         `yaml:"read_only_root" mapstructure:"read_only_root"`
	Tmpfs        []string                     `yaml:"tmpfs"          mapstructure:"tmpfs"`
	User         string                       `yaml:"user"           mapstructure:"user"`
	Volumes      []notebookWorkerDockerVolume `yaml:"volumes"        mapstructure:"volumes"`
	ExtraArgs    []string                     `yaml:"extra_args"     mapstructure:"extra_args"`
}

type notebookWorkerDockerVolume struct {
	Name          string `yaml:"name"           mapstructure:"name"`
	HostPath      string `yaml:"host_path"      mapstructure:"host_path"`
	ContainerPath string `yaml:"container_path" mapstructure:"container_path"`
	ReadOnly      bool   `yaml:"read_only"      mapstructure:"read_only"`
}

type notebookK8sSection struct {
	Worker       bool   `yaml:"worker"        mapstructure:"worker"`
	Namespace    string `yaml:"namespace"     mapstructure:"namespace"`
	WorkerImage  string `yaml:"worker_image"  mapstructure:"worker_image"`
	StorageClass string `yaml:"storage_class" mapstructure:"storage_class"`
	StorageSize  string `yaml:"storage_size"  mapstructure:"storage_size"`
	// PodDefaults is parsed as raw map to allow native k8s JSON/YAML field names.
	// Conversion to corev1.PodTemplateSpec happens in toConfig() via sigs.k8s.io/yaml.
	PodDefaults map[string]interface{} `yaml:"pod_defaults"  mapstructure:"pod_defaults"`
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
	maxRetries := -1 // negative → piper.New applies default
	if c.Run.Retries != nil {
		maxRetries = *c.Run.Retries
	}
	return piper.Config{
		OutputDir:   c.Run.OutputDir,
		MaxRetries:  maxRetries,
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
		Storage: piper.StorageConfig{
			URL:      c.Storage.URL,
			Disabled: c.Storage.Disabled,
			Token:    c.Storage.Token,
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
		Serving: piper.ServingConfig{
			ModelDir: c.Serving.ModelDir,
			Worker:   c.Serving.Worker,
		},
		Pipeline: piper.PipelineConfig{
			DispatchMode: c.Pipeline.DispatchMode,
		},
		K8s: piper.K8sConfig{
			Worker: c.K8s.Worker,
		},
		DBDriver: c.DB.Driver,
		DBDSN:    c.DB.DSN,
		NotebookWorker: piper.NotebookWorkerConfig{
			NotebooksRoot: c.NotebookWorker.NotebooksRoot,
			PortRange:     c.NotebookWorker.PortRange,
			Mode:          c.NotebookWorker.Mode,
			Docker: piper.NotebookWorkerDockerConfig{
				Image:        c.NotebookWorker.Docker.Image,
				Network:      c.NotebookWorker.Docker.Network,
				CPUs:         c.NotebookWorker.Docker.CPUs,
				Memory:       c.NotebookWorker.Docker.Memory,
				ShmSize:      c.NotebookWorker.Docker.ShmSize,
				ReadOnlyRoot: c.NotebookWorker.Docker.ReadOnlyRoot,
				Tmpfs:        c.NotebookWorker.Docker.Tmpfs,
				User:         c.NotebookWorker.Docker.User,
				Volumes:      notebookWorkerDockerVolumes(c.NotebookWorker.Docker.Volumes),
				ExtraArgs:    c.NotebookWorker.Docker.ExtraArgs,
			},
		},
		NotebookK8s: piper.NotebookK8sConfig{
			Worker:       c.NotebookK8s.Worker,
			Namespace:    c.NotebookK8s.Namespace,
			WorkerImage:  c.NotebookK8s.WorkerImage,
			StorageClass: c.NotebookK8s.StorageClass,
			StorageSize:  c.NotebookK8s.StorageSize,
			PodDefaults:  parsePodDefaults(c.NotebookK8s.PodDefaults),
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

// NewPiper creates a Piper instance from the current viper state.
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
	return p, nil
}

// resolveStorageURLFromViper derives the storage URL for workers/embedded workers.
// Priority: storage.url > source.s3.* (backward compat with pre-blobstore configs).
func resolveStorageURLFromViper() string {
	if viper.GetBool("storage.disabled") {
		return ""
	}
	if u := viper.GetString("storage.url"); u != "" {
		return u
	}
	bucket := viper.GetString("source.s3.bucket")
	if bucket == "" {
		return ""
	}
	scheme := "http"
	if viper.GetBool("source.s3.use_ssl") {
		scheme = "https"
	}
	q := "s3ForcePathStyle=true"
	if ak := viper.GetString("source.s3.access_key"); ak != "" {
		q += "&accessKey=" + ak
	}
	if sk := viper.GetString("source.s3.secret_key"); sk != "" {
		q += "&secretKey=" + sk
	}
	if ep := viper.GetString("source.s3.endpoint"); ep != "" {
		q += "&endpoint=" + scheme + "://" + ep
	}
	return "s3://" + bucket + "?" + q
}

// parsePodDefaults converts the raw map (from yaml.v3 or viper) to corev1.PodTemplateSpec
// by round-tripping through JSON using sigs.k8s.io/yaml, which honours k8s json struct tags.
func parsePodDefaults(raw map[string]interface{}) corev1.PodTemplateSpec {
	if len(raw) == 0 {
		return corev1.PodTemplateSpec{}
	}
	data, err := sigsyaml.Marshal(raw)
	if err != nil {
		return corev1.PodTemplateSpec{}
	}
	var pt corev1.PodTemplateSpec
	if err := sigsyaml.Unmarshal(data, &pt); err != nil {
		return corev1.PodTemplateSpec{}
	}
	return pt
}

func notebookWorkerDockerVolumes(items []notebookWorkerDockerVolume) []piper.NotebookWorkerDockerVolume {
	if len(items) == 0 {
		return nil
	}
	out := make([]piper.NotebookWorkerDockerVolume, len(items))
	for i, item := range items {
		out[i] = piper.NotebookWorkerDockerVolume{
			Name:          item.Name,
			HostPath:      item.HostPath,
			ContainerPath: item.ContainerPath,
			ReadOnly:      item.ReadOnly,
		}
	}
	return out
}
