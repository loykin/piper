package statsstore

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

var ErrSpoolFull = errors.New("statistics spool is full")

type spoolRecord struct {
	Kind    string        `json:"kind"`
	Logs    []LogLine     `json:"logs,omitempty"`
	Metrics []MetricPoint `json:"metrics,omitempty"`
}

// diskSpool persists each append batch as one atomically-renamed JSON file.
// Acknowledging a batch is the only operation that removes it, so crashes at
// any point produce at-least-once delivery. EventID makes replay idempotent.
type diskSpool struct {
	dir      string
	maxBytes int64

	mu     sync.Mutex
	bytes  int64
	nextID int64
	serial uint64
}

func openDiskSpool(dir string, maxBytes int64) (*diskSpool, error) {
	if strings.TrimSpace(dir) == "" {
		return nil, fmt.Errorf("statistics spool directory is required for an external backend")
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("create statistics spool: %w", err)
	}
	s := &diskSpool{dir: dir, maxBytes: maxBytes}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("scan statistics spool: %w", err)
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			return nil, err
		}
		s.bytes += info.Size()
	}
	if raw, err := os.ReadFile(filepath.Join(dir, "sequence")); err == nil {
		s.nextID, _ = strconv.ParseInt(strings.TrimSpace(string(raw)), 10, 64)
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("read statistics sequence: %w", err)
	}
	return s, nil
}

func (s *diskSpool) assignLogs(lines []LogLine) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range lines {
		if lines[i].ID <= 0 {
			s.nextID++
			lines[i].ID = s.nextID
		} else if lines[i].ID > s.nextID {
			s.nextID = lines[i].ID
		}
		if lines[i].EventID == "" {
			lines[i].EventID = newEventID()
		}
	}
	return s.persistSequenceLocked()
}

func (s *diskSpool) assignMetrics(points []MetricPoint) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range points {
		if points[i].ID <= 0 {
			s.nextID++
			points[i].ID = s.nextID
		} else if points[i].ID > s.nextID {
			s.nextID = points[i].ID
		}
		if points[i].EventID == "" {
			points[i].EventID = newEventID()
		}
	}
	return s.persistSequenceLocked()
}

func (s *diskSpool) persistSequenceLocked() error {
	if err := writeAtomic(filepath.Join(s.dir, "sequence"), []byte(strconv.FormatInt(s.nextID, 10)+"\n")); err != nil {
		return err
	}
	return syncDir(s.dir)
}

func (s *diskSpool) put(record spoolRecord) (string, error) {
	data, err := json.Marshal(record)
	if err != nil {
		return "", err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.maxBytes > 0 && s.bytes+int64(len(data)) > s.maxBytes {
		return "", ErrSpoolFull
	}
	s.serial++
	name := fmt.Sprintf("%020d-%06d-%s.json", time.Now().UTC().UnixNano(), s.serial, record.Kind)
	tmp := filepath.Join(s.dir, "."+name+".tmp")
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return "", err
	}
	if _, err = f.Write(data); err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		_ = os.Remove(tmp)
		return "", err
	}
	if err := os.Rename(tmp, filepath.Join(s.dir, name)); err != nil {
		_ = os.Remove(tmp)
		return "", err
	}
	if err := syncDir(s.dir); err != nil {
		return "", err
	}
	s.bytes += int64(len(data))
	return name, nil
}

func (s *diskSpool) records(kind string) ([]namedSpoolRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		return nil, err
	}
	var result []namedSpoolRecord
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), "-"+kind+".json") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(s.dir, entry.Name()))
		if err != nil {
			return nil, err
		}
		var record spoolRecord
		if err := json.Unmarshal(data, &record); err != nil {
			return nil, fmt.Errorf("decode statistics spool record %s: %w", entry.Name(), err)
		}
		result = append(result, namedSpoolRecord{name: entry.Name(), size: int64(len(data)), record: record})
	}
	sort.Slice(result, func(i, j int) bool { return result[i].name < result[j].name })
	return result, nil
}

type namedSpoolRecord struct {
	name   string
	size   int64
	record spoolRecord
}

func (s *diskSpool) ack(record namedSpoolRecord) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := os.Remove(filepath.Join(s.dir, record.name)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	s.bytes -= record.size
	if s.bytes < 0 {
		s.bytes = 0
	}
	return syncDir(s.dir)
}

func (s *diskSpool) purge(projectID, runID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		path := filepath.Join(s.dir, entry.Name())
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		var record spoolRecord
		if err := json.Unmarshal(data, &record); err != nil {
			return err
		}
		logs := record.Logs[:0]
		for _, line := range record.Logs {
			if line.ProjectID != projectID || (runID != "" && line.RunID != runID) {
				logs = append(logs, line)
			}
		}
		record.Logs = logs
		metrics := record.Metrics[:0]
		for _, point := range record.Metrics {
			if point.ProjectID != projectID || (runID != "" && point.RunID != runID) {
				metrics = append(metrics, point)
			}
		}
		record.Metrics = metrics
		if len(record.Logs) == 0 && len(record.Metrics) == 0 {
			if err := os.Remove(path); err != nil {
				return err
			}
			s.bytes -= int64(len(data))
			continue
		}
		updated, err := json.Marshal(record)
		if err != nil {
			return err
		}
		if err = writeAtomic(path, updated); err != nil {
			return err
		}
		s.bytes += int64(len(updated) - len(data))
	}
	if s.bytes < 0 {
		s.bytes = 0
	}
	return syncDir(s.dir)
}

func writeAtomic(path string, data []byte) error {
	tmp := path + ".tmp"
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	if _, err = f.Write(data); err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		_ = os.Remove(tmp)
		return err
	}
	if err = os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

func syncDir(dir string) error {
	f, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer f.Close()
	return f.Sync()
}
