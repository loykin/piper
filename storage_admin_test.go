package piper

import (
	"context"
	"errors"
	"strings"
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

// TestUpdateStorageSettings_RejectsConfigThatWouldFailValidationOnRestart is
// the regression for the adversarial-review finding that UpdateStorageSettings
// wrote a candidate config straight to storage.yaml without checking it
// against Config.Validate — so a Docker/K8s installation using the built-in
// file store (which requires runtime.docker.workload_url /
// runtime.k8s.workload_url) could save a File-backend config through the UI,
// pass Test Connection (it only checks the store is reachable, not the whole
// Config), and only discover the mistake on the *next restart*, when the
// server refuses to start at all. Constructed as a bare struct literal
// (bypassing New()) since this only needs p.cfg — not a live runtime.
func TestUpdateStorageSettings_RejectsConfigThatWouldFailValidationOnRestart(t *testing.T) {
	p := &Piper{
		cfg: Config{
			OutputDir: t.TempDir(),
			Auth:      AuthConfig{Trusted: true},
			Runtime: RuntimeConfig{
				Type:   RuntimeDocker,
				Docker: DockerRuntimeConfig{Concurrency: 4}, // WorkloadURL deliberately unset
			},
		},
	}

	// The built-in file store (empty URL) on Docker with no workload_url
	// must be rejected before it's ever written.
	_, err := p.UpdateStorageSettings(StorageConfig{})
	if err == nil {
		t.Fatal("UpdateStorageSettings should have rejected a file-backend config on Docker with no workload_url")
	}
	if !strings.Contains(err.Error(), "workload_url") {
		t.Fatalf("UpdateStorageSettings error = %v, want it to mention workload_url", err)
	}
	if _, exists, readErr := p.readStorageSettings(); readErr != nil || exists {
		t.Fatalf("rejected config must not be persisted: exists=%v, err=%v", exists, readErr)
	}

	// The same Docker installation switching to S3 instead needs no
	// workload_url and must be accepted and persisted.
	view, err := p.UpdateStorageSettings(StorageConfig{URL: "s3://my-bucket"})
	if err != nil {
		t.Fatalf("UpdateStorageSettings with s3 backend should succeed: %v", err)
	}
	if view.Config.URL != "s3://my-bucket" {
		t.Fatalf("StorageSettingsView.Config.URL = %q, want s3://my-bucket", view.Config.URL)
	}
	if _, exists, readErr := p.readStorageSettings(); readErr != nil || !exists {
		t.Fatalf("accepted config should be persisted: exists=%v, err=%v", exists, readErr)
	}
}

// TestCredentialDelete_RefusedWhileReferencedByStorage is the regression for
// the adversarial-review finding that a credential referenced by
// storage.credentialRef could be deleted from the Credentials page with no
// check at all: the UI only cleared its own local form state, leaving the
// persisted storage.yaml pointing at a now-deleted credential — discovered
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

// TestCredentialDelete_RefusedWhileReferencedByPendingStorageChange covers
// the other half: UpdateStorageSettings saved a *pending* (not yet applied,
// server not restarted) config referencing a credential the running
// process itself didn't boot with — deleting it would still break the next
// restart.
func TestCredentialDelete_RefusedWhileReferencedByPendingStorageChange(t *testing.T) {
	p := newTestPiper(t, Config{OutputDir: t.TempDir(), Storage: StorageConfig{Disabled: true}})
	ctx := context.Background()
	if _, err := p.credentials.Create(ctx, project.SystemID, credential.CreateRequest{
		Name: "pending-cred",
		Kind: credential.KindS3,
		Data: map[string]string{"access_key_id": "AKIA...", "secret_access_key": "secret"},
	}); err != nil {
		t.Fatalf("create credential: %v", err)
	}
	if _, err := p.UpdateStorageSettings(StorageConfig{URL: "s3://other-bucket", CredentialRef: "pending-cred"}); err != nil {
		t.Fatalf("UpdateStorageSettings: %v", err)
	}

	if err := p.credentials.Delete(ctx, project.SystemID, "pending-cred"); !errors.Is(err, credential.ErrInUse) {
		t.Fatalf("Delete() error = %v, want ErrInUse — a pending storage config change references it", err)
	}
}
