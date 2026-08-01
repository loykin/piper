package grpcagent

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/url"
	"sync"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/keepalive"
	"google.golang.org/grpc/metadata"

	"github.com/piper/piper/internal/agentpb"
)

// ClientConfig holds connection parameters for a worker-side gRPC client.
type ClientConfig struct {
	// MasterURL is the single HTTP(S) endpoint used for the agent tunnel.
	MasterURL string
	// AgentID uniquely identifies this worker.
	AgentID string
	// WorkerToken is the bearer token sent in gRPC authorization metadata.
	// Must match the master's server.worker_token.
	// Leave empty in trusted/dev mode.
	WorkerToken string
	// Registration metadata sent to master on connect.
	Infrastructure string
	Hostname       string
	Capabilities   []string
	ClusterName    string
	Labels         map[string]string
	// Namespaces lists the Kubernetes namespaces this worker is allowed to
	// manage. Empty means unrestricted. Only meaningful for k8s workers.
	Namespaces []string
	// Capacity is the maximum number of concurrent tasks (0 = unlimited).
	Capacity int

	// DefaultCommandTimeout bounds a single RPC handler invocation as a
	// generic safety net (0 = defaultCommandTimeout). This is not the
	// authoritative per-task deadline — that's step.options.timeout, carried
	// in the dispatch payload and enforced separately.
	DefaultCommandTimeout time.Duration
	// CommandConcurrency bounds how many RPC handlers run at once per
	// connection (0 = defaultCommandConcurrency).
	CommandConcurrency int

	// KeepaliveTime is how often the client pings the master on an idle
	// tunnel to detect a half-open connection (0 = defaultKeepaliveTime).
	// This is a transport-level liveness check, distinct from — and must
	// not be conflated with — the pipeline task lease (worker ownership of
	// a specific task) or step.options.timeout (application-level
	// execution deadline). See leaseLoop and Queue.scheduleTimeoutLocked.
	KeepaliveTime time.Duration
	// KeepaliveTimeout bounds how long the client waits for a keepalive
	// ping ack before considering the connection dead (0 = defaultKeepaliveTimeout).
	KeepaliveTimeout time.Duration
}

const (
	defaultKeepaliveTime    = 20 * time.Second
	defaultKeepaliveTimeout = 10 * time.Second
)

// dialOptions builds the grpc.DialOptions for a worker's connection to
// master, including keepalive. Pulled out as a pure function so it's
// unit-testable without an actual network dial.
func dialOptions(cfg ClientConfig, transport credentials.TransportCredentials) []grpc.DialOption {
	keepaliveTime := cfg.KeepaliveTime
	if keepaliveTime <= 0 {
		keepaliveTime = defaultKeepaliveTime
	}
	keepaliveTimeout := cfg.KeepaliveTimeout
	if keepaliveTimeout <= 0 {
		keepaliveTimeout = defaultKeepaliveTimeout
	}
	return []grpc.DialOption{
		grpc.WithTransportCredentials(transport),
		grpc.WithKeepaliveParams(keepalive.ClientParameters{
			Time:                keepaliveTime,
			Timeout:             keepaliveTimeout,
			PermitWithoutStream: true, // ping even when this worker has no active task, to catch a half-open idle tunnel
		}),
	}
}

// Client manages the worker-side gRPC tunnel lifecycle:
// connect → send Registration → dispatch incoming RPC frames → reconnect on disconnect.
type Client struct {
	cfg        ClientConfig
	dispatcher *Dispatcher
	exec       *commandExecutor

	// current active stream, guarded by streamMu. nil when disconnected.
	streamMu sync.RWMutex
	curSend  func(*agentpb.WorkerMessage) error
}

// NewClient creates a new worker-side gRPC client.
func NewClient(cfg ClientConfig) *Client {
	return &Client{
		cfg:        cfg,
		dispatcher: NewDispatcher(),
		exec:       newCommandExecutor(cfg.CommandConcurrency),
	}
}

// Dispatcher returns the RPC dispatcher. Register handlers before calling Run.
func (c *Client) Dispatcher() *Dispatcher { return c.dispatcher }

// Run connects to the master and serves RPC frames, reconnecting on disconnect.
// Blocks until ctx is cancelled.
func (c *Client) Run(ctx context.Context) error {
	if c.cfg.MasterURL == "" {
		return fmt.Errorf("grpc client: MasterURL is required")
	}
	if c.cfg.AgentID == "" {
		return fmt.Errorf("grpc client: AgentID is required")
	}
	for {
		if err := c.connectAndServe(ctx); err != nil && ctx.Err() == nil {
			slog.Warn("grpc agent disconnected, reconnecting in 5s", "err", err)
		}
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(5 * time.Second):
		}
	}
}

