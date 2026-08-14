package projectclient

import (
	"context"
	"errors"
	"testing"

	"github.com/loykin/piper/internal/memberclient"
	"github.com/loykin/piper/pkg/project"
)

type clientFunc func(context.Context, memberclient.AuthContext, project.ProjectRef, Request) (Response, error)

func (f clientFunc) DoProjectRequest(ctx context.Context, auth memberclient.AuthContext, ref project.ProjectRef, req Request) (Response, error) {
	return f(ctx, auth, ref, req)
}

func TestRoutingClientResolvesOwningMember(t *testing.T) {
	ref := project.ProjectRef{HomeID: "home-1", MemberID: "member-2", ProjectID: "project-3"}
	want := Response{Status: 202, Body: []byte("member response")}
	client := &RoutingClient{Resolve: func(got project.ProjectRef) (Client, error) {
		if got != ref {
			t.Fatalf("resolved ref = %+v, want %+v", got, ref)
		}
		return clientFunc(func(_ context.Context, _ memberclient.AuthContext, gotRef project.ProjectRef, req Request) (Response, error) {
			if gotRef != ref || req.Path != "/schedules" {
				t.Fatalf("request ref=%+v path=%q", gotRef, req.Path)
			}
			return want, nil
		}), nil
	}}

	got, err := client.DoProjectRequest(context.Background(), memberclient.AuthContext{}, ref, Request{Path: "/schedules"})
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != want.Status || string(got.Body) != string(want.Body) {
		t.Fatalf("response = %+v, want %+v", got, want)
	}
}

func TestRoutingClientPropagatesResolverError(t *testing.T) {
	want := errors.New("member offline")
	client := &RoutingClient{Resolve: func(project.ProjectRef) (Client, error) { return nil, want }}
	_, err := client.DoProjectRequest(context.Background(), memberclient.AuthContext{}, project.ProjectRef{}, Request{})
	if !errors.Is(err, want) {
		t.Fatalf("error = %v, want %v", err, want)
	}
}

func TestRoutingClientRejectsMissingResolverOrClient(t *testing.T) {
	for name, client := range map[string]*RoutingClient{
		"nil resolver": {},
		"nil client":   {Resolve: func(project.ProjectRef) (Client, error) { return nil, nil }},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := client.DoProjectRequest(context.Background(), memberclient.AuthContext{}, project.ProjectRef{}, Request{}); err == nil {
				t.Fatal("expected configuration error")
			}
		})
	}
}
