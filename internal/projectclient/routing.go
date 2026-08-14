package projectclient

import (
	"context"
	"fmt"

	"github.com/loykin/piper/internal/memberclient"
	"github.com/loykin/piper/pkg/project"
)

type RoutingClient struct {
	Resolve func(project.ProjectRef) (Client, error)
}

func (c *RoutingClient) DoProjectRequest(ctx context.Context, auth memberclient.AuthContext, ref project.ProjectRef, req Request) (Response, error) {
	if c == nil || c.Resolve == nil {
		return Response{}, fmt.Errorf("projectclient: member resolver is not configured")
	}
	client, err := c.Resolve(ref)
	if err != nil {
		return Response{}, err
	}
	if client == nil {
		return Response{}, fmt.Errorf("projectclient: resolver returned no client for member %q", ref.MemberID)
	}
	return client.DoProjectRequest(ctx, auth, ref, req)
}

var _ Client = (*RoutingClient)(nil)
