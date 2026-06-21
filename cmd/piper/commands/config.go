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
	cfg := piper.Config{
		OutputDir: root.Server.DataDir,
		Git:       piper.GitConfig{User: root.Source.Git.User, Token: root.Source.Git.Token},
		Storage:   piper.StorageConfig{URL: root.Storage.URL, Disabled: root.Storage.Disabled, Token: root.Storage.Token},
		Server: piper.ServerConfig{Addr: root.Server.HTTPAddr, WorkerToken: root.Server.WorkerToken,
			TLS: piper.TLSConfig{Enabled: root.Server.TLS.Enabled, CertFile: root.Server.TLS.CertFile, KeyFile: root.Server.TLS.KeyFile}},
		Retention: piper.RetentionConfig{RunTTL: root.Server.Retention.RunTTL, ArtifactTTL: root.Server.Retention.ArtifactTTL},
		Schedule:  piper.ScheduleConfig{MisfirePolicy: root.Server.Schedule.MisfirePolicy, MisfireGracePeriod: root.Server.Schedule.MisfireGracePeriod},
		Serving:   piper.ServingConfig{ModelDir: root.Server.Serving.ModelDir},
		DBDriver:  root.Server.DB.Driver, DBDSN: root.Server.DB.DSN, DBPath: root.Server.DB.Path,
		NotebookWorker: piper.NotebookWorkerConfig{
			NotebooksRoot: root.Server.Local.NotebookCfg.NotebooksRoot,
			PortRange:     root.Server.Local.NotebookCfg.PortRange,
		},
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
