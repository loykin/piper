package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	piperMCP "github.com/loykin/piper/pkg/mcp"
	"github.com/loykin/piper/pkg/notebook"
	"github.com/loykin/piper/pkg/notebook/execution"
	"github.com/loykin/piper/pkg/notebook/execution/jupyter"
	"github.com/loykin/piper/pkg/project"
	"github.com/loykin/piper/pkg/security"
)

// --- fixtures ---------------------------------------------------------------

const (
	tokenFixture    = "super-secret-jupyter-token-xyz789"
	endpointFixture = "http://127.0.0.1:38888"
	workdirFixture  = "/var/piper/secret-workdir/nb1"
	pidFixture      = 424242
)

type testFixture struct {
	notebooks *fakeNotebookRepo
	execRepo  *fakeExecRepo
	gateway   *fakeGateway
	service   *execution.Service
	handler   *Handler
	router    *gin.Engine
}

func newTestFixture(t *testing.T) *testFixture {
	t.Helper()
	gin.SetMode(gin.TestMode)

	notebooks := newFakeNotebookRepo()
	execRepo := newFakeExecRepo()
	gw := newFakeGateway()

	now := time.Now().UTC()
	srv := &notebook.NotebookServer{
		ProjectID: "proj-1", Name: "nb1", Status: notebook.StatusRunning,
		Env: "python3.11", Endpoint: endpointFixture, PID: pidFixture, WorkDir: workdirFixture,
		Token: tokenFixture, RuntimeID: "rt-1", VolumeID: "vol-1", Image: "jupyter/base",
		CreatedBy: "user-1", CreatedAt: now, UpdatedAt: now,
	}
	notebooks.put(srv)

	doc := jupyter.EmptyNotebook()
	doc.AppendCodeCell("cell-1", "print('hi')")
	gw.putDoc(srv, "analysis.ipynb", doc)

	gw.putFile(srv, "data.csv", &execution.FileContent{
		Path: "data.csv", MimeType: "text/csv", Format: "text", Content: "a,b\n1,2\n", Size: 8,
	})

	exec := &execution.NotebookExecution{
		ID: "exec-1", ProjectID: "proj-1", NotebookName: "nb1", NotebookPath: "analysis.ipynb",
		ResultPath: ".piper/executions/exec-1/result.ipynb", Kind: execution.KindNotebook,
		Status: execution.StatusSucceeded, RequestedBy: "user-1", ClientID: "rest",
		QueuedAt: now, UpdatedAt: now,
	}
	execRepo.seedExecution(exec)

	resultDoc := jupyter.EmptyNotebook()
	resultDoc.AppendCodeCell("cell-1", "print('hi')")
	gw.putDoc(srv, exec.ResultPath, resultDoc)

	svc := execution.NewService(context.Background(), execution.Deps{
		Repo: execRepo, Notebooks: notebooks, Gateway: gw, Limits: execution.DefaultLimits(),
	})

	h := NewHandler(Deps{Notebooks: notebooks, Executions: svc}, Config{
		AllowedOrigins: []string{"https://trusted.example.com"},
		SessionTTL:     time.Minute,
	})

	return &testFixture{notebooks: notebooks, execRepo: execRepo, gateway: gw, service: svc, handler: h, router: newTestRouter(h, security.ProjectRoleViewer)}
}

// newTestRouter mounts h on a group that simulates serve.go's real
// project.Require(...) middleware: it injects a project.Context (with the
// given role) and an authenticated identity before dispatch, the same shape
// the real middleware produces.
func newTestRouter(h *Handler, role security.ProjectRole) *gin.Engine {
	r := gin.New()
	rg := r.Group("/api/projects/:project_id", func(c *gin.Context) {
		ctx := project.WithContext(c.Request.Context(), project.Context{ID: c.Param("project_id"), Role: role})
		ctx = security.WithIdentity(ctx, &security.Identity{ID: "user-1"})
		c.Request = c.Request.WithContext(ctx)
		c.Next()
	})
	h.RegisterRoutes(rg)
	return r
}

// --- JSON-RPC request/response helpers --------------------------------------

