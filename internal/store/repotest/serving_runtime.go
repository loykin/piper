package repotest

import (
	"context"
	"testing"

	"github.com/loykin/piper/pkg/serving"
)

// ServingRuntimeSuite exercises serving.Repository.SetRuntime: it records the
// fields Driver.Deploy assigned without touching the status the runtime has
// already reported, and keeps endpoint/pid cleared once that status ended.
func ServingRuntimeSuite(t *testing.T, repo serving.Repository, projectID string) {
	t.Helper()
	ctx := context.Background()

	for _, tc := range []struct {
		name, reported string
		wantEndpoint   string
		wantPID        int
	}{
		{"svc-running", serving.StatusRunning, "http://deployed", 42},
		{"svc-failed", serving.StatusFailed, "", 0},
	} {
		if err := repo.Upsert(ctx, &serving.Service{ProjectID: projectID, Name: tc.name, Status: serving.StatusStarting}); err != nil {
			t.Fatal(err)
		}
		if err := repo.SetStatusEndpoint(ctx, projectID, tc.name, tc.reported, ""); err != nil {
			t.Fatal(err)
		}
		if err := repo.SetRuntime(ctx, &serving.Service{ProjectID: projectID, Name: tc.name, Status: serving.StatusStarting,
			Endpoint: "http://deployed", PID: 42, Namespace: "ns", RuntimeID: "runtime"}); err != nil {
			t.Fatal(err)
		}
		got, err := repo.Get(ctx, projectID, tc.name)
		if err != nil || got == nil {
			t.Fatalf("get %s: %+v, %v", tc.name, got, err)
		}
		if got.Status != tc.reported || got.Endpoint != tc.wantEndpoint || got.PID != tc.wantPID ||
			got.Namespace != "ns" || got.RuntimeID != "runtime" {
			t.Fatalf("%s after SetRuntime = %+v", tc.name, got)
		}
	}
}
