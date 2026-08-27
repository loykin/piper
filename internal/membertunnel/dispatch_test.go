package membertunnel

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"github.com/loykin/piper/internal/logstore"
	"github.com/loykin/piper/internal/memberclient"
	"github.com/loykin/piper/internal/projectclient"
	"github.com/loykin/piper/pkg/project"
	"github.com/loykin/piper/pkg/security"
	"github.com/loykin/piper/pkg/statsstore"
)

type fakeProjectClient struct {
	doFn     func(context.Context, memberclient.AuthContext, project.ProjectRef, projectclient.Request) (projectclient.Response, error)
	streamFn func(context.Context, memberclient.AuthContext, project.ProjectRef, http.ResponseWriter, *http.Request) error
}

func (f *fakeProjectClient) ServeProjectHTTP(ctx context.Context, auth memberclient.AuthContext, ref project.ProjectRef, w http.ResponseWriter, req *http.Request) error {
	if f.streamFn == nil {
		return fmt.Errorf("stream not configured")
	}
	return f.streamFn(ctx, auth, ref, w, req)
}

func (f *fakeProjectClient) DoProjectRequest(ctx context.Context, auth memberclient.AuthContext, ref project.ProjectRef, req projectclient.Request) (projectclient.Response, error) {
	return f.doFn(ctx, auth, ref, req)
}

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

func TestDispatchQueryLogsPreservesCursorPage(t *testing.T) {
	cursor := statsstore.CursorFromID(41)
	member := &fakeMember{queryLogsFn: func(_ context.Context, _ memberclient.AuthContext, ref project.ProjectRef, req memberclient.QueryLogsRequest) (memberclient.QueryLogsResponse, error) {
		if ref.ProjectID != "project-1" || req.Cursor != cursor || req.Limit != 25 {
			t.Fatalf("query ref=%+v req=%+v", ref, req)
		}
		return memberclient.QueryLogsResponse{Lines: []*logstore.Line{{ID: 42}}, NextCursor: statsstore.CursorFromID(42)}, nil
	}}
	payload, err := encodeCall(memberclient.AuthContext{}, project.ProjectRef{ProjectID: "project-1"}, memberclient.QueryLogsRequest{RunID: "run-1", StepName: "train", Cursor: cursor, Limit: 25})
	if err != nil {
		t.Fatal(err)
	}
	responsePayload, err := dispatch(context.Background(), member, MethodQueryLogs, payload)
	if err != nil {
		t.Fatal(err)
	}
	var response memberclient.QueryLogsResponse
	if err := json.Unmarshal(responsePayload, &response); err != nil {
		t.Fatal(err)
	}
	if len(response.Lines) != 1 || response.Lines[0].ID != 42 || response.NextCursor != statsstore.CursorFromID(42) {
		t.Fatalf("response = %+v", response)
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

func TestDispatchProjectRequestRoundTrip(t *testing.T) {
	auth := memberclient.AuthContext{ActorID: "user-1", Role: security.ProjectRoleMember}
	ref := project.ProjectRef{HomeID: "home-1", MemberID: "member-1", ProjectID: "project-1"}
	req := projectclient.Request{Method: "POST", Path: "/schedules", Body: []byte(`{"name":"nightly"}`)}
	payload, err := encodeCall(auth, ref, req)
	if err != nil {
		t.Fatal(err)
	}
	projectMember := &fakeProjectClient{doFn: func(_ context.Context, gotAuth memberclient.AuthContext, gotRef project.ProjectRef, gotReq projectclient.Request) (projectclient.Response, error) {
		if gotAuth.ActorID != auth.ActorID || gotRef != ref || gotReq.Path != req.Path || string(gotReq.Body) != string(req.Body) {
			t.Fatalf("project request auth=%+v ref=%+v req=%+v", gotAuth, gotRef, gotReq)
		}
		return projectclient.Response{Status: 201, Body: []byte(`{"id":"schedule-1"}`)}, nil
	}}

	responsePayload, err := dispatch(context.Background(), &fakeMember{}, MethodProjectRequest, payload, projectMember)
	if err != nil {
		t.Fatal(err)
	}
	var response projectclient.Response
	if err := json.Unmarshal(responsePayload, &response); err != nil {
		t.Fatal(err)
	}
	if response.Status != 201 || string(response.Body) != `{"id":"schedule-1"}` {
		t.Fatalf("response = %+v", response)
	}
}

func TestDispatchProjectRequestRequiresProjectClient(t *testing.T) {
	payload, err := encodeCall(memberclient.AuthContext{}, project.ProjectRef{}, projectclient.Request{Path: "/schedules"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := dispatch(context.Background(), &fakeMember{}, MethodProjectRequest, payload); err == nil {
		t.Fatal("expected unavailable project relay error")
	}
}