// SendPush sends an async StatusPush to the master from any goroutine.
// Returns an error if not currently connected.
func (c *Client) SendPush(method string, payload any) error {
	c.streamMu.RLock()
	send := c.curSend
	c.streamMu.RUnlock()
	if send == nil {
		return fmt.Errorf("not connected to master")
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	return send(&agentpb.WorkerMessage{
		Payload: &agentpb.WorkerMessage_Push{
			Push: &agentpb.StatusPush{Method: method, Payload: data},
		},
	})
}

func (c *Client) connectAndServe(ctx context.Context) error {
	u, err := url.Parse(c.cfg.MasterURL)
	if err != nil || u.Host == "" {
		return fmt.Errorf("grpc client: invalid MasterURL %q", c.cfg.MasterURL)
	}
	var transport credentials.TransportCredentials
	if u.Scheme == "https" {
		transport = credentials.NewTLS(&tls.Config{ServerName: u.Hostname(), MinVersion: tls.VersionTLS12})
	} else {
		transport = insecure.NewCredentials()
	}
	conn, err := grpc.NewClient(u.Host, dialOptions(c.cfg, transport)...)
	if err != nil {
		return err
	}
	defer func() { _ = conn.Close() }()

	// Attach worker token as gRPC authorization metadata when configured.
	streamCtx := ctx
	if c.cfg.WorkerToken != "" {
		streamCtx = metadata.AppendToOutgoingContext(ctx,
			"authorization", "Bearer "+c.cfg.WorkerToken,
		)
	}

	stub := agentpb.NewAgentServiceClient(conn)
	stream, err := stub.Connect(streamCtx)
	if err != nil {
		return err
	}
	return c.serve(ctx, stream)
}

// workerStream is the subset of agentpb.AgentService_ConnectClient that
// serve's frame loop needs. The real generated stream type satisfies this
// structurally; tests inject a fake implementation (see fakestream_test.go)
// instead of requiring a real network listener.
type workerStream interface {
	Send(*agentpb.WorkerMessage) error
	Recv() (*agentpb.MasterMessage, error)
}

// serve runs the registration handshake and the frame-demux loop against an
// already-established stream. Split out from connectAndServe so it can be
// exercised directly in tests against a fake workerStream.
func (c *Client) serve(ctx context.Context, stream workerStream) error {
	// sendQueue schedules every outbound frame (registration, RPC responses,
	// pushes, proxy data/close) by lane priority instead of strict arrival
	// order, so bulk proxy traffic can never starve control frames on this
	// tunnel. A single dedicated goroutine owns the actual stream.Send calls
	// (gRPC streams require a single sender); send() blocks the caller until
	// its frame is actually sent, preserving the original synchronous
	// error-return contract every call site here already relies on.
	sendQueue := newBoundedLaneQueue()
	senderDone := make(chan struct{})
	go func() {
		defer close(senderDone)
		for {
			item, ok := sendQueue.next()
			if !ok {
				return
			}
			err := stream.Send(item.msg)
			if item.result != nil {
				item.result <- err
			}
		}
	}()
	defer func() {
		sendQueue.close()
		<-senderDone
	}()
	send := func(msg *agentpb.WorkerMessage) error {
		return sendQueue.enqueueMsgWait(classifyFrameLane(msg), msg)
	}

	// Register the current send function so SendPush can use it.
	c.streamMu.Lock()
	c.curSend = send
	c.streamMu.Unlock()
	defer func() {
		c.streamMu.Lock()
		c.curSend = nil
		c.streamMu.Unlock()
	}()

	// proxySessions maps channelID to a proxy session. Sessions are registered
	// before TCP dial completes so early ProxyData frames are buffered instead
	// of being dropped.
	var proxySessions sync.Map

	closeAllProxies := func() {
		proxySessions.Range(func(_, v any) bool {
			v.(*clientProxySession).close()
			return true
		})
	}

	// Merge capacity into Labels so we don't need a proto change.
	labels := make(map[string]string, len(c.cfg.Labels)+2)
	for k, v := range c.cfg.Labels {
		labels[k] = v
	}
	if c.cfg.Capacity > 0 {
		labels["capacity"] = fmt.Sprintf("%d", c.cfg.Capacity)
	}

	// Send Registration as the first message.
	if err := send(&agentpb.WorkerMessage{
		Payload: &agentpb.WorkerMessage_Register{
			Register: &agentpb.Registration{
				Id:             c.cfg.AgentID,
				Infrastructure: c.cfg.Infrastructure,
				Hostname:       c.cfg.Hostname,
				Capabilities:   c.cfg.Capabilities,
				ClusterName:    c.cfg.ClusterName,
				Labels:         labels,
				Namespaces:     c.cfg.Namespaces,
			},
		},
	}); err != nil {
		return err
	}
	slog.Info("grpc agent registered with master", "id", c.cfg.AgentID, "master_url", c.cfg.MasterURL)

	for {
		msg, err := stream.Recv()
		if err != nil {
			closeAllProxies()
			if err == io.EOF {
				return nil
			}
			return err
		}

		switch p := msg.Payload.(type) {
		case *agentpb.MasterMessage_RpcCmd:
			// Run off the recv loop: a slow handler (e.g. pipeline.dispatch
			// starting a container) must not block reading of other frames —
			// other RPCs, cancels, proxy data — sharing this tunnel. A send
			// failure here is not fed back into this loop directly; the
			// stream is bidirectional, so a broken connection will also
			// surface as a Recv() error on the next iteration above.
			cmd := p.RpcCmd
			c.exec.runBounded(ctx, c.cfg.DefaultCommandTimeout, func(cmdCtx context.Context) {
				resp := c.dispatcher.handleCmd(cmdCtx, cmd)
				if err := send(&agentpb.WorkerMessage{
					Payload: &agentpb.WorkerMessage_Response{Response: resp},
				}); err != nil {
					slog.Warn("grpc agent: send rpc response failed", "method", cmd.Method, "err", err)
				}
			})

		case *agentpb.MasterMessage_ProxyOpen:
			po := p.ProxyOpen
			session := newClientProxySession()
			proxySessions.Store(po.ChannelId, session)
			go func() {
				dialCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
				defer cancel()
				var d net.Dialer
				tc, dialErr := d.DialContext(dialCtx, "tcp", po.Target)
				if dialErr != nil {
					proxySessions.Delete(po.ChannelId)
					session.close()
					_ = send(&agentpb.WorkerMessage{
						Payload: &agentpb.WorkerMessage_ProxyClose{
							ProxyClose: &agentpb.ProxyClose{
								ChannelId: po.ChannelId,
								Error:     dialErr.Error(),
							},
						},
					})
					slog.Warn("proxy dial failed", "target", po.Target, "err", dialErr)
					return
				}
				if !session.attach(tc) {
					return
				}
				slog.Debug("proxy session opened", "channel", po.ChannelId, "target", po.Target)

				go session.writeToTarget()

				buf := make([]byte, 32*1024)
				for {
					n, readErr := tc.Read(buf)
					if n > 0 {
						data := make([]byte, n)
						copy(data, buf[:n])
						if sendErr := send(&agentpb.WorkerMessage{
							Payload: &agentpb.WorkerMessage_ProxyData{
								ProxyData: &agentpb.ProxyData{
									ChannelId: po.ChannelId,
									Data:      data,
								},
							},
						}); sendErr != nil {
							break
						}
					}
					if readErr != nil {
						break
					}
				}

				proxySessions.Delete(po.ChannelId)
				session.close()
				_ = send(&agentpb.WorkerMessage{
					Payload: &agentpb.WorkerMessage_ProxyClose{
						ProxyClose: &agentpb.ProxyClose{ChannelId: po.ChannelId},
					},
				})
				slog.Debug("proxy session closed", "channel", po.ChannelId)
			}()

		case *agentpb.MasterMessage_ProxyData:
			pd := p.ProxyData
			if sessionAny, ok := proxySessions.Load(pd.ChannelId); ok {
				session := sessionAny.(*clientProxySession)
				if !session.send(pd.Data) {
					// Overflow: the local target connection isn't being
					// drained fast enough. Silently dropping bytes here
					// would corrupt the tunneled stream, so tear the
					// session down loudly and tell master why, instead.
					proxySessions.Delete(pd.ChannelId)
					session.close()
					cause := "backpressure: receiver too slow"
					_ = send(&agentpb.WorkerMessage{
						Payload: &agentpb.WorkerMessage_ProxyClose{
							ProxyClose: &agentpb.ProxyClose{ChannelId: pd.ChannelId, Error: cause},
						},
					})
				}
			}

		case *agentpb.MasterMessage_ProxyClose:
			pc := p.ProxyClose
			if sessionAny, ok := proxySessions.LoadAndDelete(pc.ChannelId); ok {
				sessionAny.(*clientProxySession).close()
			}
		}
	}
}

type clientProxySession struct {
	incoming chan []byte
	closed   chan struct{}
	once     sync.Once

	connMu sync.Mutex
	conn   net.Conn
}

func newClientProxySession() *clientProxySession {
	return &clientProxySession{
		incoming: make(chan []byte, 1024),
		closed:   make(chan struct{}),
	}
}

func (s *clientProxySession) attach(conn net.Conn) bool {
	s.connMu.Lock()
	defer s.connMu.Unlock()
	select {
	case <-s.closed:
		_ = conn.Close()
		return false
	default:
		s.conn = conn
		return true
	}
}

// send delivers data to the session's buffered incoming channel, reporting
// whether it was accepted. A full channel means writeToTarget isn't
// draining fast enough; callers must not treat that as a benign drop — see
// the MasterMessage_ProxyData case in serve, which tears the session down
// loudly instead of silently corrupting the tunneled stream.
func (s *clientProxySession) send(data []byte) (sent bool) {
	b := make([]byte, len(data))
	copy(b, data)
	defer func() {
		if recover() != nil {
			sent = false
		}
	}()
	select {
	case <-s.closed:
		return false
	case s.incoming <- b:
		return true
	default:
		return false
	}
}

func (s *clientProxySession) writeToTarget() {
	for data := range s.incoming {
		s.connMu.Lock()
		conn := s.conn
		s.connMu.Unlock()
		if conn == nil {
			continue
		}
		if _, err := conn.Write(data); err != nil {
			s.close()
			return
		}
	}
}

func (s *clientProxySession) close() {
	s.once.Do(func() {
		close(s.closed)
		close(s.incoming)
		s.connMu.Lock()
		if s.conn != nil {
			_ = s.conn.Close()
		}
		s.connMu.Unlock()
	})
}
