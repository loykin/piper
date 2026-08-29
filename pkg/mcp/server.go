package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
)

// ToolHandlerFunc executes one tool call. ctx carries whatever
// request-scoped values the domain wiring layer put there (actor, project
// id, ...) — this package has no opinion on that, it only routes by tool
// name.
//
// A non-nil error here means the tool's own execution failed (e.g. a
// domain-service error) — Server wraps it into a successful "tools/call"
// result with isError:true and the error text as its content, rather than a
// JSON-RPC protocol error, matching the MCP spec's guidance that tool
// execution failures should be visible to the model, not just the
// transport. Return an error from Handle itself (see ToolNotFoundError-style
// use) only for protocol-level problems such as malformed arguments.
type ToolHandlerFunc func(ctx context.Context, args json.RawMessage) (*ToolCallResult, error)

// Tool bundles a tool's advertised definition with its handler.
type Tool struct {
	Definition ToolDefinition
	Handler    ToolHandlerFunc
}

// ResourceReadFunc resolves one resource URI. Returning a *ProtocolError
// lets the domain layer control the JSON-RPC error code/message (e.g.
// invalid params for a malformed/mismatched-project URI); any other error
// is reported as CodeInternalError.
type ResourceReadFunc func(ctx context.Context, uri string) (*ReadResourceResult, error)

// ProtocolError lets domain-layer tool/resource handlers select a specific
// JSON-RPC error code instead of always getting CodeInternalError.
type ProtocolError struct {
	Code    int
	Message string
}

func (e *ProtocolError) Error() string { return e.Message }

// Server dispatches JSON-RPC 2.0 requests for the MCP methods Phase 2
// implements: initialize, notifications/initialized, ping, tools/list,
// tools/call, resources/templates/list, resources/read.
//
// Judgment call on resources/list vs resources/templates/list (design doc
// §8.3 flags this as needing a spec check): every URI Piper exposes has a
// path variable ({name}, {path}, {execution_id}, ...) — none of them name a
// single, enumerable, concrete resource — so these are resource *templates*
// in MCP terms, listed via "resources/templates/list". Plain "resources/list"
// (which enumerates concrete, directly-readable resources with no template
// variables) is intentionally not implemented; a client that calls it gets
// a normal empty list rather than a method-not-found error, since an empty
// list is the spec-correct answer for "no non-template resources exist" and
// keeps a generic client that always probes both list methods working.
type Server struct {
	Info              ServerInfo
	Instructions      string
	Tools             map[string]Tool
	ResourceTemplates []ResourceTemplate
	ReadResource      ResourceReadFunc
}

// HandleMessage dispatches one raw JSON-RPC message (a single request
// object or a batch array) and returns the raw JSON response to write back,
// and whether a response should be written at all (false when the message
// was purely notifications, per JSON-RPC 2.0 batch semantics — the
// Streamable HTTP transport returns 202 Accepted with no body in that
// case).
func (s *Server) HandleMessage(ctx context.Context, raw []byte) ([]byte, bool) {
	reqs, ok := ParseMessage(raw)
	if !ok {
		resp := newErrorResponse(nil, CodeParseError, "invalid JSON-RPC message")
		out, _ := json.Marshal(resp)
		return out, true
	}
	if len(reqs) == 0 {
		resp := newErrorResponse(nil, CodeInvalidRequest, "empty batch")
		out, _ := json.Marshal(resp)
		return out, true
	}

	var responses []*Response
	for _, req := range reqs {
		if resp := s.handleOne(ctx, req); resp != nil {
			responses = append(responses, resp)
		}
	}
	if len(responses) == 0 {
		return nil, false
	}
	// A single top-level (non-batch) request always gets a single top-level
	// response object, not a one-element array.
	if len(reqs) == 1 {
		out, _ := json.Marshal(responses[0])
		return out, true
	}
	out, _ := json.Marshal(responses)
	return out, true
}

