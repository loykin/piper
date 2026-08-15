package membertunnel

import (
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"sync"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"

	"github.com/loykin/piper/internal/agentpb"
	"github.com/loykin/piper/internal/memberclient"
	"github.com/loykin/piper/internal/projectclient"
)

// Config configures a Member's outbound tunnel to its Home.
type Config struct {
	HomeURL  string
	HomeID   string
	MemberID string
	// Token is this Member's static enrollment secret, configured on both
	// sides — see server.go's ServerConfig.Tokens and the package doc for
	// why rotation/revocation is deferred.
	Token string
}

// Client manages the Member-side gRPC tunnel lifecycle: connect → enroll →
// dispatch incoming RPC commands to a local memberclient.Client →
// reconnect on disconnect. It keeps the federation transport lifecycle at a
// much smaller scale. HTTP stream frames are constrained to the Member's
// private project router and cannot name or dial an arbitrary target host.
type Client struct {
	cfg     Config
	member  memberclient.Client
	project projectclient.Client
	httpMu  sync.Mutex
	httpIn  map[string]chan *agentpb.MemberHTTPStreamData
}

// NewClient creates a Member-side tunnel client serving member's methods
// to whatever Home it enrolls with.
func NewClient(cfg Config, member memberclient.Client, projectClients ...projectclient.Client) *Client {
	var projectClient projectclient.Client
	if len(projectClients) > 0 {
		projectClient = projectClients[0]
	}
	return &Client{cfg: cfg, member: member, project: projectClient, httpIn: make(map[string]chan *agentpb.MemberHTTPStreamData)}
}

// Run connects to Home and serves RPC commands, reconnecting on disconnect.
// Blocks until ctx is cancelled.
func (c *Client) Run(ctx context.Context) error {
	if c.cfg.HomeURL == "" || c.cfg.HomeID == "" || c.cfg.MemberID == "" {
		return fmt.Errorf("membertunnel client: HomeURL, HomeID, and MemberID are required")
	}
	for {
		if err := c.connectAndServe(ctx); err != nil && ctx.Err() == nil {
			slog.Warn("member tunnel disconnected, reconnecting in 5s", "member_id", c.cfg.MemberID, "err", err)
		}
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(5 * time.Second):
		}
	}
}

func (c *Client) connectAndServe(ctx context.Context) error {
	u, err := url.Parse(c.cfg.HomeURL)
	if err != nil || u.Host == "" {
		return fmt.Errorf("membertunnel client: invalid HomeURL %q", c.cfg.HomeURL)
	}
	var transport credentials.TransportCredentials
	if u.Scheme == "https" {
		transport = credentials.NewTLS(&tls.Config{ServerName: u.Hostname(), MinVersion: tls.VersionTLS12})
	} else {
		transport = insecure.NewCredentials()
	}
	conn, err := grpc.NewClient(u.Host, grpc.WithTransportCredentials(transport))
	if err != nil {
		return err
	}
	defer func() { _ = conn.Close() }()

	stub := agentpb.NewMemberTunnelServiceClient(conn)
	stream, err := stub.Connect(ctx)
	if err != nil {
		return err
	}
	return c.serve(ctx, stream)
}

// memberStream is the subset of agentpb.MemberTunnelService_ConnectClient
// that serve's frame loop needs, so tests can inject a fake instead of
// requiring a real network listener.
type memberStream interface {
	Send(*agentpb.MemberMessage) error
	Recv() (*agentpb.HomeMessage, error)
}

// serve runs the enrollment handshake and the command dispatch loop against
// an already-established stream.
func (c *Client) serve(ctx context.Context, stream memberStream) error {
	defer func() {
		c.httpMu.Lock()
		for id, frames := range c.httpIn {
			close(frames)
			delete(c.httpIn, id)
		}
		c.httpMu.Unlock()
	}()
	var sendMu sync.Mutex
	send := func(msg *agentpb.MemberMessage) error {
		sendMu.Lock()
		defer sendMu.Unlock()
		return stream.Send(msg)
	}

	if err := send(&agentpb.MemberMessage{
		Payload: &agentpb.MemberMessage_Enroll{
			Enroll: &agentpb.MemberEnrollment{
				HomeId:   c.cfg.HomeID,
				MemberId: c.cfg.MemberID,
				Token:    c.cfg.Token,
			},
		},
	}); err != nil {
		return err
	}
	slog.Info("member tunnel enrolled with home", "member_id", c.cfg.MemberID, "home_url", c.cfg.HomeURL)

	for {
		msg, err := stream.Recv()
		if err != nil {
			if err == io.EOF {
				return nil
			}
			return err
		}
		if open := msg.GetHttpOpen(); open != nil {
			c.handleHTTPOpen(ctx, open, send)
			continue
		}
		if frame := msg.GetHttpStream(); frame != nil {
			c.deliverHTTP(frame)
			continue
		}
		cmd := msg.GetRpcCmd()
		if cmd == nil {
			continue
		}
		// Run off the recv loop so a slow handler can't block other
		// in-flight commands sharing this tunnel.
		go func(cmd *agentpb.MemberRPCCommand) {
			resp := c.handle(ctx, cmd)
			if err := send(&agentpb.MemberMessage{
				Payload: &agentpb.MemberMessage_Response{Response: resp},
			}); err != nil {
				slog.Warn("member tunnel: send rpc response failed", "method", cmd.Method, "err", err)
			}
		}(cmd)
	}
}

