// Package projectclient defines the versioned HTTP-shaped contract used to
// relay Member-owned Project APIs without exposing a Member HTTP listener.
package projectclient

import (
	"context"
	"net/http"

	"github.com/loykin/piper/internal/memberclient"
	"github.com/loykin/piper/pkg/project"
)

type Request struct {
	Method   string              `json:"method"`
	Path     string              `json:"path"`
	RawQuery string              `json:"raw_query,omitempty"`
	Header   map[string][]string `json:"header,omitempty"`
	Body     []byte              `json:"body,omitempty"`
}

type Response struct {
	Status int                 `json:"status"`
	Header map[string][]string `json:"header,omitempty"`
	Body   []byte              `json:"body,omitempty"`
}

type Client interface {
	DoProjectRequest(ctx context.Context, auth memberclient.AuthContext, ref project.ProjectRef, req Request) (Response, error)
}

// StreamClient relays one project-scoped HTTP connection. Unlike Client's
// buffered JSON contract it preserves streaming bodies and WebSocket upgrades.
type StreamClient interface {
	ServeProjectHTTP(context.Context, memberclient.AuthContext, project.ProjectRef, http.ResponseWriter, *http.Request) error
}
