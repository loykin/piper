package execution

import (
	"context"
	"errors"
	"testing"

	"github.com/loykin/piper/pkg/security"
)

// fakeUserDirectory resolves a fixed set of users and counts lookups per ID,
// so tests can assert attachActorNames dedupes repeated actor IDs within a
// single response batch.
type fakeUserDirectory struct {
	users  map[string]*security.User
	lookup map[string]int
}

func (f *fakeUserDirectory) GetUser(_ context.Context, id string) (*security.User, error) {
	f.lookup[id]++
	if u, ok := f.users[id]; ok {
		return u, nil
	}
	return nil, errors.New("not found")
}

func (f *fakeUserDirectory) ListUsers(context.Context, int, int) ([]*security.User, error) {
	return nil, nil
}

func (f *fakeUserDirectory) CountUsers(context.Context) (int, error) { return 0, nil }

// AH regression: a project viewer/member with no system-admin privilege has
// no way to call GET /users, so an actor ID belonging to a system admin who
// isn't an explicit project member (implicit access) used to render as a raw
// UUID. attachActorNames resolves it server-side instead, using whatever
// UserDirectory the Handler already has unrestricted access to.
func TestAttachActorNamesResolvesAndDedupes(t *testing.T) {
	dir := &fakeUserDirectory{
		users: map[string]*security.User{
			"admin-1": {ID: "admin-1", Username: "sys-admin"},
		},
		lookup: map[string]int{},
	}
	h := NewHandler(nil, dir)

	list := []*NotebookExecutionResponse{
		{ID: "e1", RequestedBy: "admin-1", ApprovedBy: "admin-1"},
		{ID: "e2", RequestedBy: "admin-1", DeniedBy: "unknown-user"},
		{ID: "e3"},
	}
	h.attachActorNames(context.Background(), list)

	if list[0].RequestedByUsername != "sys-admin" || list[0].ApprovedByUsername != "sys-admin" {
		t.Fatalf("e1 actor names = %#v", list[0])
	}
	if list[1].RequestedByUsername != "sys-admin" || list[1].DeniedByUsername != "" {
		t.Fatalf("e2 actor names = %#v, want empty DeniedByUsername for an unresolvable ID", list[1])
	}
	if list[2].RequestedByUsername != "" || list[2].ApprovedByUsername != "" || list[2].DeniedByUsername != "" {
		t.Fatalf("e3 (no actor IDs) should stay empty: %#v", list[2])
	}
	if got := dir.lookup["admin-1"]; got != 1 {
		t.Fatalf("admin-1 looked up %d times, want 1 (dedup across the batch)", got)
	}
}

// A Handler with no UserDirectory configured (the pre-existing construction
// path) must leave every *_username field empty rather than panicking.
func TestAttachActorNamesNoDirectoryConfigured(t *testing.T) {
	h := NewHandler(nil)
	list := []*NotebookExecutionResponse{{ID: "e1", RequestedBy: "admin-1"}}
	h.attachActorNames(context.Background(), list)
	if list[0].RequestedByUsername != "" {
		t.Fatalf("RequestedByUsername = %q, want empty with no directory configured", list[0].RequestedByUsername)
	}
}
