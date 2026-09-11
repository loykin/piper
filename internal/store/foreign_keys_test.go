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
	"github.com/loykin/piper/pkg/project"
	"github.com/loykin/piper/pkg/security"
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
