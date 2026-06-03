package tunnelproxy

import (
	"net/http"
	"testing"
)

func TestPolicyRegistryRegisterAndBuild(t *testing.T) {
	r := NewRegistry()
	r.Register("demo", func(ctx PolicyContext) Policy {
		return PolicyFunc{
			OnRequest: func(req *http.Request) error {
				req.Header.Set("X-Demo", ctx.Name)
				return nil
			},
		}
	})

	policy, err := r.Build("demo", PolicyContext{Name: "notebook"})
	if err != nil {
		t.Fatalf("Build error: %v", err)
	}
	req, _ := http.NewRequest(http.MethodGet, "http://example.com", nil)
	if err := policy.RewriteRequest(req); err != nil {
		t.Fatalf("RewriteRequest error: %v", err)
	}
	if got := req.Header.Get("X-Demo"); got != "notebook" {
		t.Fatalf("header = %q, want notebook", got)
	}
}

func TestPolicyRegistryMissing(t *testing.T) {
	r := NewRegistry()
	if _, err := r.Build("missing", PolicyContext{}); err == nil {
		t.Fatal("Build missing policy = nil error, want error")
	}
}
