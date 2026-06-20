package commands

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"syscall"

	"github.com/jmoiron/sqlx"
	_ "github.com/lib/pq"
	cliconfig "github.com/piper/piper/cmd/piper/config"
	"github.com/spf13/cobra"
	"golang.org/x/term"
	_ "modernc.org/sqlite"

	storemod "github.com/piper/piper/internal/store"
	"github.com/piper/piper/internal/store/postgres"
	sqlitestore "github.com/piper/piper/internal/store/sqlite"
	"github.com/piper/piper/pkg/auth"
	"github.com/piper/piper/pkg/security"
)

// newUserCmd returns the `piper user` sub-command group.
func newUserCmd(loader *cliconfig.Loader) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "user",
		Short: "Manage piper users",
	}
	cmd.AddCommand(
		newUserCreateCmd(loader),
		newUserListCmd(loader),
		newUserDeleteCmd(loader),
	)
	return cmd
}

// openAuthProvider opens the configured database and returns an auth.Provider.
// It opens the DB directly — no Piper instance is created — so background
// goroutines, migrations, and queue loops are not started.
func openAuthProvider(loader *cliconfig.Loader) (*auth.Provider, func() error, error) {
	cfg, loadErr := loader.Load()
	if loadErr != nil {
		return nil, nil, loadErr
	}
	driver := cfg.Server.DB.Driver
	if driver == "" {
		driver = "sqlite"
	}

	var (
		rawDB *sql.DB
		err   error
	)
	switch driver {
	case "postgres":
		dsn := cfg.Server.DB.DSN
		if dsn == "" {
			return nil, nil, fmt.Errorf("db.dsn is required for postgres")
		}
		rawDB, err = sql.Open("postgres", dsn)
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
			return nil, nil, fmt.Errorf("create database directory for %q: %w", dbFile, err)
		}
		rawDB, err = sql.Open("sqlite", dbFile+"?_journal=WAL&_timeout=5000")
	}
	if err != nil {
		return nil, nil, fmt.Errorf("open db: %w", err)
	}
	if err := rawDB.Ping(); err != nil {
		_ = rawDB.Close()
		return nil, nil, fmt.Errorf("db ping: %w", err)
	}

	db := sqlx.NewDb(rawDB, driver)
	closeDB := db.Close
	owned := true
	defer func() {
		if owned {
			_ = closeDB()
		}
	}()
	// Run migrations so that the auth tables exist even on a fresh database.
	if err := storemod.Migrate(context.Background(), db, driver); err != nil {
		return nil, nil, fmt.Errorf("migrate: %w", err)
	}
	signingKey := cfg.Server.AuthSigningKey
	if signingKey == "" {
		signingKey = "cli-placeholder" // CLI only needs user management, not token issuing
	}

	var (
		users    auth.UserRepository
		members  security.ProjectMemberRepository
		sessions auth.SessionRepository
	)
	if driver == "postgres" {
		users = postgres.NewUserRepo(db)
		members = postgres.NewMemberRepo(db)
		sessions = postgres.NewSessionRepo(db)
	} else {
		users = sqlitestore.NewUserRepo(db)
		members = sqlitestore.NewMemberRepo(db)
		sessions = sqlitestore.NewSessionRepo(db)
	}

	provider := auth.New(auth.Config{SigningKey: []byte(signingKey)}, users, members, sessions)
	owned = false
	return provider, closeDB, nil
}

func newUserCreateCmd(loader *cliconfig.Loader) *cobra.Command {
	var email string
	var admin bool

	cmd := &cobra.Command{
		Use:   "create",
		Short: "Create a new user",
		Long: `Create a new piper user.  The password is read interactively from the terminal.

Example:
  piper user create --email admin@example.com --admin`,
		RunE: func(_ *cobra.Command, _ []string) error {
			if email == "" {
				return fmt.Errorf("--email is required")
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
				Email:       email,
				Password:    string(passwordBytes),
				SystemAdmin: admin,
			})
			if err != nil {
				return fmt.Errorf("create user: %w", err)
			}
			fmt.Printf("Created user %s (id: %s)\n", u.Email, u.ID)
			return nil
		},
	}
	cmd.Flags().StringVar(&email, "email", "", "User email address (required)")
	cmd.Flags().BoolVar(&admin, "admin", false, "Grant system admin privileges")
	return cmd
}

func newUserListCmd(loader *cliconfig.Loader) *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List all users",
		RunE: func(_ *cobra.Command, _ []string) error {
			provider, closeDB, err := openAuthProvider(loader)
			if err != nil {
				return err
			}
			defer func() { _ = closeDB() }()

			users, err := provider.ListUsers(context.Background())
			if err != nil {
				return err
			}
			if len(users) == 0 {
				fmt.Println("No users.")
				return nil
			}
			fmt.Printf("%-36s  %-30s  %s\n", "ID", "Email", "Admin")
			for _, u := range users {
				adminStr := ""
				if u.SystemAdmin {
					adminStr = "yes"
				}
				fmt.Printf("%-36s  %-30s  %s\n", u.ID, u.Email, adminStr)
			}
			return nil
		},
	}
}

func newUserDeleteCmd(loader *cliconfig.Loader) *cobra.Command {
	return &cobra.Command{
		Use:   "delete <id>",
		Short: "Delete a user and revoke all sessions",
		Args:  cobra.ExactArgs(1),
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
