package commands

import (
	"fmt"

	"github.com/jmoiron/sqlx"
	piper "github.com/piper/piper"
	cliconfig "github.com/piper/piper/cmd/piper/config"
	"github.com/piper/piper/internal/store/postgres"
	sqlitestore "github.com/piper/piper/internal/store/sqlite"
	"github.com/piper/piper/pkg/auth"
	"github.com/piper/piper/pkg/security"
	corev1 "k8s.io/api/core/v1"
	sigsyaml "sigs.k8s.io/yaml"
)

// NewPiper builds the library-facing server config from the canonical CLI config.
func NewPiper(loader *cliconfig.Loader) (*piper.Piper, error) {
	root, err := loader.Load()
	if err != nil {
		return nil, err
	}
	if err := cliconfig.ValidateServer(root); err != nil {
		return nil, err
	}
	serverPodDefaults, err := parsePodDefaults(root.Server.Notebook.K8s.PodDefaults)
	if err != nil {
		return nil, fmt.Errorf("config: server.notebook.k8s.pod_defaults: %w", err)
	}

	cfg := piper.Config{
		OutputDir: root.Server.Run.OutputDir, MaxRetries: root.Server.Run.Retries,
		RetryDelay: root.Server.Run.RetryDelay, Concurrency: root.Server.Run.Concurrency,
		Git:     piper.GitConfig{User: root.Source.Git.User, Token: root.Source.Git.Token},
		Storage: piper.StorageConfig{URL: root.Storage.URL, Disabled: root.Storage.Disabled, Token: root.Storage.Token},
		Server: piper.ServerConfig{Addr: root.Server.HTTPAddr, AgentAddr: root.Server.AgentAddr, WorkerToken: root.Server.WorkerToken,
			TLS: piper.TLSConfig{Enabled: root.Server.TLS.Enabled, CertFile: root.Server.TLS.CertFile, KeyFile: root.Server.TLS.KeyFile}},
		Retention: piper.RetentionConfig{RunTTL: root.Server.Retention.RunTTL, ArtifactTTL: root.Server.Retention.ArtifactTTL},
		Schedule:  piper.ScheduleConfig{MisfirePolicy: root.Server.Schedule.MisfirePolicy, MisfireGracePeriod: root.Server.Schedule.MisfireGracePeriod},
		Serving:   piper.ServingConfig{ModelDir: root.Server.Serving.ModelDir, Worker: root.Server.Serving.Delegate},
		DBDriver:  root.Server.DB.Driver, DBDSN: root.Server.DB.DSN, DBPath: root.Server.DB.Path,
		NotebookWorker: piper.NotebookWorkerConfig{
			NotebooksRoot: root.Workers.Notebook.NotebooksRoot, PortRange: root.Workers.Notebook.PortRange, Mode: root.Workers.Notebook.Mode,
			Docker: piper.NotebookWorkerDockerConfig{Image: root.Workers.Notebook.Docker.Image, Network: root.Workers.Notebook.Docker.Network, CPUs: root.Workers.Notebook.Docker.CPUs, Memory: root.Workers.Notebook.Docker.Memory, ShmSize: root.Workers.Notebook.Docker.ShmSize, ReadOnlyRoot: root.Workers.Notebook.Docker.ReadOnlyRoot, Tmpfs: root.Workers.Notebook.Docker.Tmpfs, User: root.Workers.Notebook.Docker.User, ExtraArgs: root.Workers.Notebook.Docker.ExtraArgs},
		},
		NotebookK8s: piper.NotebookK8sConfig{Worker: root.Server.Notebook.Delegate, Namespace: root.Server.Notebook.K8s.Namespace, WorkerImage: root.Server.Notebook.K8s.Image, StorageClass: root.Server.Notebook.K8s.StorageClass, StorageSize: root.Server.Notebook.K8s.StorageSize, PodDefaults: serverPodDefaults},
	}
	for _, v := range root.Workers.Notebook.Docker.Volumes {
		cfg.NotebookWorker.Docker.Volumes = append(cfg.NotebookWorker.Docker.Volumes, piper.NotebookWorkerDockerVolume{Name: v.Name, HostPath: v.HostPath, ContainerPath: v.ContainerPath, ReadOnly: v.ReadOnly})
	}

	signingKey := root.Server.AuthSigningKey
	if signingKey == "" {
		cfg.Auth = piper.AuthConfig{Trusted: true}
	} else {
		cfg.Auth = piper.AuthConfig{Factory: func(deps piper.AuthDependencies) (piper.AuthConfig, error) {
			if deps.DB == nil {
				return piper.AuthConfig{}, fmt.Errorf("server.auth_signing_key requires a database")
			}
			db := sqlx.NewDb(deps.DB, deps.Driver)
			var users auth.UserRepository
			var members security.ProjectMemberRepository
			var sessions auth.SessionRepository
			if deps.Driver == "postgres" {
				users, members, sessions = postgres.NewUserRepo(db), postgres.NewMemberRepo(db), postgres.NewSessionRepo(db)
			} else {
				users, members, sessions = sqlitestore.NewUserRepo(db), sqlitestore.NewMemberRepo(db), sqlitestore.NewSessionRepo(db)
			}
			provider := auth.New(auth.Config{SigningKey: []byte(signingKey)}, users, members, sessions)
			return piper.AuthConfig{LoginRoutes: auth.NewHandler(provider, provider, deps.SecureCookies), Authenticator: provider, Authorizer: provider, UserDirectory: provider, UserManager: provider, ProjectMemberManager: provider}, nil
		}}
	}
	return piper.New(cfg)
}

func parsePodDefaults(raw map[string]any) (corev1.PodTemplateSpec, error) {
	if len(raw) == 0 {
		return corev1.PodTemplateSpec{}, nil
	}
	data, err := sigsyaml.Marshal(raw)
	if err != nil {
		return corev1.PodTemplateSpec{}, err
	}
	var out corev1.PodTemplateSpec
	if err := sigsyaml.UnmarshalStrict(data, &out); err != nil {
		return corev1.PodTemplateSpec{}, err
	}
	return out, nil
}
