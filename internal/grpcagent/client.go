package grpcagent

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"sync"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	"github.com/piper/piper/internal/agentpb"
)

// ClientConfig holds connection parameters for a worker-side gRPC client.
type ClientConfig struct {
	// AgentAddr is the gRPC address of the master agent server, e.g. "master:9090".
	AgentAddr string
	// AgentID uniquely identifies this worker.
	AgentID string
	// Registration metadata sent to master on connect.
	Kind         string
	Hostname     string
	GPUs         []string
	Capabilities []string
	ClusterName  string
	Labels       map[string]string
}

// Client manages the worker-side gRPC tunnel lifecycle:
// connect → send Registration → dispatch incoming RPC frames → reconnect on disconnect.
type Client struct {
	cfg        ClientConfig
	dispatcher *Dispatcher

	// current active stream, guarded by streamMu. nil when disconnected.
	streamMu sync.RWMutex
	curSend  func(*agentpb.WorkerMessage) error
}

// NewClient creates a new worker-side gRPC client.
func NewClient(cfg ClientConfig) *Client {
	return &Client{cfg: cfg, dispatcher: NewDispatcher()}
}

// Dispatcher returns the RPC dispatcher. Register handlers before calling Run.
func (c *Client) Dispatcher() *Dispatcher { return c.dispatcher }

// Run connects to the master and serves RPC frames, reconnecting on disconnect.
// Blocks until ctx is cancelled.
func (c *Client) Run(ctx context.Context) error {
	if c.cfg.AgentAddr == "" {
		return fmt.Errorf("grpc client: AgentAddr is required")
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
	conn, err := grpc.NewClient(c.cfg.AgentAddr,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		return err
	}
	defer func() { _ = conn.Close() }()

	stub := agentpb.NewAgentServiceClient(conn)
	stream, err := stub.Connect(ctx)
	if err != nil {
		return err
	}

	// streamMu guards all stream.Send calls — both RPC responses and proxy frames.
	var streamMu sync.Mutex
	send := func(msg *agentpb.WorkerMessage) error {
		streamMu.Lock()
		defer streamMu.Unlock()
		return stream.Send(msg)
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

	// Send Registration as the first message.
	if err := send(&agentpb.WorkerMessage{
		Payload: &agentpb.WorkerMessage_Register{
			Register: &agentpb.Registration{
				Id:           c.cfg.AgentID,
				Kind:         c.cfg.Kind,
				Hostname:     c.cfg.Hostname,
				Gpus:         c.cfg.GPUs,
				Capabilities: c.cfg.Capabilities,
				ClusterName:  c.cfg.ClusterName,
				Labels:       c.cfg.Labels,
			},
		},
	}); err != nil {
		return err
	}
	slog.Info("grpc agent registered with master", "id", c.cfg.AgentID, "addr", c.cfg.AgentAddr)

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
			resp := c.dispatcher.handleCmd(ctx, p.RpcCmd)
			if err := send(&agentpb.WorkerMessage{
				Payload: &agentpb.WorkerMessage_Response{Response: resp},
			}); err != nil {
				closeAllProxies()
				return err
			}

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
				sessionAny.(*clientProxySession).send(pd.Data)
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

func (s *clientProxySession) send(data []byte) {
	b := make([]byte, len(data))
	copy(b, data)
	defer func() { recover() }()
	select {
	case <-s.closed:
	case s.incoming <- b:
	default:
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
