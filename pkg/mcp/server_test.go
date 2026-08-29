package mcp

import (
	"context"
	"encoding/json"
	"testing"
)

func testServer() *Server {
	return &Server{
		Info: ServerInfo{Name: "piper-test", Version: "0.0.0"},
		Tools: map[string]Tool{
			"echo": {
				Definition: ToolDefinition{
					Name:        "echo",
					InputSchema: json.RawMessage(`{"type":"object"}`),
					Annotations: ToolAnnotations{ReadOnlyHint: true, OpenWorldHint: false},
				},
				Handler: func(_ context.Context, args json.RawMessage) (*ToolCallResult, error) {
					return TextResult("ok", map[string]string{"echo": string(args)})
				},
			},
			"boom": {
				Definition: ToolDefinition{Name: "boom", InputSchema: json.RawMessage(`{"type":"object"}`)},
				Handler: func(_ context.Context, _ json.RawMessage) (*ToolCallResult, error) {
					return nil, errBoom
				},
			},
		},
		ResourceTemplates: []ResourceTemplate{
			{URITemplate: "piper://projects/{project_id}/widgets/{id}", Name: "widget"},
		},
		ReadResource: func(_ context.Context, uri string) (*ReadResourceResult, error) {
			if uri == "piper://projects/p1/widgets/missing" {
				return nil, &ProtocolError{Code: CodeInvalidParams, Message: "not found"}
			}
			return &ReadResourceResult{Contents: []ResourceContents{{URI: uri, Text: "hello"}}}, nil
		},
	}
}

var errBoom = errTest("boom")

type errTest string

func (e errTest) Error() string { return string(e) }

func mustHandle(t *testing.T, s *Server, req string) *Response {
	t.Helper()
	out, has := s.HandleMessage(context.Background(), []byte(req))
	if !has {
		t.Fatalf("expected a response for: %s", req)
	}
	var resp Response
	if err := json.Unmarshal(out, &resp); err != nil {
		t.Fatalf("invalid JSON response: %v (%s)", err, out)
	}
	return &resp
}

func TestInitialize(t *testing.T) {
	s := testServer()
	resp := mustHandle(t, s, `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-11-25","clientInfo":{"name":"tester"}}}`)
	if resp.Error != nil {
		t.Fatalf("unexpected error: %+v", resp.Error)
	}
	var result InitializeResult
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		t.Fatal(err)
	}
	if result.ProtocolVersion != ProtocolVersion20251125 {
		t.Errorf("protocolVersion = %q", result.ProtocolVersion)
	}
	if result.Capabilities.Tools == nil || result.Capabilities.Resources == nil {
		t.Errorf("expected tools+resources capabilities, got %+v", result.Capabilities)
	}
}

func TestInitializeRejectsUnsupportedVersion(t *testing.T) {
	s := testServer()
	resp := mustHandle(t, s, `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"1999-01-01","clientInfo":{"name":"tester"}}}`)
	if resp.Error == nil {
		t.Fatal("expected an error for unsupported protocol version")
	}
	if resp.Error.Code != CodeInvalidParams {
		t.Errorf("code = %d, want %d", resp.Error.Code, CodeInvalidParams)
	}
}

func TestNotificationsInitializedNoResponse(t *testing.T) {
	s := testServer()
	_, has := s.HandleMessage(context.Background(), []byte(`{"jsonrpc":"2.0","method":"notifications/initialized"}`))
	if has {
		t.Fatal("expected no response for a notification")
	}
}

func TestPing(t *testing.T) {
	s := testServer()
	resp := mustHandle(t, s, `{"jsonrpc":"2.0","id":"p1","method":"ping"}`)
	if resp.Error != nil {
		t.Fatalf("unexpected error: %+v", resp.Error)
	}
}

func TestToolsList(t *testing.T) {
	s := testServer()
	resp := mustHandle(t, s, `{"jsonrpc":"2.0","id":2,"method":"tools/list"}`)
	if resp.Error != nil {
		t.Fatalf("unexpected error: %+v", resp.Error)
	}
	var out struct {
		Tools []ToolDefinition `json:"tools"`
	}
	if err := json.Unmarshal(resp.Result, &out); err != nil {
		t.Fatal(err)
	}
	if len(out.Tools) != 2 {
		t.Fatalf("expected 2 tools, got %d", len(out.Tools))
	}
}