func (s *Server) handleOne(ctx context.Context, req Request) *Response {
	if req.JSONRPC != "" && req.JSONRPC != JSONRPCVersion {
		if req.IsNotification() {
			return nil
		}
		return newErrorResponse(req.ID, CodeInvalidRequest, "jsonrpc must be \"2.0\"")
	}

	switch req.Method {
	case "initialize":
		return s.handleInitialize(req)
	case "notifications/initialized":
		return nil // client notification; no response per JSON-RPC/MCP
	case "ping":
		if req.IsNotification() {
			return nil
		}
		return newResultResponse(req.ID, map[string]any{})
	case "tools/list":
		if req.IsNotification() {
			return nil
		}
		return s.handleToolsList(req)
	case "tools/call":
		if req.IsNotification() {
			return nil
		}
		return s.handleToolsCall(ctx, req)
	case "resources/templates/list":
		if req.IsNotification() {
			return nil
		}
		return s.handleResourceTemplatesList(req)
	case "resources/list":
		if req.IsNotification() {
			return nil
		}
		return newResultResponse(req.ID, map[string]any{"resources": []any{}})
	case "resources/read":
		if req.IsNotification() {
			return nil
		}
		return s.handleResourcesRead(ctx, req)
	default:
		if req.IsNotification() {
			return nil
		}
		return newErrorResponse(req.ID, CodeMethodNotFound, fmt.Sprintf("unknown method %q", req.Method))
	}
}

func (s *Server) handleInitialize(req Request) *Response {
	var params InitializeParams
	if len(req.Params) > 0 {
		if err := json.Unmarshal(req.Params, &params); err != nil {
			return newErrorResponse(req.ID, CodeInvalidParams, "invalid initialize params")
		}
	}
	if params.ProtocolVersion != "" && !IsSupportedProtocolVersion(params.ProtocolVersion) {
		return newErrorResponse(req.ID, CodeInvalidParams, (&ErrUnsupportedProtocolVersion{Got: params.ProtocolVersion}).Error())
	}
	result := InitializeResult{
		ProtocolVersion: ProtocolVersion20251125,
		ServerInfo:      s.Info,
		Instructions:    s.Instructions,
		Capabilities: ServerCapabilities{
			Tools:     &ToolsCapability{ListChanged: false},
			Resources: &ResourcesCapability{Subscribe: false, ListChanged: false},
		},
	}
	if req.IsNotification() {
		return nil
	}
	return newResultResponse(req.ID, result)
}

func (s *Server) toolOrder() []string {
	names := make([]string, 0, len(s.Tools))
	for name := range s.Tools {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func (s *Server) handleToolsList(req Request) *Response {
	defs := make([]ToolDefinition, 0, len(s.Tools))
	for _, name := range s.toolOrder() {
		defs = append(defs, s.Tools[name].Definition)
	}
	return newResultResponse(req.ID, map[string]any{"tools": defs})
}

type toolCallParams struct {
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
}

func (s *Server) handleToolsCall(ctx context.Context, req Request) *Response {
	var params toolCallParams
	if err := json.Unmarshal(req.Params, &params); err != nil {
		return newErrorResponse(req.ID, CodeInvalidParams, "invalid tools/call params")
	}
	tool, ok := s.Tools[params.Name]
	if !ok {
		return newErrorResponse(req.ID, CodeInvalidParams, fmt.Sprintf("unknown tool %q", params.Name))
	}
	result, err := tool.Handler(ctx, params.Arguments)
	if err != nil {
		var perr *ProtocolError
		if ok := asProtocolError(err, &perr); ok {
			return newErrorResponse(req.ID, perr.Code, perr.Message)
		}
		result = &ToolCallResult{IsError: true, Content: []ContentBlock{{Type: "text", Text: err.Error()}}}
	}
	return newResultResponse(req.ID, result)
}

func (s *Server) handleResourceTemplatesList(req Request) *Response {
	return newResultResponse(req.ID, map[string]any{"resourceTemplates": s.ResourceTemplates})
}

type resourceReadParams struct {
	URI string `json:"uri"`
}

func (s *Server) handleResourcesRead(ctx context.Context, req Request) *Response {
	var params resourceReadParams
	if err := json.Unmarshal(req.Params, &params); err != nil || params.URI == "" {
		return newErrorResponse(req.ID, CodeInvalidParams, "invalid resources/read params")
	}
	if s.ReadResource == nil {
		return newErrorResponse(req.ID, CodeMethodNotFound, "no resources available")
	}
	result, err := s.ReadResource(ctx, params.URI)
	if err != nil {
		var perr *ProtocolError
		if ok := asProtocolError(err, &perr); ok {
			return newErrorResponse(req.ID, perr.Code, perr.Message)
		}
		return newErrorResponse(req.ID, CodeInternalError, err.Error())
	}
	return newResultResponse(req.ID, result)
}

func asProtocolError(err error, target **ProtocolError) bool {
	if pe, ok := err.(*ProtocolError); ok {
		*target = pe
		return true
	}
	return false
}
