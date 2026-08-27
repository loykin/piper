package statsstore

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"sort"
	"strings"
	"time"
)

var ErrInvalidCursor = errors.New("invalid statistics cursor")

const cursorVersion byte = 1
const queryCursorVersion byte = 2

// CursorFromID encodes the stable Member sequence without exposing its wire
// representation as an API contract. Future cursor versions can carry native
// backend state while older cursors remain decodable.
func CursorFromID(id int64) string {
	if id <= 0 {
		return ""
	}
	payload := make([]byte, 9)
	payload[0] = cursorVersion
	binary.BigEndian.PutUint64(payload[1:], uint64(id))
	return base64.RawURLEncoding.EncodeToString(payload)
}

func CursorForLogQuery(id int64, query LogQuery) string {
	return cursorForQuery(id, logFingerprint(query))
}
func CursorForMetricQuery(id int64, query MetricQuery) string {
	return cursorForQuery(id, metricFingerprint(query))
}
func LogIDFromCursor(cursor string, query LogQuery) (int64, error) {
	return idFromQueryCursor(cursor, logFingerprint(query))
}
func MetricIDFromCursor(cursor string, query MetricQuery) (int64, error) {
	return idFromQueryCursor(cursor, metricFingerprint(query))
}

func cursorForQuery(id int64, fingerprint [16]byte) string {
	if id <= 0 {
		return ""
	}
	payload := make([]byte, 25)
	payload[0] = queryCursorVersion
	binary.BigEndian.PutUint64(payload[1:9], uint64(id))
	copy(payload[9:], fingerprint[:])
	return base64.RawURLEncoding.EncodeToString(payload)
}
func idFromQueryCursor(cursor string, fingerprint [16]byte) (int64, error) {
	if cursor == "" {
		return 0, nil
	}
	payload, err := base64.RawURLEncoding.DecodeString(cursor)
	if err != nil {
		return 0, ErrInvalidCursor
	}
	if len(payload) == 9 && payload[0] == cursorVersion {
		return IDFromCursor(cursor)
	}
	if len(payload) != 25 || payload[0] != queryCursorVersion || !equalBytes(payload[9:], fingerprint[:]) {
		return 0, ErrInvalidCursor
	}
	id := binary.BigEndian.Uint64(payload[1:9])
	if id == 0 || id > uint64(^uint64(0)>>1) {
		return 0, ErrInvalidCursor
	}
	return int64(id), nil
}
func equalBytes(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	var diff byte
	for i := range a {
		diff |= a[i] ^ b[i]
	}
	return diff == 0
}
func logFingerprint(q LogQuery) [16]byte {
	return fingerprint(q.ProjectID, q.RunID, q.StepName, q.Since, q.Until, q.Search, nil)
}
func metricFingerprint(q MetricQuery) [16]byte {
	keys := append([]string(nil), q.Keys...)
	sort.Strings(keys)
	return fingerprint(q.ProjectID, q.RunID, q.StepName, q.Since, q.Until, "", keys)
}
func fingerprint(projectID, runID, step string, since, until time.Time, search string, keys []string) [16]byte {
	raw := strings.Join([]string{projectID, runID, step, since.UTC().Format(time.RFC3339Nano), until.UTC().Format(time.RFC3339Nano), search, strings.Join(keys, "\x00")}, "\x1f")
	sum := sha256.Sum256([]byte(raw))
	var short [16]byte
	copy(short[:], sum[:16])
	return short
}

func IDFromCursor(cursor string) (int64, error) {
	if cursor == "" {
		return 0, nil
	}
	payload, err := base64.RawURLEncoding.DecodeString(cursor)
	if err != nil || len(payload) != 9 || payload[0] != cursorVersion {
		return 0, ErrInvalidCursor
	}
	id := binary.BigEndian.Uint64(payload[1:])
	if id == 0 || id > uint64(^uint64(0)>>1) {
		return 0, ErrInvalidCursor
	}
	return int64(id), nil
}
