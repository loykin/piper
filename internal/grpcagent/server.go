package grpcagent

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"sync"
	"time"

	"github.com/google/uuid"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/piper/piper/internal/agentpb"
)

// PushHandler is called when a worker sends an async StatusPush.
// agentID identifies the sending agent so callers can validate ownership.
type PushHandler func(ctx context.Context, agentID, method string, payload []byte)

// Server implements AgentServiceServer. It maintains one stream per connected
// worker and exposes SendRPC for master-initiated commands.
type Server struct {
	agentpb.UnimplementedAgentServiceServer

	mu             sync.RWMutex
	conns          map[string]*workerConn // agentID → connection
	subMu          sync.Mutex
	subs           map[string][]chan struct{} // agentID → waiters
	onReg          func(info Registration)    // called when a worker registers
	onLost         func(agentID string)       // called when a worker disconnects
	pushHandler    PushHandler
	connectHandler func(agentID string) // called after onReg completes
}

// Registration carries the metadata a worker sends on first connect.
type Registration struct {
	ID             string
	Infrastructure string
	Hostname       string
	Capabilities   []string
	ClusterName    string
	Labels         map[string]string
	Namespaces     []string
	ConnectedAt    time.Time
}

// NewServer creates a gRPC AgentService server.
// onReg is called (in a goroutine) whenever a new worker registers.
// onLost is called (in a goroutine) whenever a worker disconnects.
func NewServer(onReg func(Registration), onLost func(agentID string)) *Server {
	return &Server{
		conns:  make(map[string]*workerConn),
		subs:   make(map[string][]chan struct{}),
		onReg:  onReg,
		onLost: onLost,
	}
}

// SetPushHandler registers the handler for worker-initiated StatusPush messages.
func (s *Server) SetPushHandler(h PushHandler) { s.pushHandler = h }

// SetConnectHandler registers a callback invoked (in a goroutine) after each
// agent registration completes. Use it to trigger state reconciliation on connect
// and reconnect, after the agent registry has already been updated.
func (s *Server) SetConnectHandler(fn func(agentID string)) { s.connectHandler = fn }

// GRPCServer returns a new *grpc.Server with this service registered.
func (s *Server) GRPCServer(opts ...grpc.ServerOption) *grpc.Server {
	srv := grpc.NewServer(opts...)
	agentpb.RegisterAgentServiceServer(srv, s)
	return srv
}

// Connect is the single gRPC streaming RPC. Workers keep this stream alive.
func (s *Server) Connect(stream agentpb.AgentService_ConnectServer) error {
	// First message must be a Registration.
	first, err := stream.Recv()
	if err != nil {
		return err
	}
	reg := first.GetRegister()
	if reg == nil {
		return status.Error(codes.InvalidArgument, "first message must be Registration")
	}
	if err := validateRegistration(reg); err != nil {
		return err
	}

	info := Registration{
		ID:             reg.Id,
		Infrastructure: reg.Infrastructure,
		Hostname:       reg.Hostname,
		Capabilities:   reg.Capabilities,
		ClusterName:    reg.ClusterName,
		Labels:         reg.Labels,
		Namespaces:     reg.Namespaces,
		ConnectedAt:    time.Now(),
	}

	conn := newWorkerConn(reg.Id, stream)
	s.register(conn)
	defer s.unregister(reg.Id, conn)

	if s.onReg != nil {
		s.onReg(info) // synchronous so agentReg is updated before connectHandler fires
	}
	if s.connectHandler != nil {
		go s.connectHandler(reg.Id)
	}
	slog.Info("grpc agent connected", "id", reg.Id, "infrastructure", reg.Infrastructure, "hostname", reg.Hostname)
	go conn.runPushLoop(s.pushHandler)

	// Read loop: handle RPC responses, status pushes, and proxy frames from the worker.
	for {
		msg, err := stream.Recv()
		if err != nil {
			if err == io.EOF {
				return nil
			}
			return err
		}
		switch p := msg.Payload.(type) {
		case *agentpb.WorkerMessage_Response:
			conn.deliver(p.Response)
		case *agentpb.WorkerMessage_Push:
			payload := append([]byte(nil), p.Push.Payload...)
			conn.pushQueue.enqueue(p.Push.Method, payload)
		case *agentpb.WorkerMessage_ProxyData:
			conn.deliverProxyData(p.ProxyData.ChannelId, p.ProxyData.Data)
		case *agentpb.WorkerMessage_ProxyClose:
			conn.deliverProxyClose(p.ProxyClose.ChannelId, p.ProxyClose.Error)
		}
	}
}

