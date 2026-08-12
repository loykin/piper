package commands

import (
	"fmt"
	"log/slog"

	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"

	piper "github.com/loykin/piper"
	cliconfig "github.com/loykin/piper/cmd/piper/config"
	storemod "github.com/loykin/piper/internal/store"
	"github.com/loykin/piper/internal/store/postgres"
	sqlitestore "github.com/loykin/piper/internal/store/sqlite"
	"github.com/loykin/piper/pkg/auth"
	"github.com/loykin/piper/pkg/security"
)

func buildK8sClient(kubeconfig string, inCluster bool) (kubernetes.Interface, error) {
	if inCluster && kubeconfig != "" {
		return nil, fmt.Errorf("config: in_cluster and kubeconfig are mutually exclusive")
	}
	var cfg *rest.Config
	var err error
	if inCluster {
		cfg, err = rest.InClusterConfig()
	} else {
		if kubeconfig == "" {
			return nil, fmt.Errorf("config: kubeconfig is required when in_cluster=false")
		}
		cfg, err = clientcmd.BuildConfigFromFlags("", kubeconfig)
	}
	if err != nil {
		return nil, fmt.Errorf("k8s config: %w", err)
	}
	client, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		return nil, fmt.Errorf("k8s client: %w", err)
	}
	return client, nil
}