type rpcReq struct {
	ID     any    `json:"id,omitempty"`
	Method string `json:"method"`
	Params any    `json:"params,omitempty"`
}

type rpcResp struct {
	Result json.RawMessage `json:"result"`
	Error  *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

type callOpts struct {
	projectID       string
	sessionID       string
	protocolVersion string
	origin          string
}

func doCall(t *testing.T, r *gin.Engine, opts callOpts, req rpcReq) (*httptest.ResponseRecorder, rpcResp) {
	t.Helper()
	body := map[string]any{"jsonrpc": "2.0", "method": req.Method}
	if req.ID != nil {
		body["id"] = req.ID
	}
	if req.Params != nil {
		body["params"] = req.Params
	}
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	httpReq := httptest.NewRequest(http.MethodPost, "/api/projects/"+opts.projectID+"/mcp", bytes.NewReader(raw))
	httpReq.Header.Set("Content-Type", "application/json")
	if opts.protocolVersion != "" {
		httpReq.Header.Set("MCP-Protocol-Version", opts.protocolVersion)
	}
	if opts.sessionID != "" {
		httpReq.Header.Set(piperMCP.SessionIDHeader, opts.sessionID)
	}
	if opts.origin != "" {
		httpReq.Header.Set("Origin", opts.origin)
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httpReq)

	var resp rpcResp
	if w.Body.Len() > 0 {
		// A transport-level rejection (RBAC floor, Origin/Host policy,
		// protocol-version negotiation, missing/unknown session) responds
		// with the plain {"error":"..."} REST envelope rather than a
		// JSON-RPC envelope, since it happens before JSON-RPC dispatch even
		// starts — those tests assert on the HTTP status code instead, so a
		// shape mismatch here is expected and not itself a failure.
		_ = json.Unmarshal(w.Body.Bytes(), &resp)
	}
	return w, resp
}

func mustInitialize(t *testing.T, r *gin.Engine, projectID string) (sessionID string, protocolVersion string) {
	t.Helper()
	w, resp := doCall(t, r, callOpts{projectID: projectID, protocolVersion: piperMCP.ProtocolVersion20251125}, rpcReq{
		ID: 1, Method: "initialize",
		Params: map[string]any{"protocolVersion": piperMCP.ProtocolVersion20251125, "clientInfo": map[string]string{"name": "test-client"}},
	})
	if w.Code != http.StatusOK {
		t.Fatalf("initialize status = %d, body = %s", w.Code, w.Body.String())
	}
	if resp.Error != nil {
		t.Fatalf("initialize error: %+v", resp.Error)
	}
	sid := w.Header().Get(piperMCP.SessionIDHeader)
	if sid == "" {
		t.Fatal("expected a Mcp-Session-Id response header")
	}
	var result piperMCP.InitializeResult
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		t.Fatal(err)
	}
	return sid, result.ProtocolVersion
}

// --- full flow: initialize -> notifications/initialized -> tools/list ->
// tools/call x6 -> resources/templates/list -> resources/read x4 ----------

