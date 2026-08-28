package sqlite

import (
	"testing"

	"github.com/loykin/piper/pkg/integration/mlflow"
)

// TestNewMlflowRepo_StoresInjectedPolicy is the regression for the
// adversarial-review finding that CreateIntegration/UpdateIntegration
// validated every write against a hardcoded mlflow.DefaultSSRFPolicy() call
// inline, ignoring whatever policy the repository was actually constructed
// with — so a caller that wanted a legitimately more permissive policy (e.g.
// a future dev-mode `integrations.mlflow.allow_insecure_http` config) had no
// way to actually relax it. CreateIntegration/UpdateIntegration now validate
// against r.policy (see mlflow.go), so the fix is fully verified by
// confirming the constructor actually stores what it's given — a direct
// field check, in-package, rather than exercising a real DB round-trip
// (which m.Validate(r.policy) short-circuits before reaching anyway, since
// it's the first statement in both methods).
func TestNewMlflowRepo_StoresInjectedPolicy(t *testing.T) {
	policy := mlflow.SSRFPolicy{AllowInsecureHTTP: true, AllowedHosts: []string{"mlflow.internal"}}
	repo := NewMlflowRepo(nil, "primary", policy)

	got, ok := repo.(*mlflowRepo)
	if !ok {
		t.Fatalf("NewMlflowRepo returned %T, want *mlflowRepo", repo)
	}
	if got.policy.AllowInsecureHTTP != policy.AllowInsecureHTTP {
		t.Errorf("policy.AllowInsecureHTTP = %v, want %v", got.policy.AllowInsecureHTTP, policy.AllowInsecureHTTP)
	}
	if len(got.policy.AllowedHosts) != 1 || got.policy.AllowedHosts[0] != "mlflow.internal" {
		t.Errorf("policy.AllowedHosts = %v, want [mlflow.internal]", got.policy.AllowedHosts)
	}
}
