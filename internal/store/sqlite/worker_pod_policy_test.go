package sqlite_test

import (
	"context"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"

	iagent "github.com/piper/piper/internal/agent"
	"github.com/piper/piper/internal/store"
)

func TestWorkerPodPolicyRepo_CRUD(t *testing.T) {
	repos, err := store.Open(":memory:")
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = repos.Close() })

	ctx := context.Background()
	repo := repos.WorkerPodPolicy

	// Get on empty — nil, no error
	got, err := repo.Get(ctx, "worker-1")
	if err != nil {
		t.Fatalf("Get on empty: %v", err)
	}
	if got != nil {
		t.Fatalf("expected nil, got %+v", got)
	}

	// Set
	policy := iagent.WorkerPodPolicy{
		WorkerID:  "worker-1",
		UpdatedBy: "admin",
		PodTemplate: corev1.PodTemplateSpec{
			Spec: corev1.PodSpec{
				NodeSelector: map[string]string{"nvidia.com/gpu": "true"},
				Tolerations: []corev1.Toleration{
					{Key: "dedicated", Value: "gpu"},
				},
			},
		},
	}
	if err := repo.Set(ctx, policy); err != nil {
		t.Fatalf("Set: %v", err)
	}

	// Get after Set
	got, err = repo.Get(ctx, "worker-1")
	if err != nil {
		t.Fatalf("Get after Set: %v", err)
	}
	if got == nil {
		t.Fatal("expected policy, got nil")
	}
	if got.WorkerID != "worker-1" {
		t.Errorf("worker_id: want worker-1, got %q", got.WorkerID)
	}
	if got.UpdatedBy != "admin" {
		t.Errorf("updated_by: want admin, got %q", got.UpdatedBy)
	}
	if got.PodTemplate.Spec.NodeSelector["nvidia.com/gpu"] != "true" {
		t.Errorf("node selector not round-tripped: %v", got.PodTemplate.Spec.NodeSelector)
	}
	if len(got.PodTemplate.Spec.Tolerations) != 1 || got.PodTemplate.Spec.Tolerations[0].Key != "dedicated" {
		t.Errorf("tolerations not round-tripped: %v", got.PodTemplate.Spec.Tolerations)
	}
	if got.UpdatedAt.IsZero() {
		t.Error("updated_at should be set")
	}

	// Upsert — update existing
	policy.PodTemplate.Spec.NodeSelector["zone"] = "us-east-1"
	policy.UpdatedBy = "ops"
	if err := repo.Set(ctx, policy); err != nil {
		t.Fatalf("Set upsert: %v", err)
	}
	got2, err := repo.Get(ctx, "worker-1")
	if err != nil {
		t.Fatalf("Get after upsert: %v", err)
	}
	if got2.PodTemplate.Spec.NodeSelector["zone"] != "us-east-1" {
		t.Errorf("upsert: zone not updated, got %v", got2.PodTemplate.Spec.NodeSelector)
	}
	if got2.UpdatedBy != "ops" {
		t.Errorf("upsert: updated_by not updated, got %q", got2.UpdatedBy)
	}

	// Different worker — isolated
	got3, err := repo.Get(ctx, "worker-2")
	if err != nil {
		t.Fatalf("Get worker-2: %v", err)
	}
	if got3 != nil {
		t.Fatalf("worker-2 should have no policy, got %+v", got3)
	}

	// Delete
	if err := repo.Delete(ctx, "worker-1"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	got4, err := repo.Get(ctx, "worker-1")
	if err != nil {
		t.Fatalf("Get after Delete: %v", err)
	}
	if got4 != nil {
		t.Fatalf("expected nil after delete, got %+v", got4)
	}

	// Delete non-existent — no error
	if err := repo.Delete(ctx, "no-such-worker"); err != nil {
		t.Fatalf("Delete non-existent should be no-op: %v", err)
	}
}

func TestWorkerPodPolicyRepo_UpdatedAtAutoSet(t *testing.T) {
	repos, err := store.Open(":memory:")
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = repos.Close() })

	ctx := context.Background()
	before := time.Now().Add(-time.Second)

	policy := iagent.WorkerPodPolicy{WorkerID: "w1"}
	if err := repos.WorkerPodPolicy.Set(ctx, policy); err != nil {
		t.Fatalf("Set: %v", err)
	}
	got, _ := repos.WorkerPodPolicy.Get(ctx, "w1")
	if got.UpdatedAt.Before(before) {
		t.Errorf("updated_at %v is before set time %v", got.UpdatedAt, before)
	}
}
