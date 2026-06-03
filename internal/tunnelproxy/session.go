package tunnelproxy

import (
	"net"
	"net/http"
	"sync"
)

// Session represents one upstream tunnel endpoint bound to a policy.
type Session struct {
	upstream net.Conn
	policy   Policy
	once     sync.Once
	closed   chan struct{}
}

// Builder assembles a Session from an upstream connection and optional policy.
type Builder struct {
	upstream net.Conn
	policy   Policy
}

// NewBuilder creates a session builder for the given upstream connection.
func NewBuilder(upstream net.Conn) *Builder {
	return &Builder{upstream: upstream}
}

// WithPolicy sets the policy used by the eventual Session.
func (b *Builder) WithPolicy(policy Policy) *Builder {
	if b == nil {
		return nil
	}
	b.policy = policy
	return b
}

// Build creates the Session.
func (b *Builder) Build() *Session {
	if b == nil {
		return nil
	}
	return NewSession(b.upstream, b.policy)
}

// NewSession binds an upstream connection to a policy.
func NewSession(upstream net.Conn, policy Policy) *Session {
	return &Session{upstream: upstream, policy: policy, closed: make(chan struct{})}
}

// Close closes the upstream connection.
func (s *Session) Close() error {
	if s == nil || s.upstream == nil {
		return nil
	}
	var err error
	s.once.Do(func() {
		close(s.closed)
		err = s.upstream.Close()
	})
	return err
}

// ServeHTTP proxies the request through the session.
func (s *Session) ServeHTTP(w http.ResponseWriter, req *http.Request) error {
	if s == nil || s.upstream == nil {
		return nil
	}
	select {
	case <-s.closed:
		return net.ErrClosed
	default:
	}
	return ServeHTTP(w, req, s.upstream, s.policy)
}

// Manager tracks live sessions so they can be closed together.
type Manager struct {
	mu       sync.Mutex
	sessions map[string]*Session
}

// NewManager creates an empty Session manager.
func NewManager() *Manager {
	return &Manager{sessions: make(map[string]*Session)}
}

// Open creates, stores, and returns a session under the given key.
func (m *Manager) Open(key string, upstream net.Conn, policy Policy) *Session {
	if m == nil {
		return NewSession(upstream, policy)
	}
	session := NewSession(upstream, policy)
	m.mu.Lock()
	if m.sessions == nil {
		m.sessions = make(map[string]*Session)
	}
	if old := m.sessions[key]; old != nil {
		_ = old.Close()
	}
	m.sessions[key] = session
	m.mu.Unlock()
	return session
}

// Get returns a tracked session, if any.
func (m *Manager) Get(key string) (*Session, bool) {
	if m == nil {
		return nil, false
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.sessions[key]
	return s, ok
}

// Close removes and closes a tracked session.
func (m *Manager) Close(key string) error {
	if m == nil {
		return nil
	}
	m.mu.Lock()
	s := m.sessions[key]
	delete(m.sessions, key)
	m.mu.Unlock()
	if s == nil {
		return nil
	}
	return s.Close()
}

// CloseAll closes every tracked session.
func (m *Manager) CloseAll() error {
	if m == nil {
		return nil
	}
	m.mu.Lock()
	sessions := make([]*Session, 0, len(m.sessions))
	for key, s := range m.sessions {
		sessions = append(sessions, s)
		delete(m.sessions, key)
	}
	m.mu.Unlock()
	var firstErr error
	for _, s := range sessions {
		if err := s.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}