// NewPiper builds the library-facing server config from the canonical CLI config.
func NewPiper(loader *cliconfig.Loader) (*piper.Piper, error) {
	root, err := loader.Load()
	if err != nil {
		return nil, err
	}
	if err := cliconfig.ValidateServer(root); err != nil {
		return nil, err
	}
	secrets, err := cliconfig.EnsureServerSecrets(&root)
	if err != nil {
		return nil, err
	}
	if secrets.Path != "" {
		if secrets.Generated {
			slog.Info("generated persistent server secrets", "path", secrets.Path)
		} else {
			slog.Debug("loaded persistent server secrets", "path", secrets.Path)
		}
	}
	var runtimeCfg piper.RuntimeConfig
	switch root.Runtime.Type {
	case cliconfig.InfrastructureK8s:
		client, err := buildK8sClient(root.Runtime.Kubeconfig, root.Runtime.InCluster)
		if err != nil {
			return nil, err
		}
		runnerImage := root.Runtime.PipelineRunner.Image
		if runnerImage == "" {
			runnerImage = "ghcr.io/loykin/piper:latest"
		}
		pullPolicy := root.Runtime.PipelineRunner.ImagePullPolicy
		if pullPolicy == "" {
			pullPolicy = "IfNotPresent"
		}
		runtimeCfg = piper.RuntimeConfig{
			Type: piper.RuntimeK8s,
			K8s: piper.K8sRuntimeConfig{
				Client:              client,
				Namespaces:          append([]string(nil), root.Runtime.Namespaces...),
				PipelineRunnerImage: runnerImage,
				ImagePullPolicy:     pullPolicy,
				WorkloadURL:         root.Runtime.WorkloadURL,
			},
		}
	case cliconfig.InfrastructureDocker:
		var docker cliconfig.RuntimeDockerConfig
		if root.Runtime.Docker != nil {
			docker = *root.Runtime.Docker
		}
		runtimeCfg = piper.RuntimeConfig{
			Type: piper.RuntimeDocker,
			Docker: piper.DockerRuntimeConfig{
				Network:     docker.Network,
				Concurrency: docker.Concurrency,
				WorkloadURL: docker.WorkloadURL,
			},
		}
	case cliconfig.InfrastructureBaremetal:
		var baremetal cliconfig.RuntimeBaremetalConfig
		if root.Runtime.Baremetal != nil {
			baremetal = *root.Runtime.Baremetal
		}
		runtimeCfg = piper.RuntimeConfig{
			Type: piper.RuntimeBaremetal,
			Baremetal: piper.BaremetalRuntimeConfig{
				MetaDir:     baremetal.MetaDir,
				Concurrency: baremetal.Concurrency,
			},
		}
	}
	cfg := piper.Config{
		OutputDir: root.Server.DataDir,
		Git:       piper.GitConfig{User: root.Source.Git.User, Token: root.Source.Git.Token},
		Storage:   piper.StorageConfig{URL: root.Storage.URL, Disabled: root.Storage.Disabled, Token: root.Storage.Token, CredentialRef: root.Storage.CredentialRef},
		Server: piper.ServerConfig{Addr: root.Server.HTTPAddr, WorkerToken: root.Server.WorkerToken, SecretEncryptionKey: root.Server.SecretEncryptionKey, AllowInsecureDevKey: root.Server.AllowInsecureDevKey,
			TLS: piper.TLSConfig{Enabled: root.Server.TLS.Enabled, CertFile: root.Server.TLS.CertFile, KeyFile: root.Server.TLS.KeyFile}},
		Retention: piper.RetentionConfig{RunTTL: root.Server.Retention.RunTTL, ArtifactTTL: root.Server.Retention.ArtifactTTL},
		Schedule:  piper.ScheduleConfig{MisfirePolicy: root.Server.Schedule.MisfirePolicy, MisfireGracePeriod: root.Server.Schedule.MisfireGracePeriod},
		Serving:   piper.ServingConfig{ModelDir: root.Server.Serving.ModelDir},
		DBDriver:  root.Server.DB.Driver, DBDSN: root.Server.DB.DSN, DBPath: root.Server.DB.Path,
		NotebookWorker: piper.NotebookWorkerConfig{
			NotebooksRoot: root.Notebook.NotebooksRoot,
			PortRange:     root.Notebook.PortRange,
		},
		Runtime: runtimeCfg,
	}

	if root.Deployment.Mode == cliconfig.DeploymentModeMember {
		// Member mode never serves the UI or any user-facing/auth-gated
		// route (fed.md §10.7) — it only opens an outbound tunnel to Home
		// (see runMemberMode in server.go). server.auth_signing_key is
		// therefore irrelevant here, not just optional.
		cfg.Auth = piper.AuthConfig{Trusted: true}
		return piper.New(cfg)
	}

	signingKey := root.Server.AuthSigningKey
	if signingKey == "" {
		if !root.Server.AllowInsecureTrustedMode {
			return nil, fmt.Errorf("server.auth_signing_key is required; set server.allow_insecure_trusted_mode=true only for trusted local development")
		}
		cfg.Auth = piper.AuthConfig{Trusted: true}
	} else {
		cfg.Auth = piper.AuthConfig{Factory: func(deps piper.AuthDependencies) (piper.AuthConfig, error) {
			if deps.Executor == nil {
				return piper.AuthConfig{}, fmt.Errorf("server.auth_signing_key requires a database")
			}
			var users auth.UserRepository
			var members security.ProjectMemberRepository
			var sessions auth.SessionRepository
			if deps.Driver == "postgres" {
				users = postgres.NewUserRepo(deps.Executor, storemod.PrimarySource)
				members = postgres.NewMemberRepo(deps.Executor, storemod.PrimarySource)
				sessions = postgres.NewSessionRepo(deps.Executor, storemod.PrimarySource)
			} else {
				users = sqlitestore.NewUserRepo(deps.Executor, storemod.PrimarySource)
				members = sqlitestore.NewMemberRepo(deps.Executor, storemod.PrimarySource)
				sessions = sqlitestore.NewSessionRepo(deps.Executor, storemod.PrimarySource)
			}
			provider := auth.New(auth.Config{SigningKey: []byte(signingKey)}, users, members, sessions)
			return piper.AuthConfig{LoginRoutes: auth.NewHandler(provider, provider, deps.SecureCookies), Authenticator: provider, Authorizer: provider, UserDirectory: provider, UserManager: provider, ProjectMemberManager: provider}, nil
		}}
	}
	return piper.New(cfg)
}
