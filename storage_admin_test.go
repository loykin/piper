package piper

import (
	"context"
	"errors"
	"testing"

	"github.com/loykin/piper/pkg/credential"
	"github.com/loykin/piper/pkg/project"
)

func TestProjectStorageKey(t *testing.T) {
	ctx := project.WithContext(context.Background(), project.Context{ID: "project-a"})

	got, err := projectStorageKey(ctx, "models/model.bin")
	if err != nil {
		t.Fatal(err)
	}
	if got != "projects/project-a/uploads/models/model.bin" {
		t.Fatalf("key = %q", got)
	}
}

func TestProjectStorageKeyRejectsTraversal(t *testing.T) {
	ctx := project.WithContext(context.Background(), project.Context{ID: "project-a"})

	if _, err := projectStorageKey(ctx, "../project-b/secret"); err == nil {
		t.Fatal("expected traversal key to be rejected")
	}
}

func TestProjectStorageKeyRequiresProject(t *testing.T) {
	if _, err := projectStorageKey(context.Background(), "model.bin"); err == nil {
		t.Fatal("expected missing project context to be rejected")
	}
}

// TestCredentialDelete_RefusedWhileReferencedByStorage is the regression for
// the adversarial-review finding that a credential referenced by
// storage.credentialRef could be deleted from the Credentials page with no
// check at all: the UI only cleared its own local form state, leaving the
// persisted storage config pointing at a now-deleted credential — discovered
// only on the next restart, when "resolve storage credential" fails and the
// server won't come up.
func TestCredentialDelete_RefusedWhileReferencedByStorage(t *testing.T) {
	// New() itself eagerly resolves Storage.CredentialRef (it must, to open
	// the store), so it can't be booted with a CredentialRef pointing at a
	// credential that doesn't exist yet. Boot with storage disabled instead,
	// create the credential, then set p.cfg.Storage.CredentialRef directly
	// to simulate the state a real install reaches after this credential
	// was created and referenced by an already-running server (equivalent
	// to what New() would have done, had the credential existed at boot).
	p := newTestPiper(t, Config{OutputDir: t.TempDir(), Storage: StorageConfig{Disabled: true}})
	ctx := context.Background()
	if _, err := p.credentials.Create(ctx, project.SystemID, credential.CreateRequest{
		Name: "storage-cred",
		Kind: credential.KindS3,
		Data: map[string]string{"access_key_id": "AKIA...", "secret_access_key": "secret"},
	}); err != nil {
		t.Fatalf("create credential: %v", err)
	}
	p.cfg.Storage = StorageConfig{URL: "s3://my-bucket", CredentialRef: "storage-cred"}

	err := p.credentials.Delete(ctx, project.SystemID, "storage-cred")
	if !errors.Is(err, credential.ErrInUse) {
		t.Fatalf("Delete() error = %v, want ErrInUse — the running server's storage config still references it", err)
	}

	// An unrelated credential must still delete normally — the guard is
	// scoped to the specific in-use name, not a blanket refusal.
	if _, err := p.credentials.Create(ctx, project.SystemID, credential.CreateRequest{
		Name: "unrelated-cred",
		Kind: credential.KindS3,
		Data: map[string]string{"access_key_id": "AKIA...", "secret_access_key": "secret"},
	}); err != nil {
		t.Fatalf("create unrelated credential: %v", err)
	}
	if err := p.credentials.Delete(ctx, project.SystemID, "unrelated-cred"); err != nil {
		t.Fatalf("Delete() unrelated credential should succeed: %v", err)
	}
}
