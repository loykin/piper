package mlflow

import (
	"context"
	"errors"
	"testing"

	"github.com/loykin/piper/pkg/credential"
)

type fakeCredentials struct {
	meta *credential.Metadata
	err  error
}

func (f fakeCredentials) Get(context.Context, string, string) (*credential.Metadata, error) {
	return f.meta, f.err
}

func TestIntegrationDetailReflectsDispatcherState(t *testing.T) {
	t.Parallel()
	item := &MLflowIntegration{ID: "ml-1", Enabled: true}

	disabled := NewHandler(HandlerDeps{}).integrationDetail(context.Background(), item)
	if disabled.SystemEnabled || disabled.Health != "disabled" {
		t.Fatalf("dispatcher-off detail = system_enabled %v, health %q; want false, disabled", disabled.SystemEnabled, disabled.Health)
	}

	enabled := NewHandler(HandlerDeps{DispatcherEnabled: true}).integrationDetail(context.Background(), item)
	if !enabled.SystemEnabled || enabled.Health != "healthy" {
		t.Fatalf("dispatcher-on detail = system_enabled %v, health %q; want true, healthy", enabled.SystemEnabled, enabled.Health)
	}
}

func TestValidateCredentialRef(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		meta *credential.Metadata
		err  error
	}{
		{name: "missing"},
		{name: "wrong kind", meta: &credential.Metadata{Kind: credential.KindGit}},
		{name: "disabled", meta: &credential.Metadata{Kind: credential.KindMlflow, Disabled: true}},
		{name: "repository error", err: errors.New("db unavailable")},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := NewHandler(HandlerDeps{Credentials: fakeCredentials{meta: tc.meta, err: tc.err}})
			if err := h.validateCredentialRef(context.Background(), "p", "cred"); err == nil {
				t.Fatal("validateCredentialRef() error = nil")
			}
		})
	}
	h := NewHandler(HandlerDeps{Credentials: fakeCredentials{meta: &credential.Metadata{Kind: credential.KindMlflow}}})
	if err := h.validateCredentialRef(context.Background(), "p", "cred"); err != nil {
		t.Fatalf("valid MLflow credential: %v", err)
	}
}
