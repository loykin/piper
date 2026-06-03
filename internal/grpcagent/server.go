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

	"github.com/google/uuid"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/piper/piper/internal/agentpb"
)

// PushHandler is called when a worker sends an async StatusPush.
type PushHandler func(ctx context.Context, method string, payload []byte)

// Server implements AgentServiceServer. It maintains one stream per connected
// worker and exposes SendRPC for master-initiated commands.
type Server struct {
	agentpb.UnimplementedAgentServiceServer

	mu          sync.RWMutex
	conns       map[string]*workerConn // agentID → connection
	subMu       sync.Mutex
	subs        map[string][]chan struct{} // agentID → waiters
	onReg       func(info Registration)    // called when a worker registers
	onLost      func(agentID string)       // called when a worker disconnects
	pushHandler PushHandler
}

type pushMessage struct {
	method  string
	payload []byte
}

type pushQueue struct {
	mu     sync.Mutex
	cond   *sync.Cond
	items  []pushMessage
	closed bool
}

func newPushQueue() *pushQueue {
	q := &pushQueue{}
	q.cond = sync.NewCond(&q.mu)
	return q
}

func (q *pushQueue) enqueue(msg pushMessage) bool {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.closed {
		return false
	}
	q.items = append(q.items, msg)
	q.cond.Signal()
	return true
}

func (q *pushQueue) next() (pushMessage, bool) {
	q.mu.Lock()
	defer q.mu.Unlock()
	for len(q.items) == 0 && !q.closed {
		q.cond.Wait()
	}
	if len(q.items) == 0 {
		return pushMessage{}, false
	}
	msg := q.items[0]
	q.items[0] = pushMessage{}
	q.items = q.items[1:]
	return msg, true
}

func (q *pushQueue) close() {
	q.mu.Lock()
	if !q.closed {
		q.closed = true
		q.cond.Broadcast()
	}
	q.mu.Unlock()
}

// Registration carries the metadata a worker sends on first connect.
type Registration struct {
	ID           string
	Kind         string
	Hostname     string
	GPUs         []string
	Capabilities []string
	ClusterName  string
	Labels       map[string]string
	ConnectedAt  time.Time
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
	if reg.Id == "" {
		return status.Error(codes.InvalidArgument, "registration id is required")
	}

	info := Registration{
		ID:           reg.Id,
		Kind:         reg.Kind,
		Hostname:     reg.Hostname,
		GPUs:         reg.Gpus,
		Capabilities: reg.Capabilities,
		ClusterName:  reg.ClusterName,
		Labels:       reg.Labels,
		ConnectedAt:  time.Now(),
	}

	conn := newWorkerConn(reg.Id, stream)
	s.register(conn)
	defer s.unregister(reg.Id, conn)

	if s.onReg != nil {
		go s.onReg(info)
	}
	slog.Info("grpc agent connected", "id", reg.Id, "kind", reg.Kind, "hostname", reg.Hostname)
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
			conn.pushQueue.enqueue(pushMessage{method: p.Push.Method, payload: payload})
		case *agentpb.WorkerMessage_ProxyData:
			conn.deliverProxyData(p.ProxyData.ChannelId, p.ProxyData.Data)
		case *agentpb.WorkerMessage_ProxyClose:
			conn.deliverProxyClose(p.ProxyClose.ChannelId, p.ProxyClose.Error)
		}
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
func (s *Server) DialProxy(_ context.Context, agentID, target string) (net.Conn, error) {
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
	return &proxyConn{channelID: channelID, wconn: conn, pc: pc, pr: pr}, nil
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
	if s.conns[agentID] == conn {
		delete(s.conns, agentID)
	}
	s.mu.Unlock()
	conn.close()
	if s.onLost != nil {
		go s.onLost(agentID)
	}
	slog.Info("grpc agent disconnected", "id", agentID)
}

// ── per-worker connection ─────────────────────────────────────────────────────

type workerConn struct {
	agentID       string
	stream        agentpb.AgentService_ConnectServer
	writeMu       sync.Mutex
	pending       sync.Map // requestID → chan *agentpb.RPCResponse
	proxyChannels sync.Map // channelID → *proxyChannel
	pushQueue     *pushQueue
	pushDone      chan struct{}
	closed        chan struct{}
	once          sync.Once
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
		pushQueue: newPushQueue(),
		pushDone:  make(chan struct{}),
		closed:    make(chan struct{}),
	}
}

func (c *workerConn) runPushLoop(handler PushHandler) {
	defer close(c.pushDone)
	for {
		msg, ok := c.pushQueue.next()
		if !ok {
			return
		}
		if handler != nil {
			handler(context.Background(), msg.method, msg.payload)
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
// started in DialProxy writes it to the io.Pipe asynchronously.
func (c *workerConn) deliverProxyData(channelID string, data []byte) {
	if pcAny, ok := c.proxyChannels.Load(channelID); ok {
		b := make([]byte, len(data))
		copy(b, data)
		proxyChannelSend(pcAny.(*proxyChannel).incoming, b)
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
}

func (p *proxyConn) Read(b []byte) (int, error) { return p.pr.Read(b) }

func (p *proxyConn) Write(b []byte) (int, error) {
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

func (p *proxyConn) LocalAddr() net.Addr                { return proxyAddr("master") }
func (p *proxyConn) RemoteAddr() net.Addr               { return proxyAddr(p.channelID) }
func (p *proxyConn) SetDeadline(_ time.Time) error      { return nil }
func (p *proxyConn) SetReadDeadline(_ time.Time) error  { return nil }
func (p *proxyConn) SetWriteDeadline(_ time.Time) error { return nil }

type proxyAddr string

func (a proxyAddr) Network() string { return "grpc-proxy" }
func (a proxyAddr) String() string  { return string(a) }

// ── channel helpers ───────────────────────────────────────────────────────────

// proxyChannelSend sends data to the channel without blocking.
// A full channel (1024 items) indicates something is severely wrong; data is dropped.
// A closed-channel panic is silently recovered.
func proxyChannelSend(ch chan []byte, data []byte) {
	defer func() { recover() }()
	select {
	case ch <- data:
	default:
	}
}

// proxyChannelClose closes the channel, recovering if it is already closed.
func proxyChannelClose(ch chan []byte) {
	defer func() { recover() }()
	close(ch)
}
