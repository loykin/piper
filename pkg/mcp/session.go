package mcp

import (
	"crypto/rand"
	"encoding/hex"
	"sync"
	"time"
)

// SessionIDHeader is the Streamable HTTP transport's session header name
// (design doc §8.1: "서버가 session ID를 발급하면 ... Mcp-Session-Id 응답
// 헤더로 반환한다").
const SessionIDHeader = "Mcp-Session-Id"

// Session is transport-level bookkeeping only — which identity, project,
// and MCP client a session ID belongs to, plus its expiry. It deliberately
// carries no Piper domain state (design doc §8.1: "도메인 실행 상태는 MCP
// session 메모리에 저장하지 않는다") — a NotebookExecution lives in the DB and
// keeps running/is queryable no matter what happens to this session.
type Session struct {
	ID         string
	IdentityID string
	ProjectID  string
	ClientID   string
	CreatedAt  time.Time
	ExpiresAt  time.Time
}

// Expired reports whether the session's TTL has elapsed as of now.
func (s *Session) Expired(now time.Time) bool {
	return !now.Before(s.ExpiresAt)
}

// SessionStore is an in-memory, TTL-expiring map of MCP transport sessions
// (design doc §8.1: "in-memory map + expiry sweep is fine"). It is plain
// data with no background goroutine: expiry is enforced lazily on Get, and
// Create opportunistically sweeps already-expired entries so the map
// doesn't grow unboundedly between accesses — judgment call documented on
// NewSessionStore.
type SessionStore struct {
	mu       sync.Mutex
	ttl      time.Duration
	sessions map[string]*Session
	now      func() time.Time
}

// NewSessionStore constructs a SessionStore with the given TTL.
//
// Judgment call: the design doc allows "in-memory map + expiry sweep" and
// doesn't mandate a dedicated background sweeper goroutine. A goroutine
// would need to be threaded through Piper's own lifecycle (bgCtx/Shutdown,
// mirroring execution.Service's pattern) purely to bound memory for a map
// whose entries are already cheap (a handful of short strings + two
// timestamps) and already self-limiting (one entry per active MCP
// connection). Lazy expiry-on-Get plus an opportunistic sweep on every
// Create keeps the map bounded without that extra lifecycle wiring; Get
// still always re-validates ExpiresAt so a session past its TTL is never
// usable even if the sweep hasn't run yet.
func NewSessionStore(ttl time.Duration) *SessionStore {
	if ttl <= 0 {
		ttl = 30 * time.Minute
	}
	return &SessionStore{ttl: ttl, sessions: map[string]*Session{}, now: time.Now}
}

// Create issues a new session bound to identityID/projectID/clientID and
// returns it. The session ID is a random 32-byte hex string.
func (s *SessionStore) Create(identityID, projectID, clientID string) (*Session, error) {
	id, err := newSessionID()
	if err != nil {
		return nil, err
	}
	now := s.now()
	sess := &Session{
		ID:         id,
		IdentityID: identityID,
		ProjectID:  projectID,
		ClientID:   clientID,
		CreatedAt:  now,
		ExpiresAt:  now.Add(s.ttl),
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sweepLocked(now)
	s.sessions[id] = sess
	return sess, nil
}

// Get returns the session for id, or (nil, false) if it doesn't exist or
// has expired (an expired entry is deleted as a side effect).
func (s *SessionStore) Get(id string) (*Session, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	sess, ok := s.sessions[id]
	if !ok {
		return nil, false
	}
	now := s.now()
	if sess.Expired(now) {
		delete(s.sessions, id)
		return nil, false
	}
	return sess, true
}

// Touch extends id's expiry by the store's TTL from now, if it still
// exists and hasn't expired. Used to keep a session alive across repeated
// use, matching the design doc's TTL description.
func (s *SessionStore) Touch(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	sess, ok := s.sessions[id]
	if !ok {
		return
	}
	now := s.now()
	if sess.Expired(now) {
		delete(s.sessions, id)
		return
	}
	sess.ExpiresAt = now.Add(s.ttl)
}

// Delete removes a session (no-op if absent).
func (s *SessionStore) Delete(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.sessions, id)
}

// Count returns the number of non-expired sessions currently stored — for
// tests and metrics (design doc §13.3's piper_mcp_active_sessions).
func (s *SessionStore) Count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	s.sweepLocked(now)
	return len(s.sessions)
}

func (s *SessionStore) sweepLocked(now time.Time) {
	for id, sess := range s.sessions {
		if sess.Expired(now) {
			delete(s.sessions, id)
		}
	}
}

func newSessionID() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}