func validateRegistration(reg *agentpb.Registration) error {
	if reg.Id == "" {
		return status.Error(codes.InvalidArgument, "registration id is required")
	}
	switch reg.Infrastructure {
	case "baremetal", "docker", "k8s":
		return nil
	default:
		return status.Error(codes.InvalidArgument, "registration infrastructure must be baremetal, docker, or k8s")
	}
}

// SendRPC sends a command to a specific worker and waits for the response.
func (s *Server) SendRPC(ctx context.Context, agentID, method string, payload any, result any) error {
	s.mu.RLock()
	conn := s.conns[agentID]
	s.mu.RUnlock()
	if conn == nil {
		return fmt.Errorf("agent %q is not connected", agentID)
	}
	return conn.sendRPC(ctx, method, payload, result)
}

// DialProxy opens a proxy channel to target (host:port) through the given agent.
// The returned net.Conn tunnels raw bytes over the gRPC Connect stream.
// target is a "host:port" address reachable from inside the agent's cluster.
func (s *Server) DialProxy(ctx context.Context, agentID, target string) (net.Conn, error) {
	s.mu.RLock()
	conn := s.conns[agentID]
	s.mu.RUnlock()
	if conn == nil {
		return nil, fmt.Errorf("agent %q is not connected", agentID)
	}

	channelID := uuid.NewString()
	pr, pw := io.Pipe()
	pc := &proxyChannel{incoming: make(chan []byte, 1024), pw: pw}
	conn.proxyChannels.Store(channelID, pc)

	// Decouple the recv loop from the pipe: a dedicated goroutine drains the
	// buffered incoming channel and writes to pw. Without this, deliverProxyData
	// would block the gRPC recv loop whenever the pipe reader is not yet ready.
	go func() {
		for data := range pc.incoming {
			if _, err := pw.Write(data); err != nil {
				for range pc.incoming {
				} // drain remaining to unblock senders
				return
			}
		}
		_ = pw.Close()
	}()

	conn.writeMu.Lock()
	err := conn.stream.Send(&agentpb.MasterMessage{
		Payload: &agentpb.MasterMessage_ProxyOpen{
			ProxyOpen: &agentpb.ProxyOpen{ChannelId: channelID, Target: target},
		},
	})
	conn.writeMu.Unlock()
	if err != nil {
		conn.proxyChannels.Delete(channelID)
		proxyChannelClose(pc.incoming)
		_ = pw.CloseWithError(err)
		return nil, fmt.Errorf("send ProxyOpen: %w", err)
	}

	pcn := &proxyConn{channelID: channelID, wconn: conn, pc: pc, pr: pr, closed: make(chan struct{})}
	// ctx is the caller's dial-scope context, not tied to the tunnel itself.
	// If it's canceled (e.g. the HTTP request that triggered this dial ends)
	// before the caller explicitly closes pcn, tear the channel down instead
	// of leaking it until the tunnel connection itself goes away.
	go func() {
		select {
		case <-ctx.Done():
			_ = pcn.Close()
		case <-pcn.closed:
		}
	}()
	return pcn, nil
}

// Connected reports whether the agent has an active stream.
func (s *Server) Connected(agentID string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.conns[agentID] != nil
}