func (c *Client) handleHTTPOpen(ctx context.Context, open *agentpb.MemberHTTPStreamOpen, send func(*agentpb.MemberMessage) error) {
	fail := func(err error) {
		_ = send(&agentpb.MemberMessage{Payload: &agentpb.MemberMessage_HttpStream{HttpStream: &agentpb.MemberHTTPStreamData{StreamId: open.StreamId, End: true, Error: err.Error()}}})
	}
	if err := verifyCall(open.Payload, c.cfg.Token, c.cfg.HomeID, c.cfg.MemberID, MethodProjectHTTP, time.Now()); err != nil {
		fail(err)
		return
	}
	env, err := decodeCall[httpOpenRequest](open.Payload)
	if err != nil {
		fail(err)
		return
	}
	streamProject, ok := c.project.(projectclient.StreamClient)
	if !ok {
		fail(fmt.Errorf("membertunnel: project HTTP stream is unavailable"))
		return
	}
	reader, inbound := io.Pipe()
	frames := make(chan *agentpb.MemberHTTPStreamData, 128)
	c.httpMu.Lock()
	c.httpIn[open.StreamId] = frames
	c.httpMu.Unlock()
	conn := &streamConn{reader: reader, closed: make(chan struct{}), send: func(data []byte, end bool) error {
		return send(&agentpb.MemberMessage{Payload: &agentpb.MemberMessage_HttpStream{HttpStream: &agentpb.MemberHTTPStreamData{StreamId: open.StreamId, Data: data, End: end}}})
	}}
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != env.Req.Method || r.URL.Path != env.Req.Path || r.URL.RawQuery != env.Req.RawQuery {
			http.Error(w, "membertunnel: streamed HTTP request does not match delegation", http.StatusForbidden)
			return
		}
		if err := streamProject.ServeProjectHTTP(ctx, env.Auth, env.Ref, w, r); err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
		}
	})
	go func() {
		defer func() {
			c.httpMu.Lock()
			if c.httpIn[open.StreamId] == frames {
				delete(c.httpIn, open.StreamId)
			}
			c.httpMu.Unlock()
			_ = inbound.Close()
		}()
		for {
			select {
			case <-ctx.Done():
				_ = inbound.CloseWithError(ctx.Err())
				return
			case frame, ok := <-frames:
				if !ok {
					_ = inbound.CloseWithError(memberclient.ErrMemberUnavailable)
					return
				}
				if frame.Error != "" {
					_ = inbound.CloseWithError(fmt.Errorf("%s", frame.Error))
					return
				}
				if len(frame.Data) > 0 {
					if _, err := inbound.Write(frame.Data); err != nil {
						return
					}
				}
				if frame.End {
					return
				}
			}
		}
	}()
	go func() {
		_ = serveOneHTTP(conn, handler)
		_ = conn.Close()
	}()
}

func (c *Client) deliverHTTP(frame *agentpb.MemberHTTPStreamData) {
	c.httpMu.Lock()
	frames := c.httpIn[frame.StreamId]
	if frames == nil {
		c.httpMu.Unlock()
		return
	}
	select {
	case frames <- frame:
		c.httpMu.Unlock()
	default:
		delete(c.httpIn, frame.StreamId)
		close(frames)
		c.httpMu.Unlock()
	}
}

func (c *Client) handle(ctx context.Context, cmd *agentpb.MemberRPCCommand) *agentpb.MemberRPCResponse {
	resp := &agentpb.MemberRPCResponse{RequestId: cmd.RequestId}
	if err := verifyCall(cmd.Payload, c.cfg.Token, c.cfg.HomeID, c.cfg.MemberID, cmd.Method, time.Now()); err != nil {
		resp.Error = err.Error()
		return resp
	}
	payload, err := dispatch(ctx, c.member, cmd.Method, cmd.Payload, c.project)
	if err != nil {
		resp.Error = err.Error()
		return resp
	}
	resp.Payload = payload
	return resp
}
