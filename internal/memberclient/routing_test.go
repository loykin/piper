package memberclient

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/loykin/piper/pkg/project"
)

type routingStub struct {
	Client
	runID string
	refs  []project.ProjectRef
	err   error
	keys  []string
}

func (s *routingStub) SubmitRun(_ context.Context, _ AuthContext, ref project.ProjectRef, req SubmitRunRequest) (SubmitRunResponse, error) {
	s.refs = append(s.refs, ref)
	s.keys = append(s.keys, req.IdempotencyKey)
	return SubmitRunResponse{RunID: s.runID}, s.err
}

func TestRoutingClientRetriesAmbiguousSubmitWithSameKey(t *testing.T) {
	first := &routingStub{err: fmt.Errorf("%w: disconnected", ErrMemberUnavailable)}
	second := &routingStub{runID: "same-run"}
	current := Client(first)
	resolveCount := 0
	router := &RoutingClient{Resolve: func(project.ProjectRef) (Client, error) {
		resolveCount++
		if resolveCount > 1 {
			current = second
		}
		return current, nil
	}}
	req := SubmitRunRequest{IdempotencyKey: "submission-a"}
	got, err := router.SubmitRun(context.Background(), AuthContext{}, project.LocalRef("project-a"), req)
	if err != nil || got.RunID != "same-run" {
		t.Fatalf("SubmitRun = %#v, %v", got, err)
	}
	if len(first.keys) != 1 || len(second.keys) != 1 || first.keys[0] != second.keys[0] {
		t.Fatalf("retry keys first=%v second=%v", first.keys, second.keys)
	}
}

func TestRoutingClientResolvesEveryRequest(t *testing.T) {
	first := &routingStub{runID: "first"}
	second := &routingStub{runID: "second"}
	current := Client(first)
	router := &RoutingClient{Resolve: func(project.ProjectRef) (Client, error) { return current, nil }}
	ref := project.ProjectRef{HomeID: "home-1", MemberID: "member-1", ProjectID: "project-1"}

	got, err := router.SubmitRun(context.Background(), AuthContext{}, ref, SubmitRunRequest{})
	if err != nil || got.RunID != "first" {
		t.Fatalf("first route = %#v, %v", got, err)
	}
	current = second
	got, err = router.SubmitRun(context.Background(), AuthContext{}, ref, SubmitRunRequest{})
	if err != nil || got.RunID != "second" {
		t.Fatalf("second route = %#v, %v", got, err)
	}
	if len(first.refs) != 1 || len(second.refs) != 1 || second.refs[0] != ref {
		t.Fatalf("project ref was not preserved: first=%v second=%v", first.refs, second.refs)
	}
}

func TestRoutingClientReturnsResolverError(t *testing.T) {
	want := errors.New("member unavailable")
	router := &RoutingClient{Resolve: func(project.ProjectRef) (Client, error) { return nil, want }}
	_, err := router.GetRun(context.Background(), AuthContext{}, project.ProjectRef{MemberID: "offline"}, "run-1")
	if !errors.Is(err, want) {
		t.Fatalf("GetRun error = %v, want %v", err, want)
	}
}