// WaitConnected blocks until the agent connects or ctx is cancelled.
func (s *Server) WaitConnected(ctx context.Context, agentID string) error {
	s.mu.RLock()
	ok := s.conns[agentID] != nil
	s.mu.RUnlock()
	if ok {
		return nil
	}
	ch := make(chan struct{})
	s.subMu.Lock()
	s.subs[agentID] = append(s.subs[agentID], ch)
	s.subMu.Unlock()
	select {
	case <-ctx.Done():
		return fmt.Errorf("agent %q not connected: %w", agentID, ctx.Err())
	case <-ch:
		return nil
	}
}

func (s *Server) register(conn *workerConn) {
	s.mu.Lock()
	old := s.conns[conn.agentID]
	s.conns[conn.agentID] = conn
	s.mu.Unlock()
	if old != nil {
		old.close()
	}
	s.subMu.Lock()
	chans := s.subs[conn.agentID]
	delete(s.subs, conn.agentID)
	s.subMu.Unlock()
	for _, ch := range chans {
		close(ch)
	}
}

func (s *Server) unregister(agentID string, conn *workerConn) {
	s.mu.Lock()
	isCurrent := s.conns[agentID] == conn
	if isCurrent {
		delete(s.conns, agentID)
	}
	s.mu.Unlock()
	conn.close()
	// Only fire onLost when this was still the active connection.
	// If a newer connection already replaced it, the new registration must not be removed.
	if isCurrent && s.onLost != nil {
		go s.onLost(agentID)
	}
	slog.Info("grpc agent disconnected", "id", agentID, "was_current", isCurrent)
}

// ── per-worker connection ─────────────────────────────────────────────────────

type workerConn struct {
	agentID       string
	stream        agentpb.AgentService_ConnectServer
	writeMu       sync.Mutex
	pending       sync.Map // requestID → chan *agentpb.RPCResponse
	proxyChannels sync.Map // channelID → *proxyChannel
	// pushQueue holds worker-initiated StatusPush messages, drained by
	// runPushLoop in strict lane-priority order (control > durable >
	// telemetry > bulk) so a slow DB-backed log handler can't starve task
	// results or lease renewals behind it. See lane.go.
	pushQueue *boundedLaneQueue
	pushDone  chan struct{}
	closed    chan struct{}
	once      sync.Once
}

// proxyChannel holds the buffered incoming channel and the pipe write end for
// one active proxy session. The incoming channel decouples the gRPC recv loop
// from the blocking io.PipeWriter.
type proxyChannel struct {
	incoming chan []byte
	pw       *io.PipeWriter
}

func newWorkerConn(agentID string, stream agentpb.AgentService_ConnectServer) *workerConn {
	return &workerConn{
		agentID:   agentID,
		stream:    stream,
		pushQueue: newBoundedLaneQueue(),
		pushDone:  make(chan struct{}),
		closed:    make(chan struct{}),
	}
}

func (c *workerConn) runPushLoop(handler PushHandler) {
	defer close(c.pushDone)
	for {
		item, ok := c.pushQueue.next()
		if !ok {
			return
		}
		if handler != nil {
			handler(context.Background(), c.agentID, item.method, item.payload)
		}
	}
}

func (c *workerConn) sendRPC(ctx context.Context, method string, payload any, result any) error {
	data, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal rpc payload: %w", err)
	}
	reqID := fmt.Sprintf("%d", time.Now().UnixNano())
	ch := make(chan *agentpb.RPCResponse, 1)
	c.pending.Store(reqID, ch)
	defer c.pending.Delete(reqID)

	c.writeMu.Lock()
	err = c.stream.Send(&agentpb.MasterMessage{
		Payload: &agentpb.MasterMessage_RpcCmd{
			RpcCmd: &agentpb.RPCCommand{
				RequestId: reqID,
				Method:    method,
				Payload:   data,
			},
		},
	})
	c.writeMu.Unlock()
	if err != nil {
		return err
	}

	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-c.closed:
		return fmt.Errorf("agent %q disconnected", c.agentID)
	case resp := <-ch:
		if resp.Error != "" {
			return fmt.Errorf("agent rpc %s: %s", method, resp.Error)
		}
		if result != nil && len(resp.Payload) > 0 {
			return json.Unmarshal(resp.Payload, result)
		}
		return nil
	}
}

