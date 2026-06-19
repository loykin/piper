package viewer

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/piper/piper/pkg/storage"
)

// Manager orchestrates viewer lifecycle: start, stop, TTL cleanup, and artifact materialization.
type Manager struct {
	repo      Repository
	drivers   map[string]Driver
	store     storage.Store // nil for local-only deployments
	outputDir string
}

func NewManager(repo Repository, store storage.Store, outputDir string) *Manager {
	return &Manager{
		repo:      repo,
		drivers:   make(map[string]Driver),
		store:     store,
		outputDir: outputDir,
	}
}

func (m *Manager) RegisterDriver(d Driver) {
	m.drivers[d.Type()] = d
}

// Open finds an existing running viewer or starts a new one.
func (m *Manager) Open(ctx context.Context, projectID, runID, stepName, artifact, typ string) (*Viewer, error) {
	existing, err := m.repo.FindRunning(ctx, projectID, runID, stepName, artifact, typ)
	if err == nil && existing != nil {
		return existing, nil
	}

	d, ok := m.drivers[typ]
	if !ok {
		return nil, fmt.Errorf("unsupported viewer type %q", typ)
	}

	now := time.Now().UTC()
	exp := now.Add(DefaultTTL)
	v := &Viewer{
		ID:        genID(),
		ProjectID: projectID,
		Type:      typ,
		RunID:     runID,
		StepName:  stepName,
		Artifact:  artifact,
		Status:    StatusStarting,
		CreatedAt: now,
		UpdatedAt: now,
		ExpiresAt: &exp,
	}

	if err := m.repo.Create(ctx, v); err != nil {
		return nil, fmt.Errorf("create viewer record: %w", err)
	}

	localPath, tempDir, err := m.materialize(ctx, v)
	if err != nil {
		_ = m.repo.UpdateStatus(ctx, v.ID, StatusFailed, "", 0, "")
		return nil, fmt.Errorf("materialize artifact: %w", err)
	}
	v.WorkDir = tempDir // temp dir to clean up on stop (empty for local storage)

	if err := d.Start(ctx, v, localPath); err != nil {
		if tempDir != "" {
			_ = os.RemoveAll(tempDir)
		}
		_ = m.repo.UpdateStatus(ctx, v.ID, StatusFailed, "", 0, "")
		return nil, fmt.Errorf("start viewer: %w", err)
	}

	if err := m.repo.UpdateStatus(ctx, v.ID, StatusRunning, v.Endpoint, v.PID, v.WorkDir); err != nil {
		if d, ok := m.drivers[v.Type]; ok {
			_ = d.Stop(ctx, v)
		}
		if tempDir != "" {
			_ = os.RemoveAll(tempDir)
		}
		return nil, err
	}
	v.Status = StatusRunning
	return v, nil
}

// Stop kills the viewer process and cleans up resources.
func (m *Manager) Stop(ctx context.Context, id string) error {
	v, err := m.repo.Get(ctx, id)
	if err != nil {
		return err
	}
	if d, ok := m.drivers[v.Type]; ok {
		if err := d.Stop(ctx, v); err != nil {
			slog.Warn("viewer stop error", "id", id, "err", err)
		}
	}
	if v.WorkDir != "" {
		_ = os.RemoveAll(v.WorkDir)
	}
	return m.repo.Delete(ctx, id)
}

// CleanupExpired stops all viewers past their TTL.
func (m *Manager) CleanupExpired(ctx context.Context) {
	expired, err := m.repo.ListExpired(ctx)
	if err != nil {
		slog.Warn("viewer cleanup: list expired", "err", err)
		return
	}
	for _, v := range expired {
		if err := m.Stop(ctx, v.ID); err != nil {
			slog.Warn("viewer cleanup: stop", "id", v.ID, "err", err)
		}
	}
}

// MarkStaleFailed is called on server startup to reset viewers whose processes died.
func (m *Manager) MarkStaleFailed(ctx context.Context) {
	if err := m.repo.MarkStaleFailed(ctx); err != nil {
		slog.Warn("viewer: mark stale failed", "err", err)
	}
}

// materialize returns the local filesystem path for the artifact.
// For local storage, it points directly to outputDir; no temp dir is created.
// For object storage, files are downloaded to a temp dir (returned as second value).
func (m *Manager) materialize(ctx context.Context, v *Viewer) (localPath, tempDir string, err error) {
	if m.store == nil {
		// Local filesystem: outputDir/runID/stepName/artifact/
		return filepath.Join(m.outputDir, v.RunID, v.StepName, v.Artifact), "", nil
	}

	prefix := v.RunID + "/" + v.StepName + "/" + v.Artifact + "/"
	objs, err := m.store.List(ctx, prefix)
	if err != nil {
		return "", "", fmt.Errorf("list objects: %w", err)
	}

	tmp, err := os.MkdirTemp("", "piper-viewer-"+v.ID+"-")
	if err != nil {
		return "", "", err
	}

	for _, obj := range objs {
		rel := obj.Key[len(prefix):]
		if rel == "" {
			continue
		}
		dst := filepath.Join(tmp, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			_ = os.RemoveAll(tmp)
			return "", "", err
		}
		rc, err := m.store.Get(ctx, obj.Key)
		if err != nil {
			_ = os.RemoveAll(tmp)
			return "", "", err
		}
		f, err := os.Create(dst)
		if err != nil {
			_ = rc.Close()
			_ = os.RemoveAll(tmp)
			return "", "", err
		}
		_, copyErr := io.Copy(f, rc)
		_ = rc.Close()
		_ = f.Close()
		if copyErr != nil {
			_ = os.RemoveAll(tmp)
			return "", "", copyErr
		}
	}

	return tmp, tmp, nil
}

func genID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return "viewer-" + hex.EncodeToString(b)
}
