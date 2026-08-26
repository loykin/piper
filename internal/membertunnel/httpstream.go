package membertunnel

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/loykin/piper/internal/agentpb"
	"github.com/loykin/piper/internal/memberclient"
	"github.com/loykin/piper/internal/projectclient"
	"github.com/loykin/piper/internal/tunnelproxy"
	"github.com/loykin/piper/pkg/project"
)

const MethodProjectHTTP = "ProjectHTTP"

type httpOpenRequest struct {
	Method   string `json:"method"`
	Path     string `json:"path"`
	RawQuery string `json:"raw_query,omitempty"`
}

type streamConn struct {
	reader *io.PipeReader
	send   func([]byte, bool) error
	once   sync.Once
	closed chan struct{}
}

func (c *streamConn) Read(p []byte) (int, error) { return c.reader.Read(p) }
func (c *streamConn) Write(p []byte) (int, error) {
	for start := 0; start < len(p); {
		end := start + 32<<10
		if end > len(p) {
			end = len(p)
		}
		if err := c.send(append([]byte(nil), p[start:end]...), false); err != nil {
			return start, err
		}
		start = end
	}
	return len(p), nil
}
func (c *streamConn) Close() error {
	var err error
	c.once.Do(func() { close(c.closed); err = c.send(nil, true); _ = c.reader.Close() })
	return err
}
func (*streamConn) LocalAddr() net.Addr              { return streamAddr("member-tunnel-local") }
func (*streamConn) RemoteAddr() net.Addr             { return streamAddr("member-tunnel-remote") }
func (*streamConn) SetDeadline(time.Time) error      { return nil }
func (*streamConn) SetReadDeadline(time.Time) error  { return nil }
func (*streamConn) SetWriteDeadline(time.Time) error { return nil }

type streamAddr string

func (a streamAddr) Network() string { return "member-tunnel" }
func (a streamAddr) String() string  { return string(a) }

type streamResponseWriter struct {
	conn     net.Conn
	rw       *bufio.ReadWriter
	header   http.Header
	wrote    bool
	hijacked bool
}

func (w *streamResponseWriter) Header() http.Header { return w.header }
func (w *streamResponseWriter) WriteHeader(status int) {
	if w.wrote {
		return
	}
	w.wrote = true
	_, _ = fmt.Fprintf(w.rw, "HTTP/1.1 %d %s\r\n", status, http.StatusText(status))
	_ = w.header.Write(w.rw)
	_, _ = w.rw.WriteString("\r\n")
}
func (w *streamResponseWriter) Write(p []byte) (int, error) {
	if !w.wrote {
		w.WriteHeader(http.StatusOK)
	}
	if err := w.rw.Flush(); err != nil {
		return 0, err
	}
	return w.conn.Write(p)
}
func (w *streamResponseWriter) Flush() {
	if !w.wrote {
		w.WriteHeader(http.StatusOK)
	}
	_ = w.rw.Flush()
}
func (w *streamResponseWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	w.hijacked = true
	return w.conn, w.rw, nil
}

func serveOneHTTP(conn net.Conn, handler http.Handler) error {
	reader := bufio.NewReader(conn)
	req, err := http.ReadRequest(reader)
	if err != nil {
		return err
	}
	writer := &streamResponseWriter{conn: conn, rw: bufio.NewReadWriter(reader, bufio.NewWriter(conn)), header: make(http.Header)}
	handler.ServeHTTP(writer, req)
	if !writer.hijacked {
		if !writer.wrote {
			writer.WriteHeader(http.StatusOK)
		}
		_ = writer.rw.Flush()
		return conn.Close()
	}
	return nil
}

func signedHTTPPayload(auth memberclient.AuthContext, ref project.ProjectRef, request *http.Request, key string) ([]byte, error) {
	req := httpOpenRequest{Method: request.Method, Path: request.URL.Path, RawQuery: request.URL.RawQuery}
	raw, _ := json.Marshal(req)
	signed, err := memberclient.SignDelegation(auth, ref, MethodProjectHTTP, raw, key, time.Now())
	if err != nil {
		return nil, err
	}
	return encodeCall(signed, ref, req)
}

func (r *remoteMemberClient) ServeProjectHTTP(ctx context.Context, auth memberclient.AuthContext, ref project.ProjectRef, w http.ResponseWriter, req *http.Request) error {
	payload, err := signedHTTPPayload(auth, ref, req, r.token)
	if err != nil {
		return err
	}
	id := uuid.NewString()
	frames := newHTTPFrameQueue()
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		return memberclient.ErrMemberUnavailable
	}
	if r.streams == nil {
		r.streams = make(map[string]*httpFrameQueue)
	}
	r.streams[id] = frames
	r.mu.Unlock()
	defer func() { r.mu.Lock(); delete(r.streams, id); r.mu.Unlock() }()
	if err := r.send(&agentpb.HomeMessage{Payload: &agentpb.HomeMessage_HttpOpen{HttpOpen: &agentpb.MemberHTTPStreamOpen{StreamId: id, Payload: payload}}}); err != nil {
		return err
	}
	reader, inbound := io.Pipe()
	conn := &streamConn{reader: reader, closed: make(chan struct{}), send: func(data []byte, end bool) error {
		return r.send(&agentpb.HomeMessage{Payload: &agentpb.HomeMessage_HttpStream{HttpStream: &agentpb.MemberHTTPStreamData{StreamId: id, Data: data, End: end}}})
	}}
	go func() {
		defer inbound.Close()
		for {
			frame, ok := frames.pop(ctx)
			if !ok {
				if ctx.Err() != nil {
					_ = inbound.CloseWithError(ctx.Err())
				} else {
					_ = inbound.CloseWithError(memberclient.ErrMemberUnavailable)
				}
				return
			}
			if frame.Error != "" {
				_ = inbound.CloseWithError(errors.New(frame.Error))
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
	}()
	defer conn.Close()
	return tunnelproxy.ServeHTTP(w, req.Clone(ctx), conn, nil)
}

func (r *remoteMemberClient) deliverHTTP(frame *agentpb.MemberHTTPStreamData) {
	r.mu.Lock()
	q := r.streams[frame.StreamId]
	r.mu.Unlock()
	if q == nil {
		return
	}
	q.push(frame)
}

var _ projectclient.StreamClient = (*remoteMemberClient)(nil)