func (c *workerConn) deliver(resp *agentpb.RPCResponse) {
	if chAny, ok := c.pending.Load(resp.RequestId); ok {
		ch := chAny.(chan *agentpb.RPCResponse)
		select {
		case ch <- resp:
		default:
		}
	}
}

// deliverProxyData is called from the gRPC recv loop and must not block.
// It sends data to the buffered incoming channel; the per-channel goroutine
// started in DialProxy writes it to the io.Pipe asynchronously. On overflow
// (the local reader isn't draining fast enough) it does NOT silently drop
// bytes — that would corrupt the tunneled stream. Instead it tears the
// channel down loudly and tells the worker why, so exactly one HTTP/WS
// connection resets cleanly instead of delivering truncated data.
func (c *workerConn) deliverProxyData(channelID string, data []byte) {
	pcAny, ok := c.proxyChannels.Load(channelID)
	if !ok {
		return
	}
	pc := pcAny.(*proxyChannel)
	b := make([]byte, len(data))
	copy(b, data)
	if !proxyChannelSend(pc.incoming, b) {
		c.closeProxyChannelWithError(channelID, pc, fmt.Errorf("backpressure: receiver too slow"))
	}
}

func (c *workerConn) deliverProxyClose(channelID, errMsg string) {
	if pcAny, ok := c.proxyChannels.LoadAndDelete(channelID); ok {
		pc := pcAny.(*proxyChannel)
		if errMsg != "" {
			_ = pc.pw.CloseWithError(fmt.Errorf("proxy: %s", errMsg))
		}
		proxyChannelClose(pc.incoming)
	}
}

// closeProxyChannelWithError tears down channelID locally and tells the
// worker to stop forwarding for it, carrying cause so the failure isn't
// silent on either end.
func (c *workerConn) closeProxyChannelWithError(channelID string, pc *proxyChannel, cause error) {
	if _, loaded := c.proxyChannels.LoadAndDelete(channelID); !loaded {
		return // already torn down by someone else
	}
	_ = pc.pw.CloseWithError(cause)
	proxyChannelClose(pc.incoming)
	c.writeMu.Lock()
	_ = c.stream.Send(&agentpb.MasterMessage{
		Payload: &agentpb.MasterMessage_ProxyClose{
			ProxyClose: &agentpb.ProxyClose{ChannelId: channelID, Error: cause.Error()},
		},
	})
	c.writeMu.Unlock()
}

func (c *workerConn) close() {
	c.once.Do(func() {
		close(c.closed)
		c.pushQueue.close()
		<-c.pushDone
		c.proxyChannels.Range(func(_, pcAny any) bool {
			pc := pcAny.(*proxyChannel)
			_ = pc.pw.CloseWithError(io.ErrUnexpectedEOF)
			proxyChannelClose(pc.incoming)
			return true
		})
	})
}

// ── proxyConn — net.Conn backed by a gRPC proxy channel ──────────────────────

type proxyConn struct {
	channelID string
	wconn     *workerConn
	pc        *proxyChannel
	pr        *io.PipeReader
	once      sync.Once
	closed    chan struct{} // closed once, by Close — lets the DialProxy ctx-watcher goroutine stop

	deadlineMu    sync.Mutex
	writeDeadline time.Time
	readTimer     *time.Timer
}

func (p *proxyConn) Read(b []byte) (int, error) { return p.pr.Read(b) }

func (p *proxyConn) Write(b []byte) (int, error) {
	p.deadlineMu.Lock()
	wd := p.writeDeadline
	p.deadlineMu.Unlock()
	if !wd.IsZero() && time.Now().After(wd) {
		return 0, os.ErrDeadlineExceeded
	}
	p.wconn.writeMu.Lock()
	err := p.wconn.stream.Send(&agentpb.MasterMessage{
		Payload: &agentpb.MasterMessage_ProxyData{
			ProxyData: &agentpb.ProxyData{ChannelId: p.channelID, Data: b},
		},
	})
	p.wconn.writeMu.Unlock()
	if err != nil {
		return 0, err
	}
	return len(b), nil
}

