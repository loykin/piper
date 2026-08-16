package commands

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"syscall"

	cliconfig "github.com/loykin/piper/cmd/piper/config"
	storemod "github.com/loykin/piper/internal/store"
	"github.com/loykin/piper/internal/store/postgres"
	sqlitestore "github.com/loykin/piper/internal/store/sqlite"
	"github.com/loykin/piper/pkg/auth"
	"github.com/loykin/piper/pkg/security"
	"github.com/spf13/cobra"
	"golang.org/x/term"
)

// newUserCmd returns the `piper user` sub-command group.
func newUserCmd(loader *cliconfig.Loader) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "user",
		Short: "manage piper users",
	}
	cmd.AddCommand(
		newUserCreateCmd(loader),
		newUserListCmd(loader),
		newUserDeleteCmd(loader),
	)
	return cmd
}

// openRepos opens the configured database directly — no Piper instance is
// created, so background goroutines and queue loops are not started — and
// returns the fully-wired Repos. Shared by any CLI command that needs direct
// DB access without running a server (user management, manifest migration).
func openRepos(cfg cliconfig.RootConfig) (*storemod.Repos, error) {
	driver := cfg.Server.DB.Driver
	if driver == "" {
		driver = "sqlite"
	}

	switch driver {
	case "postgres":
		dsn := cfg.Server.DB.DSN
		if dsn == "" {
			return nil, fmt.Errorf("db.dsn is required for postgres")
		}
		repos, err := storemod.OpenPostgres(dsn)
		if err != nil {
			return nil, fmt.Errorf("open db: %w", err)
		}
		return repos, nil
	default:
		dbFile := cfg.Server.DB.Path
		if dbFile == "" {
			outputDir := cfg.Server.DataDir
			if outputDir == "" {
				outputDir = "./piper-outputs"
			}
			dbFile = filepath.Join(outputDir, "piper.db")
		}
		if err := os.MkdirAll(filepath.Dir(dbFile), 0755); err != nil {
			return nil, fmt.Errorf("create database directory for %q: %w", dbFile, err)
		}
		repos, err := storemod.Open(dbFile)
		if err != nil {
			return nil, fmt.Errorf("open db: %w", err)
		}
		return repos, nil
	}
}

// openAuthProvider opens the configured database and returns an auth.Provider.
func openAuthProvider(loader *cliconfig.Loader) (*auth.Provider, func() error, error) {
	cfg, loadErr := loader.Load()
	if loadErr != nil {
		return nil, nil, loadErr
	}
	repos, err := openRepos(cfg)
	if err != nil {
		return nil, nil, err
	}
	closeOnError := true
	defer func() {
		if closeOnError {
			_ = repos.Close()
		}
	}()

	driver := cfg.Server.DB.Driver
	if driver == "" {
		driver = "sqlite"
	}

	signingKey := cfg.Server.AuthSigningKey
	if signingKey == "" {
		signingKey = "cli-placeholder" // CLI only needs user management, not token issuing
	}

	executor := repos.Executor()
	var (
		users    auth.UserRepository
		members  security.ProjectMemberRepository
		sessions auth.SessionRepository
	)
	if driver == "postgres" {
		users = postgres.NewUserRepo(executor, storemod.PrimarySource)
		members = postgres.NewMemberRepo(executor, storemod.PrimarySource)
		sessions = postgres.NewSessionRepo(executor, storemod.PrimarySource)
	} else {
		users = sqlitestore.NewUserRepo(executor, storemod.PrimarySource)
		members = sqlitestore.NewMemberRepo(executor, storemod.PrimarySource)
		sessions = sqlitestore.NewSessionRepo(executor, storemod.PrimarySource)
	}

	provider := auth.New(auth.Config{SigningKey: []byte(signingKey)}, users, members, sessions)
	closeOnError = false
	return provider, repos.Close, nil
}

func newUserCreateCmd(loader *cliconfig.Loader) *cobra.Command {
	var username string
	var admin bool

	cmd := &cobra.Command{
		Use:   "create",
		Short: "create a new user",
		Long: `Create a new piper user.  The password is read interactively from the terminal.

Example:
  piper user create --username admin --admin`,
		PreRunE: makePreRunE(loader),
		RunE: func(_ *cobra.Command, _ []string) error {
			if username == "" {
				return fmt.Errorf("--username is required")
			}

			_, _ = fmt.Fprint(os.Stderr, "Password: ")
			passwordBytes, err := term.ReadPassword(int(syscall.Stdin))
			_, _ = fmt.Fprintln(os.Stderr)
			if err != nil {
				return fmt.Errorf("read password: %w", err)
			}
			if len(passwordBytes) < 8 {
				return fmt.Errorf("password must be at least 8 characters")
			}

			provider, closeDB, err := openAuthProvider(loader)
			if err != nil {
				return err
			}
			defer func() { _ = closeDB() }()

			u, err := provider.CreateUser(context.Background(), security.CreateUserInput{
				Username:    username,
				Password:    string(passwordBytes),
				SystemAdmin: admin,
			})
			if err != nil {
				return fmt.Errorf("create user: %w", err)
			}
			fmt.Printf("Created user %s (id: %s)\n", u.Username, u.ID)
			return nil
		},
	}
	cmd.Flags().StringVar(&username, "username", "", "Login username (required)")
	cmd.Flags().BoolVar(&admin, "admin", false, "Grant system admin privileges")
	return cmd
}

func newUserListCmd(loader *cliconfig.Loader) *cobra.Command {
	return &cobra.Command{
		Use:     "list",
		Short:   "list all users",
		PreRunE: makePreRunE(loader),
		RunE: func(_ *cobra.Command, _ []string) error {
			provider, closeDB, err := openAuthProvider(loader)
			if err != nil {
				return err
			}
			defer func() { _ = closeDB() }()

			users, err := provider.ListUsers(context.Background(), 0, 0)
			if err != nil {
				return err
			}
			if len(users) == 0 {
				fmt.Println("No users.")
				return nil
			}
			fmt.Printf("%-36s  %-30s  %s\n", "ID", "Username", "Admin")
			for _, u := range users {
				adminStr := ""
				if u.SystemAdmin {
					adminStr = "yes"
				}
				fmt.Printf("%-36s  %-30s  %s\n", u.ID, u.Username, adminStr)
			}
			return nil
		},
	}
}

func newUserDeleteCmd(loader *cliconfig.Loader) *cobra.Command {
	return &cobra.Command{
		Use:     "delete <id>",
		Short:   "delete a user and revoke all sessions",
		Args:    cobra.ExactArgs(1),
		PreRunE: makePreRunE(loader),
		RunE: func(_ *cobra.Command, args []string) error {
			provider, closeDB, err := openAuthProvider(loader)
			if err != nil {
				return err
			}
			defer func() { _ = closeDB() }()

			if err := provider.DeleteUser(context.Background(), args[0]); err != nil {
				return fmt.Errorf("delete user: %w", err)
			}
			fmt.Printf("Deleted user %s\n", args[0])
			return nil
		},
	}
}
