package jupyter

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"path"
	"strings"
	"sync"
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

	// xsrfOnce/xsrfToken/xsrfErr cache the result of the one-time XSRF
	// priming request every non-GET call needs — see ensureXSRFToken's doc
	// comment for why this exists at all. sync.Once rather than a plain
	// mutex+bool because ensureXSRFToken can be called concurrently by
	// nothing today (Client isn't shared across goroutines per the doc
	// comment above), but costs nothing to make safe against that changing.
	xsrfOnce  sync.Once
	xsrfToken string
	xsrfErr   error
}

// NewClient constructs a Client for one Jupyter server. baseURL should come
// from BuildBaseURL. A cookiejar.Jar is always attached (overriding any Jar
// on a caller-supplied *http.Client via WithHTTPClient too) — ensureXSRFToken
// depends on Go's http.Client automatically capturing and replaying the
// _xsrf cookie Jupyter Server sets, which requires a non-nil Jar.
func NewClient(baseURL, token string) *Client {
	jar, _ := cookiejar.New(nil) // cookiejar.New never actually errors with a nil Options
	return &Client{
		baseURL: strings.TrimRight(baseURL, "/"),
		token:   token,
		http:    &http.Client{Timeout: 30 * time.Second, Jar: jar},
	}
}

// WithHTTPClient overrides the underlying *http.Client (tests / custom
// timeouts) — attaching a cookiejar.Jar if h doesn't already have one, for
// the same reason NewClient always attaches one (see its doc comment).
func (c *Client) WithHTTPClient(h *http.Client) *Client {
	if h.Jar == nil {
		if jar, err := cookiejar.New(nil); err == nil {
			h.Jar = jar
		}
	}
	c.http = h
	return c
}

// ensureXSRFToken performs Jupyter Server's XSRF handshake exactly once per
// Client: Tornado (which jupyter_server is built on) rejects every
// non-idempotent request — POST/PUT/PATCH/DELETE, i.e. every state-changing
// call this package makes: CreateSession, DeleteSession, InterruptKernel,
// RestartKernel, PutContents — with 403 "'_xsrf' argument missing" unless
// the caller first obtains the _xsrf cookie Jupyter sets on an HTML page
// response and echoes its value back as the X-XSRFToken header on the
// state-changing request. This applies even when Jupyter's own token auth
// is disabled (Piper's baremetal/docker drivers always launch Jupyter with
// --ServerApp.token=, since Piper's reverse proxy is the actual access
// boundary — see pkg/notebook/dispatch/localdriver) — XSRF protection is a
// separate, unconditional Tornado default (verified live against a real
// jupyter_server: a bare `GET /api/status` does NOT set the cookie, since
// pure JSON API handlers don't render the template that triggers it, but
// `GET /lab` — the page Piper's own driver always has available, since it
// launches Jupyter via `jupyter lab` — does).
//
// The GET-then-cache is safe to do exactly once because the cookie is
// static for a server process's lifetime; it does not need to be
// refreshed per request, and Client's own doc comment already establishes
// it is short-lived (constructed fresh per Gateway call), so there is no
// long-lived-staleness concern here either.
func (c *Client) ensureXSRFToken(ctx context.Context) (string, error) {
	c.xsrfOnce.Do(func() {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/lab", nil)
		if err != nil {
			c.xsrfErr = err
			return
		}
		if c.token != "" {
			req.Header.Set("Authorization", "token "+c.token)
		}
		resp, err := c.http.Do(req)
		if err != nil {
			c.xsrfErr = err
			return
		}
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()

		// Deliberately req.URL here, not c.baseURL: Jupyter Server sets the
		// _xsrf cookie's Path to its configured --ServerApp.base_url, which
		// always has a *trailing* slash (see
		// pkg/notebook/dispatch/localdriver.Driver's baseURL construction —
		// ".../proxy/"). RFC 6265's cookie-path-match requires the cookie's
		// Path to be a literal prefix of the request path being matched.
		// c.baseURL has that trailing slash trimmed off in NewClient
		// (".../proxy", no slash), so http.CookieJar.Cookies(base) with
		// that trimmed URL fails the prefix check by exactly one character
		// and silently returns no cookies — confirmed live against a real
		// jupyter_server (this was the second bug live testing found, after
		// the missing XSRF header entirely). req.URL (".../proxy/lab") does
		// have the base_url as a genuine prefix, so querying against the
		// request actually made is both correct and simpler than
		// reconstructing a trailing-slash variant of c.baseURL by hand.
		for _, ck := range c.http.Jar.Cookies(req.URL) {
			if ck.Name == "_xsrf" {
				c.xsrfToken = ck.Value
				return
			}
		}
		c.xsrfErr = fmt.Errorf("jupyter: server did not set an _xsrf cookie")
	})
	return c.xsrfToken, c.xsrfErr
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
	// Every non-idempotent request needs the X-XSRFToken header — see
	// ensureXSRFToken's doc comment. Resolved before building req so a
	// failure here (e.g. Jupyter unreachable) short-circuits with the same
	// opaqueError shape every other failure in this method uses.
	var xsrfToken string
	if method != http.MethodGet && method != http.MethodHead {
		token, err := c.ensureXSRFToken(ctx)
		if err != nil {
			return &opaqueError{op: method + " " + p, err: err}
		}
		xsrfToken = token
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
	if xsrfToken != "" {
		req.Header.Set("X-XSRFToken", xsrfToken)
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
