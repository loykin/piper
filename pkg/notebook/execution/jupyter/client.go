package jupyter

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"strings"
	"time"
)

// BuildBaseURL constructs the Jupyter Server base URL Piper's own reverse
// proxy already expects the Jupyter process to be configured with (see
// pkg/notebook/dispatch/localdriver.Driver's baseURL and
// pkg/notebook/handler.go's proxyNotebook). endpoint is the bare
// NotebookServer.Endpoint (scheme://host:port with no path); appending
// "/api/..." directly to it would bypass Jupyter's own --ServerApp.base_url
// and 404 (docs/jupyter-mcp-execution.md §4.2) — every REST and WebSocket
// call this package makes MUST go through this one builder instead of
// reconstructing the prefix inline.
func BuildBaseURL(endpoint, projectID, notebookName string) string {
	return strings.TrimRight(endpoint, "/") + "/projects/" + projectID + "/notebooks/" + notebookName + "/proxy"
}

// BuildWebSocketURL builds the kernel channels WebSocket URL for kernelID,
// reusing BuildBaseURL for the scheme/host/path-prefix and swapping the
// scheme to ws/wss.
func BuildWebSocketURL(endpoint, projectID, notebookName, kernelID, sessionID string) (string, error) {
	base := BuildBaseURL(endpoint, projectID, notebookName)
	u, err := url.Parse(base)
	if err != nil {
		return "", fmt.Errorf("jupyter: invalid endpoint")
	}
	switch u.Scheme {
	case "https":
		u.Scheme = "wss"
	default:
		u.Scheme = "ws"
	}
	u.Path = path.Join(u.Path, "api", "kernels", kernelID, "channels")
	q := u.Query()
	q.Set("session_id", sessionID)
	u.RawQuery = q.Encode()
	return u.String(), nil
}

// Client is a thin REST client for the Jupyter Server Contents, Sessions,
// and Kernels APIs. It is constructed fresh per call site (see
// pkg/notebook/execution/gateway.go) from the target NotebookServer's
// Endpoint/Token, which differ per notebook — it is not a long-lived shared
// client. Token is held only in memory for the lifetime of one Gateway call
// and is sent as an Authorization header (never a query parameter, so it
// never lands in access logs) — see design doc §4.2.
type Client struct {
	baseURL string
	token   string
	http    *http.Client
}

// NewClient constructs a Client for one Jupyter server. baseURL should come
// from BuildBaseURL.
func NewClient(baseURL, token string) *Client {
	return &Client{
		baseURL: strings.TrimRight(baseURL, "/"),
		token:   token,
		http:    &http.Client{Timeout: 30 * time.Second},
	}
}

// WithHTTPClient overrides the underlying *http.Client (tests / custom timeouts).
func (c *Client) WithHTTPClient(h *http.Client) *Client {
	c.http = h
	return c
}

// opaqueError wraps a request failure without leaking the request URL
// (which carries the notebook path and, for query-based fallbacks, could
// carry secrets) or the token. Callers that need more detail should log the
// underlying error server-side, never pass it back to REST/MCP callers
// verbatim (docs/jupyter-mcp-execution.md §11.3).
type opaqueError struct {
	op     string
	status int
	err    error
}

func (e *opaqueError) Error() string {
	if e.status > 0 {
		return fmt.Sprintf("jupyter: %s failed (status %d)", e.op, e.status)
	}
	return fmt.Sprintf("jupyter: %s failed: %v", e.op, e.err)
}

func (e *opaqueError) Unwrap() error { return e.err }

// StatusCode returns the upstream HTTP status code, or 0 if the request
// never got a response (network error).
func (e *opaqueError) StatusCode() int { return e.status }

// AsStatusError extracts the upstream HTTP status from err, if it originated
// from this client.
func AsStatusError(err error) (int, bool) {
	var oe *opaqueError
	if ok := asOpaque(err, &oe); ok {
		return oe.status, true
	}
	return 0, false
}

func asOpaque(err error, target **opaqueError) bool {
	for err != nil {
		if oe, ok := err.(*opaqueError); ok {
			*target = oe
			return true
		}
		u, ok := err.(interface{ Unwrap() error })
		if !ok {
			return false
		}
		err = u.Unwrap()
	}
	return false
}