func TestFullMCPFlow(t *testing.T) {
	fx := newTestFixture(t)
	r := fx.router

	sessionID, protoVersion := mustInitialize(t, r, "proj-1")
	if protoVersion != piperMCP.ProtocolVersion20251125 {
		t.Errorf("protocolVersion = %q", protoVersion)
	}

	opts := callOpts{projectID: "proj-1", sessionID: sessionID, protocolVersion: piperMCP.ProtocolVersion20251125}

	// notifications/initialized — a notification, no id, expect 202 + empty body.
	notifyReq := httptest.NewRequest(http.MethodPost, "/api/projects/proj-1/mcp", strings.NewReader(`{"jsonrpc":"2.0","method":"notifications/initialized"}`))
	notifyReq.Header.Set("Content-Type", "application/json")
	notifyReq.Header.Set("MCP-Protocol-Version", piperMCP.ProtocolVersion20251125)
	notifyReq.Header.Set(piperMCP.SessionIDHeader, sessionID)
	notifyW := httptest.NewRecorder()
	r.ServeHTTP(notifyW, notifyReq)
	if notifyW.Code != http.StatusAccepted {
		t.Fatalf("notifications/initialized status = %d", notifyW.Code)
	}
	if notifyW.Body.Len() != 0 {
		t.Errorf("expected empty body for a notification, got %q", notifyW.Body.String())
	}

	// tools/list — assert exactly the 6 Phase 2 tools, and none of the
	// Phase 3 execution tools the task explicitly forbids implementing.
	var allBodies [][]byte
	w, resp := doCall(t, r, opts, rpcReq{ID: 2, Method: "tools/list"})
	allBodies = append(allBodies, w.Body.Bytes())
	if resp.Error != nil {
		t.Fatalf("tools/list error: %+v", resp.Error)
	}
	var toolsOut struct {
		Tools []piperMCP.ToolDefinition `json:"tools"`
	}
	if err := json.Unmarshal(resp.Result, &toolsOut); err != nil {
		t.Fatal(err)
	}
	wantTools := map[string]bool{
		"piper_list_notebook_servers": false,
		"piper_get_notebook_server":   false,
		"piper_list_notebook_files":   false,
		"piper_read_notebook":         false,
		"piper_get_execution":         false,
		"piper_list_executions":       false,
	}
	phase3Tools := []string{
		"piper_start_notebook_server", "piper_create_kernel_session",
		"piper_execute_notebook", "piper_execute_cell",
		"piper_cancel_execution", "piper_close_kernel_session",
	}
	for _, td := range toolsOut.Tools {
		if _, known := wantTools[td.Name]; known {
			wantTools[td.Name] = true
		}
		for _, forbidden := range phase3Tools {
			if td.Name == forbidden {
				t.Errorf("Phase 3 tool %q must not be implemented in Phase 2", td.Name)
			}
		}
		if !td.Annotations.ReadOnlyHint {
			t.Errorf("tool %q: expected readOnlyHint=true", td.Name)
		}
		if td.Annotations.OpenWorldHint {
			t.Errorf("tool %q: expected openWorldHint=false", td.Name)
		}
	}
	if len(toolsOut.Tools) != 6 {
		t.Errorf("expected exactly 6 tools, got %d", len(toolsOut.Tools))
	}
	for name, found := range wantTools {
		if !found {
			t.Errorf("expected tool %q to be present", name)
		}
	}

	// tools/call for each of the 6 tools.
	toolCalls := []struct {
		name string
		args map[string]any
	}{
		{"piper_list_notebook_servers", map[string]any{}},
		{"piper_get_notebook_server", map[string]any{"name": "nb1"}},
		{"piper_list_notebook_files", map[string]any{"name": "nb1"}},
		{"piper_read_notebook", map[string]any{"name": "nb1", "path": "analysis.ipynb"}},
		{"piper_get_execution", map[string]any{"execution_id": "exec-1"}},
		{"piper_list_executions", map[string]any{"name": "nb1"}},
	}
	for i, tc := range toolCalls {
		w, resp := doCall(t, r, opts, rpcReq{ID: 10 + i, Method: "tools/call", Params: map[string]any{"name": tc.name, "arguments": tc.args}})
		allBodies = append(allBodies, w.Body.Bytes())
		if resp.Error != nil {
			t.Fatalf("tools/call %s error: %+v", tc.name, resp.Error)
		}
		var result piperMCP.ToolCallResult
		if err := json.Unmarshal(resp.Result, &result); err != nil {
			t.Fatal(err)
		}
		if result.IsError {
			t.Errorf("tools/call %s: unexpected isError result: %+v", tc.name, result.Content)
		}
		if len(result.StructuredContent) == 0 {
			t.Errorf("tools/call %s: expected structuredContent", tc.name)
		}
	}

	// resources/templates/list — assert the 4 documented URI patterns.
	w, resp = doCall(t, r, opts, rpcReq{ID: 20, Method: "resources/templates/list"})
	allBodies = append(allBodies, w.Body.Bytes())
	if resp.Error != nil {
		t.Fatalf("resources/templates/list error: %+v", resp.Error)
	}
	var templatesOut struct {
		ResourceTemplates []piperMCP.ResourceTemplate `json:"resourceTemplates"`
	}
	if err := json.Unmarshal(resp.Result, &templatesOut); err != nil {
		t.Fatal(err)
	}
	if len(templatesOut.ResourceTemplates) != 4 {
		t.Errorf("expected 4 resource templates, got %d", len(templatesOut.ResourceTemplates))
	}

	// resources/read for one of each of the 4 URI patterns.
	resourceURIs := []string{
		documentURI("proj-1", "nb1", "analysis.ipynb"),
		executionURI("proj-1", "exec-1"),
		executionResultURI("proj-1", "exec-1"),
		fileURI("proj-1", "nb1", "data.csv"),
	}
	for i, uri := range resourceURIs {
		w, resp := doCall(t, r, opts, rpcReq{ID: 30 + i, Method: "resources/read", Params: map[string]any{"uri": uri}})
		allBodies = append(allBodies, w.Body.Bytes())
		if resp.Error != nil {
			t.Fatalf("resources/read %s error: %+v", uri, resp.Error)
		}
		var readOut piperMCP.ReadResourceResult
		if err := json.Unmarshal(resp.Result, &readOut); err != nil {
			t.Fatal(err)
		}
		if len(readOut.Contents) != 1 {
			t.Fatalf("resources/read %s: expected 1 content entry, got %d", uri, len(readOut.Contents))
		}
	}

	// Token/endpoint/work_dir non-leakage: none of the raw response bytes
	// collected above may contain the secret/internal fixture values
	// (design doc §3.1/§4.2/§8.3's core security requirement).
	for _, secret := range []string{tokenFixture, endpointFixture, workdirFixture} {
		for _, b := range allBodies {
			if bytes.Contains(b, []byte(secret)) {
				t.Errorf("response leaked secret/internal value %q: %s", secret, b)
			}
		}
	}
}

