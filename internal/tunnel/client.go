package tunnel

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
)

// ClientConfig holds connection parameters for a tunnel client.
type ClientConfig struct {
	MasterURL string
	AgentID   string
	Token     string
}

// Client manages the outbound WebSocket tunnel lifecycle: register → connect →
// dispatch RPC frames → reconnect on disconnect.
// Callers register RPC handlers on Dispatcher() before calling Run.
type Client struct {
	cfg        ClientConfig
	dispatcher *Dispatcher
	httpClient *http.Client
}

func NewClient(cfg ClientConfig) *Client {
	return &Client{
		cfg:        cfg,
		dispatcher: NewDispatcher(),
		httpClient: &http.Client{Timeout: 10 * time.Second},
	}
}

// Dispatcher returns the RPC dispatcher. Register handlers on it before calling Run.
func (c *Client) Dispatcher() *Dispatcher {
	return c.dispatcher
}

// Run calls registerFn then opens the tunnel, reconnecting every 5 s on disconnect.
// registerFn is invoked before each connection attempt so that the server always has
// fresh registration state after a restart.
func (c *Client) Run(ctx context.Context, registerFn func(context.Context) error) error {
	if c.cfg.MasterURL == "" {
		return fmt.Errorf("tunnel client: master url is required")
	}
	if c.cfg.AgentID == "" {
		return fmt.Errorf("tunnel client: agent id is required")
	}
	for {
		if err := registerFn(ctx); err != nil {
			return err
		}
		if err := c.connectAndServe(ctx); err != nil && ctx.Err() == nil {
			slog.Warn("tunnel disconnected, reconnecting", "err", err)
		}
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(5 * time.Second):
		}
	}
}

// connectAndServe dials the tunnel WebSocket and serves RPC frames until the
// connection closes or ctx is cancelled.
// The server sends periodic WebSocket pings; the library replies with pong
// automatically, keeping the server-side registry entry alive.
func (c *Client) connectAndServe(ctx context.Context) error {
	tunnelURL, err := TunnelURL(c.cfg.MasterURL, c.cfg.AgentID)
	if err != nil {
		return err
	}
	opts := &websocket.DialOptions{}
	if c.cfg.Token != "" {
		opts.HTTPHeader = http.Header{"Authorization": []string{"Bearer " + c.cfg.Token}}
	}
	conn, _, err := websocket.Dial(ctx, tunnelURL, opts)
	if err != nil {
		return err
	}
	defer func() { _ = conn.Close(websocket.StatusNormalClosure, "") }()

	for {
		var frame Frame
		if err := wsjson.Read(ctx, conn, &frame); err != nil {
			return err
		}
		if frame.Type != FrameRPCRequest {
			continue
		}
		if err := wsjson.Write(ctx, conn, c.dispatcher.Handle(ctx, frame)); err != nil {
			return err
		}
	}
}

// TunnelURL converts a master HTTP(S) URL to the agent-specific WebSocket tunnel URL.
func TunnelURL(masterURL, agentID string) (string, error) {
	u, err := url.Parse(masterURL)
	if err != nil {
		return "", err
	}
	switch u.Scheme {
	case "http":
		u.Scheme = "ws"
	case "https":
		u.Scheme = "wss"
	case "ws", "wss":
	default:
		return "", fmt.Errorf("unsupported master url scheme %q", u.Scheme)
	}
	u.Path = strings.TrimRight(u.Path, "/") + "/api/agents/" + agentID + "/tunnel"
	u.RawQuery = ""
	return u.String(), nil
}
