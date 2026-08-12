package membertunnel

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/loykin/piper/internal/memberclient"
	"github.com/loykin/piper/pkg/project"
	"github.com/loykin/piper/pkg/security"
)

func TestDispatchSubmitRunRoundTrip(t *testing.T) {
	var gotReq memberclient.SubmitRunRequest
	var gotAuth memberclient.AuthContext
	var gotRef project.ProjectRef
	member := &fakeMember{
		submitRunFn: func(_ context.Context, auth memberclient.AuthContext, ref project.ProjectRef, req memberclient.SubmitRunRequest) (memberclient.SubmitRunResponse, error) {
			gotReq, gotAuth, gotRef = req, auth, ref
			return memberclient.SubmitRunResponse{RunID: "run-1"}, nil
		},
	}
	auth := memberclient.AuthContext{ActorID: "user-1", Role: security.ProjectRoleMember}
	ref := project.ProjectRef{HomeID: "h", MemberID: "m", ProjectID: "p"}
	req := memberclient.SubmitRunRequest{YAML: "metadata:\n  name: x\n", Experiment: "exp"}
	payload, err := encodeCall(auth, ref, req)
	if err != nil {
		t.Fatal(err)
	}

	respPayload, err := dispatch(context.Background(), member, MethodSubmitRun, payload)
	if err != nil {
		t.Fatalf("dispatch: %v", err)
	}

	if gotReq.Experiment != "exp" || gotAuth.ActorID != "user-1" || gotRef.ProjectID != "p" {
		t.Fatalf("member received wrong args: req=%+v auth=%+v ref=%+v", gotReq, gotAuth, gotRef)
	}
	var resp memberclient.SubmitRunResponse
	if err := json.Unmarshal(respPayload, &resp); err != nil {
		t.Fatal(err)
	}
	if resp.RunID != "run-1" {
		t.Fatalf("RunID = %q, want run-1", resp.RunID)
	}
}

func TestDispatchRerunRunAdaptsMultiArgMethod(t *testing.T) {
	var gotRunID string
	var gotFailedOnly bool
	member := &fakeMember{
		rerunRunFn: func(_ context.Context, _ memberclient.AuthContext, _ project.ProjectRef, runID string, failedOnly bool) (string, error) {
			gotRunID, gotFailedOnly = runID, failedOnly
			return "run-2", nil
		},
	}
	payload, err := encodeCall(memberclient.AuthContext{}, project.ProjectRef{}, RerunRunRequest{RunID: "run-1", FailedOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	respPayload, err := dispatch(context.Background(), member, MethodRerunRun, payload)
	if err != nil {
		t.Fatal(err)
	}
	if gotRunID != "run-1" || !gotFailedOnly {
		t.Fatalf("member received runID=%q failedOnly=%v", gotRunID, gotFailedOnly)
	}
	var newRunID string
	if err := json.Unmarshal(respPayload, &newRunID); err != nil {
		t.Fatal(err)
	}
	if newRunID != "run-2" {
		t.Fatalf("newRunID = %q, want run-2", newRunID)
	}
}

func TestDispatchCancelRunVoidResponse(t *testing.T) {
	called := false
	member := &fakeMember{cancelRunFn: func(context.Context, memberclient.AuthContext, project.ProjectRef, string) error {
		called = true
		return nil
	}}
	payload, err := encodeCall(memberclient.AuthContext{}, project.ProjectRef{}, "run-1")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := dispatch(context.Background(), member, MethodCancelRun, payload); err != nil {
		t.Fatal(err)
	}
	if !called {
		t.Fatal("CancelRun was not called")
	}
}

func TestDispatchPropagatesError(t *testing.T) {
	member := &fakeMember{getRunFn: func(context.Context, memberclient.AuthContext, project.ProjectRef, string) (memberclient.RunDetail, error) {
		return memberclient.RunDetail{}, memberclient.ErrRunNotFound
	}}
	payload, err := encodeCall(memberclient.AuthContext{}, project.ProjectRef{}, "missing")
	if err != nil {
		t.Fatal(err)
	}
	_, err = dispatch(context.Background(), member, MethodGetRun, payload)
	if err == nil || err.Error() != memberclient.ErrRunNotFound.Error() {
		t.Fatalf("err = %v, want %v", err, memberclient.ErrRunNotFound)
	}
}

func TestDispatchUnknownMethod(t *testing.T) {
	_, err := dispatch(context.Background(), &fakeMember{}, "NoSuchMethod", []byte(`{}`))
	if err == nil {
		t.Fatal("expected error for unknown method")
	}
}
