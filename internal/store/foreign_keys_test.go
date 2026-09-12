package store

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/loykin/piper/internal/store/sqlite"
	"github.com/loykin/piper/pkg/auth"
	"github.com/loykin/piper/pkg/credential"
	"github.com/loykin/piper/pkg/notebook"
	"github.com/loykin/piper/pkg/project"
	"github.com/loykin/piper/pkg/security"
	"github.com/loykin/piper/pkg/serving"
)

// Regression test for the SQLite driver silently ignoring the schema's
// `ON DELETE CASCADE` because the connection never enabled
// `PRAGMA foreign_keys` (see Open's DSN). Deleting a project or a user must
// actually take its dependent rows with it, not just remove the parent row
// and leave orphans behind — asserting the response/error alone (as the
// pre-existing repotest.ProjectRepoSuite delete case does) can't catch this
// class of bug because it never creates child rows in the first place.
func TestSQLiteForeignKeyCascadeOnDelete(t *testing.T) {
	ctx := context.Background()
	repos, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = repos.Close() })

	t.Run("deleting a project cascades to its credentials", func(t *testing.T) {
		proj := &project.Project{ID: "cascade-project", Name: "cascade-project"}
		if err := repos.Project.Create(ctx, proj); err != nil {
			t.Fatalf("create project: %v", err)
		}
		meta := &credential.Metadata{ProjectID: proj.ID, Name: "github", Kind: credential.KindGeneric, Keys: []string{"token"}}
		if err := repos.Credential.Create(ctx, meta, []byte("secret")); err != nil {
			t.Fatalf("create credential: %v", err)
		}

		if err := repos.Project.Delete(ctx, proj.ID); err != nil {
			t.Fatalf("delete project: %v", err)
		}

		if got, err := repos.Credential.Get(ctx, proj.ID, "github"); err != nil || got != nil {
			t.Fatalf("credential survived project deletion: got=%#v err=%v", got, err)
		}
	})

	t.Run("deleting a user cascades to its project memberships", func(t *testing.T) {
		proj := &project.Project{ID: "membership-project", Name: "membership-project"}
		if err := repos.Project.Create(ctx, proj); err != nil {
			t.Fatalf("create project: %v", err)
		}
		users := sqlite.NewUserRepo(repos.Executor(), PrimarySource)
		members := sqlite.NewMemberRepo(repos.Executor(), PrimarySource)

		user := &auth.User{ID: "cascade-user", Username: "cascade-user", PasswordHash: "hash"}
		if err := users.Create(ctx, user); err != nil {
			t.Fatalf("create user: %v", err)
		}
		now := time.Now().UTC()
		if err := members.Add(ctx, &security.ProjectMember{ProjectID: proj.ID, UserID: user.ID, Role: "member", CreatedAt: now, UpdatedAt: now}); err != nil {
			t.Fatalf("add project member: %v", err)
		}

		if err := users.Delete(ctx, user.ID); err != nil {
			t.Fatalf("delete user: %v", err)
		}

		if got, err := members.Get(ctx, proj.ID, user.ID); !errors.Is(err, sql.ErrNoRows) {
			t.Fatalf("project membership survived user deletion: got=%#v err=%v, want %v", got, err, sql.ErrNoRows)
		}
	})
}

// TestProjectDeleteDoesNotCascadeToHistory is the deliberate counterpart to
// TestSQLiteForeignKeyCascadeOnDelete's credential case: notebook_history/
// service_history are append-only audit logs (BA in the adversarial QA
// review — deleting a project used to wipe its entire notebook/service
// history via ON DELETE CASCADE the moment FK enforcement was actually
// turned on). Migration 00047_history_drop_project_fk removed that FK
// entirely; project_id is now a plain tag, and retention.
// NotebookHistoryTTL/ServiceHistoryTTL (internal/retention) is the only
// lifecycle policy for these rows. Deleting the project they reference must
// not touch them.
func TestProjectDeleteDoesNotCascadeToHistory(t *testing.T) {
	ctx := context.Background()
	repos, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = repos.Close() })

	proj := &project.Project{ID: "history-survives-project", Name: "history-survives-project"}
	if err := repos.Project.Create(ctx, proj); err != nil {
		t.Fatalf("create project: %v", err)
	}
	if err := repos.Notebook.AppendHistory(ctx, &notebook.NotebookServer{ProjectID: proj.ID, Name: "nb", Status: "stopped", CreatedAt: time.Now().UTC()}); err != nil {
		t.Fatalf("append notebook history: %v", err)
	}
	if err := repos.Serving.AppendHistory(ctx, &serving.Service{ProjectID: proj.ID, Name: "svc", Status: "stopped", CreatedAt: time.Now().UTC()}); err != nil {
		t.Fatalf("append service history: %v", err)
	}

	if err := repos.Project.Delete(ctx, proj.ID); err != nil {
		t.Fatalf("delete project: %v", err)
	}

	nbHist, err := repos.Notebook.ListHistory(ctx, proj.ID, 0, 0)
	if err != nil {
		t.Fatalf("list notebook history: %v", err)
	}
	if len(nbHist) != 1 {
		t.Fatalf("notebook_history did not survive project deletion: got %d rows, want 1", len(nbHist))
	}

	svcHist, err := repos.Serving.ListHistory(ctx, proj.ID, 0, 0)
	if err != nil {
		t.Fatalf("list service history: %v", err)
	}
	if len(svcHist) != 1 {
		t.Fatalf("service_history did not survive project deletion: got %d rows, want 1", len(svcHist))
	}
}