func TestToolsCallSuccess(t *testing.T) {
	s := testServer()
	resp := mustHandle(t, s, `{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"echo","arguments":{"x":1}}}`)
	if resp.Error != nil {
		t.Fatalf("unexpected error: %+v", resp.Error)
	}
	var result ToolCallResult
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Errorf("expected success, got isError result")
	}
	if len(result.StructuredContent) == 0 {
		t.Error("expected structuredContent to be populated")
	}
}

func TestToolsCallUnknownTool(t *testing.T) {
	s := testServer()
	resp := mustHandle(t, s, `{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"nope","arguments":{}}}`)
	if resp.Error == nil {
		t.Fatal("expected a JSON-RPC error for an unknown tool")
	}
	if resp.Error.Code != CodeInvalidParams {
		t.Errorf("code = %d, want %d", resp.Error.Code, CodeInvalidParams)
	}
}

func TestToolsCallHandlerErrorBecomesIsError(t *testing.T) {
	s := testServer()
	resp := mustHandle(t, s, `{"jsonrpc":"2.0","id":5,"method":"tools/call","params":{"name":"boom","arguments":{}}}`)
	if resp.Error != nil {
		t.Fatalf("handler errors should not become JSON-RPC errors, got %+v", resp.Error)
	}
	var result ToolCallResult
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		t.Fatal(err)
	}
	if !result.IsError {
		t.Error("expected isError:true")
	}
}

func TestResourceTemplatesList(t *testing.T) {
	s := testServer()
	resp := mustHandle(t, s, `{"jsonrpc":"2.0","id":6,"method":"resources/templates/list"}`)
	if resp.Error != nil {
		t.Fatalf("unexpected error: %+v", resp.Error)
	}
	var out struct {
		ResourceTemplates []ResourceTemplate `json:"resourceTemplates"`
	}
	if err := json.Unmarshal(resp.Result, &out); err != nil {
		t.Fatal(err)
	}
	if len(out.ResourceTemplates) != 1 {
		t.Fatalf("expected 1 template, got %d", len(out.ResourceTemplates))
	}
}

func TestResourcesReadSuccess(t *testing.T) {
	s := testServer()
	resp := mustHandle(t, s, `{"jsonrpc":"2.0","id":7,"method":"resources/read","params":{"uri":"piper://projects/p1/widgets/w1"}}`)
	if resp.Error != nil {
		t.Fatalf("unexpected error: %+v", resp.Error)
	}
}

func TestResourcesReadProtocolError(t *testing.T) {
	s := testServer()
	resp := mustHandle(t, s, `{"jsonrpc":"2.0","id":8,"method":"resources/read","params":{"uri":"piper://projects/p1/widgets/missing"}}`)
	if resp.Error == nil {
		t.Fatal("expected a JSON-RPC error")
	}
	if resp.Error.Code != CodeInvalidParams {
		t.Errorf("code = %d, want %d", resp.Error.Code, CodeInvalidParams)
	}
}

func TestUnknownMethod(t *testing.T) {
	s := testServer()
	resp := mustHandle(t, s, `{"jsonrpc":"2.0","id":9,"method":"nope/nope"}`)
	if resp.Error == nil || resp.Error.Code != CodeMethodNotFound {
		t.Fatalf("expected method-not-found, got %+v", resp.Error)
	}
}

func TestBatchRequest(t *testing.T) {
	s := testServer()
	out, has := s.HandleMessage(context.Background(), []byte(`[
		{"jsonrpc":"2.0","id":1,"method":"ping"},
		{"jsonrpc":"2.0","id":2,"method":"tools/list"}
	]`))
	if !has {
		t.Fatal("expected a response")
	}
	var resps []Response
	if err := json.Unmarshal(out, &resps); err != nil {
		t.Fatalf("expected a batch array response: %v (%s)", err, out)
	}
	if len(resps) != 2 {
		t.Fatalf("expected 2 responses, got %d", len(resps))
	}
}

func TestBatchOfOnlyNotificationsHasNoResponse(t *testing.T) {
	s := testServer()
	_, has := s.HandleMessage(context.Background(), []byte(`[{"jsonrpc":"2.0","method":"notifications/initialized"}]`))
	if has {
		t.Fatal("expected no response for an all-notification batch")
	}
}

func TestParseError(t *testing.T) {
	s := testServer()
	out, has := s.HandleMessage(context.Background(), []byte(`not json`))
	if !has {
		t.Fatal("expected a parse-error response")
	}
	var resp Response
	if err := json.Unmarshal(out, &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Error == nil || resp.Error.Code != CodeParseError {
		t.Fatalf("expected parse error, got %+v", resp.Error)
	}
}
