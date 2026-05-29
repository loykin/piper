package commands

import (
	"fmt"

	piper "github.com/piper/piper"
	"github.com/piper/piper/pkg/k8s"
	"github.com/spf13/viper"
)

// buildConfig reads the fully-loaded viper state into a piper.Config.
// Must be called from RunE (after cobra has parsed flags and initConfig has run).
func buildConfig() piper.Config {
	return piper.Config{
		OutputDir:   viper.GetString("run.output_dir"),
		MaxRetries:  viper.GetInt("run.retries"),
		RetryDelay:  viper.GetDuration("run.retry_delay"),
		Concurrency: viper.GetInt("run.concurrency"),
		Git: piper.GitConfig{
			Token: viper.GetString("source.git.token"),
			User:  viper.GetString("source.git.user"),
		},
		S3: piper.S3Config{
			Endpoint:  viper.GetString("source.s3.endpoint"),
			AccessKey: viper.GetString("source.s3.access_key"),
			SecretKey: viper.GetString("source.s3.secret_key"),
			Bucket:    viper.GetString("source.s3.bucket"),
			UseSSL:    viper.GetBool("source.s3.use_ssl"),
		},
		Server: piper.ServerConfig{
			Addr:  viper.GetString("server.addr"),
			Token: viper.GetString("server.token"),
			TLS: piper.TLSConfig{
				Enabled:  viper.GetBool("server.tls.enabled"),
				CertFile: viper.GetString("server.tls.cert_file"),
				KeyFile:  viper.GetString("server.tls.key_file"),
			},
		},
		Retention: piper.RetentionConfig{
			RunTTL:      viper.GetDuration("retention.run_ttl"),
			ArtifactTTL: viper.GetDuration("retention.artifact_ttl"),
		},
		Schedule: piper.ScheduleConfig{
			MisfirePolicy:      viper.GetString("schedule.misfire_policy"),
			MisfireGracePeriod: viper.GetDuration("schedule.misfire_grace_period"),
		},
		K8s: piper.K8sConfig{
			AgentImage:           viper.GetString("k8s.agent_image"),
			AgentImagePullPolicy: viper.GetString("k8s.agent_image_pull_policy"),
			Namespace:            viper.GetString("k8s.namespace"),
			InCluster:            viper.GetBool("k8s.in_cluster"),
			Kubeconfig:           viper.GetString("k8s.kubeconfig"),
			MasterURL:            viper.GetString("k8s.master_url"),
			Token:                viper.GetString("k8s.token"),
			DefaultImage:         viper.GetString("k8s.default_image"),
			TTLAfterFinished:     int32(viper.GetInt("k8s.ttl_after_finished")),
		},
	}
}

// NewPiper creates a Piper instance from the current viper state and wires up
// the K8s launcher if k8s.agent_image is configured.
// Exported so that library users can pass it to Commands() as the factory.
func NewPiper() (*piper.Piper, error) {
	cfg := buildConfig()
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