func (c *Client) do(ctx context.Context, method, p string, body any, out any) error {
	var reader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("jupyter: encode request: %w", err)
		}
		reader = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+p, reader)
	if err != nil {
		return &opaqueError{op: method + " " + p, err: err}
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.token != "" {
		req.Header.Set("Authorization", "token "+c.token)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return &opaqueError{op: method + " " + p, err: err}
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode >= 300 {
		return &opaqueError{op: method + " " + p, status: resp.StatusCode}
	}
	if out == nil {
		_, _ = io.Copy(io.Discard, resp.Body)
		return nil
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return &opaqueError{op: method + " " + p, err: err}
	}
	return nil
}

// ContentModel mirrors Jupyter's Contents API resource model
// (https://jupyter-server.readthedocs.io/en/latest/developers/rest-api.html).
type ContentModel struct {
	Name         string          `json:"name"`
	Path         string          `json:"path"`
	Type         string          `json:"type"` // "directory" | "notebook" | "file"
	Size         *int64          `json:"size,omitempty"`
	LastModified string          `json:"last_modified,omitempty"`
	Created      string          `json:"created,omitempty"`
	Format       string          `json:"format,omitempty"`
	Content      json.RawMessage `json:"content,omitempty"`
	Writable     bool            `json:"writable,omitempty"`
}

func contentsPath(p string) string {
	return "/api/contents/" + strings.TrimLeft(p, "/")
}

// GetContents fetches metadata (and, for a "notebook"/"file" type, content)
// at path. An empty path lists the workspace root directory.
func (c *Client) GetContents(ctx context.Context, path string) (*ContentModel, error) {
	var out ContentModel
	if err := c.do(ctx, http.MethodGet, contentsPath(path), nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// PutContents creates or overwrites the notebook document at path.
func (c *Client) PutContents(ctx context.Context, path string, model ContentModel) error {
	return c.do(ctx, http.MethodPut, contentsPath(path), model, nil)
}

// SessionKernel is the embedded kernel descriptor of a Sessions API resource.
type SessionKernel struct {
	ID    string `json:"id,omitempty"`
	Name  string `json:"name,omitempty"`
	State string `json:"execution_state,omitempty"`
}

// SessionModel mirrors the Jupyter Sessions API resource model.
type SessionModel struct {
	ID     string        `json:"id,omitempty"`
	Path   string        `json:"path"`
	Name   string        `json:"name,omitempty"`
	Type   string        `json:"type,omitempty"`
	Kernel SessionKernel `json:"kernel"`
}

// CreateSession starts a new kernel session bound to notebookPath.
func (c *Client) CreateSession(ctx context.Context, notebookPath, kernelName string) (*SessionModel, error) {
	req := SessionModel{
		Path: notebookPath,
		Name: path.Base(notebookPath),
		Type: "notebook",
		Kernel: SessionKernel{
			Name: kernelName,
		},
	}
	var out SessionModel
	if err := c.do(ctx, http.MethodPost, "/api/sessions", req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// GetSession fetches the current state of a session by ID.
func (c *Client) GetSession(ctx context.Context, id string) (*SessionModel, error) {
	var out SessionModel
	if err := c.do(ctx, http.MethodGet, "/api/sessions/"+url.PathEscape(id), nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// DeleteSession terminates a session (and, per Jupyter Server default
// behavior, its kernel unless another session still references it).
func (c *Client) DeleteSession(ctx context.Context, id string) error {
	return c.do(ctx, http.MethodDelete, "/api/sessions/"+url.PathEscape(id), nil, nil)
}

// InterruptKernel sends SIGINT to the kernel's execution — the graceful
// cancellation path used before Piper considers restarting it (§6.3).
func (c *Client) InterruptKernel(ctx context.Context, kernelID string) error {
	return c.do(ctx, http.MethodPost, "/api/kernels/"+url.PathEscape(kernelID)+"/interrupt", nil, nil)
}

// RestartKernel restarts a possibly-wedged kernel in place, keeping its ID.
func (c *Client) RestartKernel(ctx context.Context, kernelID string) error {
	return c.do(ctx, http.MethodPost, "/api/kernels/"+url.PathEscape(kernelID)+"/restart", nil, nil)
}

// GetKernel fetches kernel status (used by recovery to check whether a
// kernel from before a Piper restart is still alive).
func (c *Client) GetKernel(ctx context.Context, kernelID string) (*SessionKernel, error) {
	var out SessionKernel
	if err := c.do(ctx, http.MethodGet, "/api/kernels/"+url.PathEscape(kernelID), nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}