func (p *proxyConn) Close() error {
	p.once.Do(func() {
		close(p.closed)
		p.deadlineMu.Lock()
		if p.readTimer != nil {
			p.readTimer.Stop()
		}
		p.deadlineMu.Unlock()
		_ = p.pr.CloseWithError(io.ErrClosedPipe)
		if _, loaded := p.wconn.proxyChannels.LoadAndDelete(p.channelID); loaded {
			proxyChannelClose(p.pc.incoming)
		}
		p.wconn.writeMu.Lock()
		_ = p.wconn.stream.Send(&agentpb.MasterMessage{
			Payload: &agentpb.MasterMessage_ProxyClose{
				ProxyClose: &agentpb.ProxyClose{ChannelId: p.channelID},
			},
		})
		p.wconn.writeMu.Unlock()
	})
	return nil
}

func (p *proxyConn) LocalAddr() net.Addr  { return proxyAddr("master") }
func (p *proxyConn) RemoteAddr() net.Addr { return proxyAddr(p.channelID) }

func (p *proxyConn) SetDeadline(t time.Time) error {
	if err := p.SetReadDeadline(t); err != nil {
		return err
	}
	return p.SetWriteDeadline(t)
}

// SetReadDeadline arms a timer that force-closes the underlying pipe reader
// with os.ErrDeadlineExceeded once the deadline passes, unblocking a stalled
// Read. Note: because io.Pipe cannot be "un-closed," once a deadline fires
// this connection's reads are permanently done — matching how a caller
// should treat a deadline-exceeded net.Conn in practice (close and redial),
// even though the standard net.Conn contract technically allows extending a
// deadline and continuing.
func (p *proxyConn) SetReadDeadline(t time.Time) error {
	p.deadlineMu.Lock()
	defer p.deadlineMu.Unlock()
	if p.readTimer != nil {
		p.readTimer.Stop()
		p.readTimer = nil
	}
	if t.IsZero() {
		return nil
	}
	d := time.Until(t)
	if d <= 0 {
		// io.Pipe: to make pr.Read() return a specific error, the WRITER
		// side must be closed with it (closing the reader instead only
		// affects future writes, not reads) — see io.PipeWriter.CloseWithError.
		_ = p.pc.pw.CloseWithError(os.ErrDeadlineExceeded)
		return nil
	}
	p.readTimer = time.AfterFunc(d, func() {
		_ = p.pc.pw.CloseWithError(os.ErrDeadlineExceeded)
	})
	return nil
}

// SetWriteDeadline records the deadline; Write checks it at call time and
// fails fast once passed. This is an honest partial implementation: unlike
// SetReadDeadline it cannot interrupt a Write already in flight (Write here
// is a single non-blocking-ish gRPC Send, not a long blocking syscall), but
// it does prevent new writes after the deadline.
func (p *proxyConn) SetWriteDeadline(t time.Time) error {
	p.deadlineMu.Lock()
	p.writeDeadline = t
	p.deadlineMu.Unlock()
	return nil
}

type proxyAddr string

func (a proxyAddr) Network() string { return "grpc-proxy" }
func (a proxyAddr) String() string  { return string(a) }

// ── channel helpers ───────────────────────────────────────────────────────────

// proxyChannelSend sends data to the channel without blocking, reporting
// whether it was actually accepted. A full channel (1024 items) means the
// local reader isn't keeping up; callers must not treat that as a benign
// drop — see deliverProxyData/closeProxyChannelWithError, which tear the
// channel down loudly instead of silently corrupting the tunneled stream. A
// closed-channel panic is treated the same as "not accepted."
func proxyChannelSend(ch chan []byte, data []byte) (sent bool) {
	defer func() {
		if recover() != nil {
			sent = false
		}
	}()
	select {
	case ch <- data:
		return true
	default:
		return false
	}
}

// proxyChannelClose closes the channel, recovering if it is already closed.
func proxyChannelClose(ch chan []byte) {
	defer func() { recover() }()
	close(ch)
}