// TestDTOsDoNotLeakSecrets is a direct, HTTP-independent check on the DTO
// mapping functions themselves (design doc §17.1: "token/endpoint/work_dir가
// public DTO에 없는지 확인").
func TestDTOsDoNotLeakSecrets(t *testing.T) {
	srv := &notebook.NotebookServer{
		ProjectID: "proj-1", Name: "nb1", Status: notebook.StatusRunning,
		Endpoint: endpointFixture, PID: pidFixture, WorkDir: workdirFixture, Token: tokenFixture,
	}
	raw, err := json.Marshal(NewNotebookServerPublic(srv))
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{tokenFixture, endpointFixture, workdirFixture} {
		if bytes.Contains(raw, []byte(secret)) {
			t.Errorf("NotebookServerPublic leaked %q: %s", secret, raw)
		}
	}
	if strings.Contains(string(raw), "\"pid\"") || strings.Contains(string(raw), "\"work_dir\"") || strings.Contains(string(raw), "\"endpoint\"") || strings.Contains(string(raw), "\"token\"") {
		t.Errorf("NotebookServerPublic must not have pid/work_dir/endpoint/token fields at all: %s", raw)
	}
}

// TestResourceReadProjectMismatchRejected proves a resource URI naming a
// different project than the endpoint's own is rejected (design doc §8.3:
// "URI의 project는 현재 endpoint project와 반드시 일치해야 한다").
func TestResourceReadProjectMismatchRejected(t *testing.T) {
	fx := newTestFixture(t)
	sessionID, _ := mustInitialize(t, fx.router, "proj-1")
	opts := callOpts{projectID: "proj-1", sessionID: sessionID, protocolVersion: piperMCP.ProtocolVersion20251125}

	_, resp := doCall(t, fx.router, opts, rpcReq{ID: 1, Method: "resources/read", Params: map[string]any{
		"uri": documentURI("some-other-project", "nb1", "analysis.ipynb"),
	}})
	if resp.Error == nil {
		t.Fatal("expected an error for a cross-project resource URI")
	}
	if resp.Error.Code != piperMCP.CodeInvalidParams {
		t.Errorf("code = %d, want %d", resp.Error.Code, piperMCP.CodeInvalidParams)
	}
}

