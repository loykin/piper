package mcp

import (
	"context"

	"github.com/loykin/piper/pkg/notebook/execution"
)

// requestScope carries the per-HTTP-request identity Piper's own middleware
// already resolved (mirrors execution.Handler's actorFrom) into the
// pkg/mcp.Server's tool/resource handler closures. pkg/mcp's Server.Handle
// only threads a plain context.Context through, so this is how the domain
// wiring layer gets the caller's Actor and project id into each tool call
// without pkg/mcp needing to know anything about either type (design doc
// §4.1's package boundary).
type requestScope struct {
	Actor     execution.Actor
	ProjectID string
}

type requestScopeKey struct{}

func withRequestScope(ctx context.Context, actor execution.Actor, projectID string) context.Context {
	return context.WithValue(ctx, requestScopeKey{}, requestScope{Actor: actor, ProjectID: projectID})
}

func requestScopeFrom(ctx context.Context) requestScope {
	scope, _ := ctx.Value(requestScopeKey{}).(requestScope)
	return scope
}
