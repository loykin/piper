package agent

import (
	"context"
	"time"

	corev1 "k8s.io/api/core/v1"
)

// WorkerPodPolicy is the admin-managed baseline pod template for a K8s notebook worker.
// Applied at dispatch time before the manifest's own pod_template (which takes precedence).
type WorkerPodPolicy struct {
	WorkerID    string                 `db:"worker_id"    json:"worker_id"`
	PodTemplate corev1.PodTemplateSpec `db:"-"            json:"pod_template"`
	RawTemplate string                 `db:"pod_template" json:"-"`
	UpdatedAt   time.Time              `db:"updated_at"   json:"updated_at"`
	UpdatedBy   string                 `db:"updated_by"   json:"updated_by"`
}

// WorkerPodPolicyRepository persists per-worker pod template policies.
type WorkerPodPolicyRepository interface {
	List(ctx context.Context) ([]WorkerPodPolicy, error)
	Get(ctx context.Context, workerID string) (*WorkerPodPolicy, error)
	Set(ctx context.Context, policy WorkerPodPolicy) error
	Delete(ctx context.Context, workerID string) error
}