// TestPathTraversalRejected proves a ".." escape is rejected both as a tool
// argument and inside a resource URI (design doc §7.1/§8.3, mirroring
// WorkspaceReader's own rules via notebook.CleanWorkspacePath).
func TestPathTraversalRejected(t *testing.T) {
	fx := newTestFixture(t)
	sessionID, _ := mustInitialize(t, fx.router, "proj-1")
	opts := callOpts{projectID: "proj-1", sessionID: sessionID, protocolVersion: piperMCP.ProtocolVersion20251125}

	t.Run("tool argument", func(t *testing.T) {
		_, resp := doCall(t, fx.router, opts, rpcReq{ID: 1, Method: "tools/call", Params: map[string]any{
			"name": "piper_list_notebook_files", "arguments": map[string]any{"name": "nb1", "path": "../../etc"},
		}})
		if resp.Error != nil {
			t.Fatalf("expected a normal (isError) tool result, not a protocol error: %+v", resp.Error)
		}
		var result piperMCP.ToolCallResult
		if err := json.Unmarshal(resp.Result, &result); err != nil {
			t.Fatal(err)
		}
		if !result.IsError {
			t.Error("expected isError:true for a path-traversal argument")
		}
	})

	t.Run("resource uri", func(t *testing.T) {
		_, resp := doCall(t, fx.router, opts, rpcReq{ID: 2, Method: "resources/read", Params: map[string]any{
			"uri": documentURI("proj-1", "nb1", "../secrets/x.ipynb"),
		}})
		if resp.Error == nil {
			t.Fatal("expected an error for a path-traversal resource uri")
		}
	})

	t.Run("absolute path tool argument", func(t *testing.T) {
		_, resp := doCall(t, fx.router, opts, rpcReq{ID: 3, Method: "tools/call", Params: map[string]any{
			"name": "piper_read_notebook", "arguments": map[string]any{"name": "nb1", "path": "/etc/passwd"},
		}})
		if resp.Error != nil {
			t.Fatalf("expected a normal (isError) tool result, not a protocol error: %+v", resp.Error)
		}
		var result piperMCP.ToolCallResult
		if err := json.Unmarshal(resp.Result, &result); err != nil {
			t.Fatal(err)
		}
		if !result.IsError {
			t.Error("expected isError:true for an absolute-path argument")
		}
	})
}

// TestRBACRejectsBelowViewer proves a request context with no/below-viewer
// project role is rejected before reaching any tool (design doc §9.1).
func TestRBACRejectsBelowViewer(t *testing.T) {
	fx := newTestFixture(t)
	// security.ProjectRole's zero value (no role resolved at all) — the
	// lowest possible value, below ProjectRoleViewer.
	r := newTestRouter(fx.handler, security.ProjectRole(0))

	w, _ := doCall(t, r, callOpts{projectID: "proj-1"}, rpcReq{ID: 1, Method: "initialize", Params: map[string]any{
		"protocolVersion": piperMCP.ProtocolVersion20251125, "clientInfo": map[string]string{"name": "test"},
	}})
	if w.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", w.Code)
	}
}

// TestOriginRejected / TestHostRejected exercise the DNS-rebinding defense.
func TestOriginRejected(t *testing.T) {
	fx := newTestFixture(t)
	req := httptest.NewRequest(http.MethodPost, "/api/projects/proj-1/mcp", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-11-25","clientInfo":{"name":"t"}}}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", "https://evil.example.com")
	w := httptest.NewRecorder()
	fx.router.ServeHTTP(w, req)
	if w.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403, body=%s", w.Code, w.Body.String())
	}
}

func TestNoOriginAllowedForNonBrowserClient(t *testing.T) {
	fx := newTestFixture(t)
	req := httptest.NewRequest(http.MethodPost, "/api/projects/proj-1/mcp", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-11-25","clientInfo":{"name":"t"}}}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	fx.router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (no Origin header should pass), body=%s", w.Code, w.Body.String())
	}
}

