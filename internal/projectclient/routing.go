package projectclient

import (
	"context"
	"fmt"
	"net/http"

	"github.com/loykin/piper/internal/memberclient"
	"github.com/loykin/piper/pkg/project"
)

type RoutingClient struct {
	Resolve func(project.ProjectRef) (Client, error)
}

func (c *RoutingClient) ServeProjectHTTP(ctx context.Context, auth memberclient.AuthContext, ref project.ProjectRef, w http.ResponseWriter, req *http.Request) error {
	if c == nil || c.Resolve == nil {
		return fmt.Errorf("projectclient: member resolver is not configured")
	}
	client, err := c.Resolve(ref)
	if err != nil {
		return err
	}
	stream, ok := client.(StreamClient)
	if !ok {
		return fmt.Errorf("projectclient: member %q has no HTTP stream", ref.MemberID)
	}
	return stream.ServeProjectHTTP(ctx, auth, ref, w, req)
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
var _ StreamClient = (*RoutingClient)(nil)
