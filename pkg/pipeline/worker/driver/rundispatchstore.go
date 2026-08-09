package driver

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/loykin/piper/internal/proto"
)

// RunDispatchStore durably persists the pipeline.run_dispatch payloads a
// worker has received — most importantly RunDispatch.Env (resolved
// credentials) — so a restarting worker can rebuild its local scheduler
// state for any run it hasn't finished yet. This is necessary because
// pipeline.worker_recovery_query only returns run/step DB rows
// (pipeline_yaml, params, statuses); the master deliberately never persists
// resolved env/secrets, so recovery can't reconstruct them from the DB
// alone. A run with no matching entry here after a restart (disk wiped, a
// fresh pod, etc.) can't safely resume anything not already running — see
// the scheduler's recovery path, which degrades to failing not-yet-started
// steps rather than guessing at credentials.
type RunDispatchStore struct {
	dir string
	mu  sync.Mutex
}

func NewRunDispatchStore(dir string) (*RunDispatchStore, error) {
	if dir == "" {
		return nil, fmt.Errorf("run dispatch store directory is required")
	}
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, fmt.Errorf("create run dispatch store: %w", err)
	}
	return &RunDispatchStore{dir: dir}, nil
}

// Save atomically persists dispatch, keyed by its RunID. Must be called
// before any step of the run is started, so a crash immediately after
// receiving pipeline.run_dispatch never loses the env needed to resume.
func (s *RunDispatchStore) Save(dispatch proto.RunDispatch) error {
	if dispatch.RunID == "" {
		return fmt.Errorf("run dispatch RunID is required")
	}
	data, err := json.Marshal(dispatch)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return writeAtomic(s.path(dispatch.RunID), data)
}

// Load returns the persisted RunDispatch for runID. ok is false if nothing
// is stored for it (never received, or already Delete-d after the run
// reached a terminal state).
func (s *RunDispatchStore) Load(runID string) (proto.RunDispatch, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	data, err := os.ReadFile(s.path(runID))
	if os.IsNotExist(err) {
		return proto.RunDispatch{}, false, nil
	}
	if err != nil {
		return proto.RunDispatch{}, false, err
	}
	var dispatch proto.RunDispatch
	if err := json.Unmarshal(data, &dispatch); err != nil {
		return proto.RunDispatch{}, false, err
	}
	return dispatch, true, nil
}

// LoadAll returns every persisted RunDispatch, keyed by RunID — used at
// worker startup to pair against pipeline.worker_recovery_query's response.
// A malformed entry is skipped (logged by the caller via the returned map
// simply omitting it) rather than failing the whole load.
func (s *RunDispatchStore) LoadAll() (map[string]proto.RunDispatch, error) {
	s.mu.Lock()
	entries, err := os.ReadDir(s.dir)
	s.mu.Unlock()
	if err != nil {
		return nil, err
	}
	out := make(map[string]proto.RunDispatch, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(s.dir, entry.Name()))
		if err != nil {
			continue
		}
		var dispatch proto.RunDispatch
		if err := json.Unmarshal(data, &dispatch); err != nil {
			continue
		}
		if dispatch.RunID == "" {
			continue
		}
		out[dispatch.RunID] = dispatch
	}
	return out, nil
}

// Delete removes a run's persisted dispatch once it reaches a terminal
// state — its env is no longer needed and shouldn't linger on disk.
func (s *RunDispatchStore) Delete(runID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	err := os.Remove(s.path(runID))
	if os.IsNotExist(err) {
		return nil
	}
	return err
}

func (s *RunDispatchStore) path(runID string) string {
	sum := sha256.Sum256([]byte(runID))
	return filepath.Join(s.dir, hex.EncodeToString(sum[:])+".json")
}