// TestProtocolVersionRejected proves an unsupported/missing
// MCP-Protocol-Version header is rejected before any dispatch happens.
func TestProtocolVersionRejected(t *testing.T) {
	fx := newTestFixture(t)
	sessionID, _ := mustInitialize(t, fx.router, "proj-1")

	t.Run("unsupported version", func(t *testing.T) {
		w, _ := doCall(t, fx.router, callOpts{projectID: "proj-1", sessionID: sessionID, protocolVersion: "1999-01-01"}, rpcReq{ID: 1, Method: "tools/list"})
		if w.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400", w.Code)
		}
	})

	t.Run("missing header on non-initialize request", func(t *testing.T) {
		w, _ := doCall(t, fx.router, callOpts{projectID: "proj-1", sessionID: sessionID}, rpcReq{ID: 1, Method: "tools/list"})
		if w.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400", w.Code)
		}
	})
}

// TestSessionRequiredAndValidated covers missing/unknown session id
// handling for non-initialize requests.
func TestSessionRequiredAndValidated(t *testing.T) {
	fx := newTestFixture(t)

	t.Run("missing session id", func(t *testing.T) {
		w, _ := doCall(t, fx.router, callOpts{projectID: "proj-1", protocolVersion: piperMCP.ProtocolVersion20251125}, rpcReq{ID: 1, Method: "tools/list"})
		if w.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400", w.Code)
		}
	})

	t.Run("unknown session id", func(t *testing.T) {
		w, _ := doCall(t, fx.router, callOpts{projectID: "proj-1", protocolVersion: piperMCP.ProtocolVersion20251125, sessionID: "does-not-exist"}, rpcReq{ID: 1, Method: "tools/list"})
		if w.Code != http.StatusNotFound {
			t.Fatalf("status = %d, want 404", w.Code)
		}
	})

	t.Run("session id from a different project is rejected", func(t *testing.T) {
		sessionID, _ := mustInitialize(t, fx.router, "proj-1")
		w, _ := doCall(t, fx.router, callOpts{projectID: "proj-2", protocolVersion: piperMCP.ProtocolVersion20251125, sessionID: sessionID}, rpcReq{ID: 1, Method: "tools/list"})
		if w.Code != http.StatusNotFound {
			t.Fatalf("status = %d, want 404", w.Code)
		}
	})
}

// TestSessionExpiry proves an issued session stops working once its TTL
// elapses.
func TestSessionExpiry(t *testing.T) {
	gin.SetMode(gin.TestMode)
	notebooks := newFakeNotebookRepo()
	execRepo := newFakeExecRepo()
	gw := newFakeGateway()
	svc := execution.NewService(context.Background(), execution.Deps{Repo: execRepo, Notebooks: notebooks, Gateway: gw, Limits: execution.DefaultLimits()})
	h := NewHandler(Deps{Notebooks: notebooks, Executions: svc}, Config{AllowedOrigins: []string{"https://trusted.example.com"}, SessionTTL: 5 * time.Millisecond})
	r := newTestRouter(h, security.ProjectRoleViewer)

	sessionID, _ := mustInitialize(t, r, "proj-1")
	time.Sleep(20 * time.Millisecond)
	w, _ := doCall(t, r, callOpts{projectID: "proj-1", protocolVersion: piperMCP.ProtocolVersion20251125, sessionID: sessionID}, rpcReq{ID: 1, Method: "tools/list"})
	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 (expired session)", w.Code)
	}
}

// TestGetNotebookServerNotFoundIsModelVisible proves a domain "not found"
// becomes an isError tool result, not a transport failure.
func TestGetNotebookServerNotFoundIsModelVisible(t *testing.T) {
	fx := newTestFixture(t)
	sessionID, _ := mustInitialize(t, fx.router, "proj-1")
	opts := callOpts{projectID: "proj-1", sessionID: sessionID, protocolVersion: piperMCP.ProtocolVersion20251125}

	_, resp := doCall(t, fx.router, opts, rpcReq{ID: 1, Method: "tools/call", Params: map[string]any{
		"name": "piper_get_notebook_server", "arguments": map[string]any{"name": "does-not-exist"},
	}})
	if resp.Error != nil {
		t.Fatalf("unexpected protocol error: %+v", resp.Error)
	}
	var result piperMCP.ToolCallResult
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		t.Fatal(err)
	}
	if !result.IsError {
		t.Error("expected isError:true for a not-found notebook server")
	}
}
