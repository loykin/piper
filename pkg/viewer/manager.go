package viewer

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/loykin/piper/internal/memberclient"
	"github.com/loykin/piper/pkg/storage"
)

// RunStorageLookup returns the storage-identity stamp recorded on runID's
// row (see pkg/pipeline/run.Run.StorageBackend) — the narrow slice of the
// run repository a Manager needs to diagnose a mismatch, without pulling in
// pkg/pipeline/run.Repository's full querying surface. Returns ("", nil)
// when the run itself can't be found.
type RunStorageLookup func(ctx context.Context, projectID, runID string) (string, error)

// Manager orchestrates viewer lifecycle: start, stop, TTL cleanup, and artifact materialization.
type Manager struct {
	repo      Repository
	drivers   map[string]Driver
	store     storage.Store // nil for local-only deployments
	outputDir string

	// runStorage and liveIdentity are wired post-construction via
	// SetStorageDiagnostics (mirrors *queue.Queue's own post-New() setter
	// idiom). runStorage nil means no diagnostic is available — Open behaves
	// exactly as it did before this field existed.
	runStorage   RunStorageLookup
	liveIdentity string
}

func NewManager(repo Repository, store storage.Store, outputDir string) *Manager {
	return &Manager{
		repo:      repo,
		drivers:   make(map[string]Driver),
		store:     store,
		outputDir: outputDir,
	}
}

// SetStorageDiagnostics wires the run-storage-backend lookup and the live
// storage identity so a materialize/Start failure can distinguish "artifact
// legitimately absent" from "artifact unreachable because the storage
// backend changed since this run's data was written" (see docs on the
// storage-identity stamp). Optional — a Manager without this wired behaves
// exactly as before, with no mismatch diagnostic.
func (m *Manager) SetStorageDiagnostics(lookup RunStorageLookup, liveIdentity string) {
	m.runStorage = lookup
	m.liveIdentity = liveIdentity
}

// storageBackendMismatch reports whether v's run was created under a
// storage backend that's no longer the live one, using the Run's own stamp
// — a viewer never has an independent stamp, since it always resolves
// through a Run's artifacts (see RunID/StepName/Artifact in materialize). A
// run with no stamp (predates this feature) or whose stamp matches the live
// backend never mismatches.
func (m *Manager) storageBackendMismatch(ctx context.Context, v *Viewer) bool {
	if m.runStorage == nil {
		return false
	}
	backend, err := m.runStorage(ctx, v.ProjectID, v.RunID)
	if err != nil || backend == "" {
		return false
	}
	return backend != m.liveIdentity
}

func (m *Manager) RegisterDriver(d Driver) {
	m.drivers[d.Type()] = d
}

// Open finds an existing running viewer or starts a new one.
func (m *Manager) Open(ctx context.Context, projectID, runID, stepName, artifact, typ string) (*Viewer, bool, error) {
	existing, err := m.repo.FindRunning(ctx, projectID, runID, stepName, artifact, typ)
	if err == nil && existing != nil {
		return existing, false, nil
	}

	d, ok := m.drivers[typ]
	if !ok {
		return nil, false, fmt.Errorf("unsupported viewer type %q", typ)
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
		return nil, false, fmt.Errorf("create viewer record: %w", err)
	}

	localPath, tempDir, err := m.materialize(ctx, v)
	if err != nil {
		_ = m.repo.UpdateStatus(ctx, v.ID, StatusFailed, "", 0, "")
		if m.storageBackendMismatch(ctx, v) {
			return nil, false, fmt.Errorf("materialize artifact: %w", memberclient.ErrStorageBackendMismatch)
		}
		return nil, false, fmt.Errorf("materialize artifact: %w", err)
	}
	v.WorkDir = tempDir // temp dir to clean up on stop (empty for local storage)

	if err := d.Start(ctx, v, localPath); err != nil {
		if tempDir != "" {
			_ = os.RemoveAll(tempDir)
		}
		_ = m.repo.UpdateStatus(ctx, v.ID, StatusFailed, "", 0, "")
		if m.storageBackendMismatch(ctx, v) {
			return nil, false, fmt.Errorf("start viewer: %w", memberclient.ErrStorageBackendMismatch)
		}
		return nil, false, fmt.Errorf("start viewer: %w", err)
	}

	if err := m.repo.UpdateStatus(ctx, v.ID, StatusRunning, v.Endpoint, v.PID, v.WorkDir); err != nil {
		if d, ok := m.drivers[v.Type]; ok {
			_ = d.Stop(ctx, v)
		}
		if tempDir != "" {
			_ = os.RemoveAll(tempDir)
		}
		return nil, false, err
	}
	v.Status = StatusRunning
	return v, true, nil
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
		return m.materializeLocal(v)
	}

	prefix := v.RunID + "/" + v.StepName + "/" + v.Artifact + "/"
	objs, err := m.store.List(ctx, prefix, "")
	if err != nil {
		return "", "", fmt.Errorf("list objects: %w", err)
	}

	tmp, err := os.MkdirTemp("", "piper-viewer-"+v.ID+"-")
	if err != nil {
		return "", "", err
	}
	destination, err := os.OpenRoot(tmp)
	if err != nil {
		_ = os.RemoveAll(tmp)
		return "", "", err
	}
	defer func() { _ = destination.Close() }()

	for _, obj := range objs {
		if !strings.HasPrefix(obj.Key, prefix) {
			_ = os.RemoveAll(tmp)
			return "", "", fmt.Errorf("object key %q is outside requested prefix", obj.Key)
		}
		rel := strings.TrimPrefix(obj.Key, prefix)
		if rel == "" {
			continue
		}
		name, err := cleanMaterializedPath(rel)
		if err != nil {
			_ = os.RemoveAll(tmp)
			return "", "", fmt.Errorf("unsafe object key %q: %w", obj.Key, err)
		}
		if err := destination.MkdirAll(filepath.Dir(name), 0o755); err != nil {
			_ = os.RemoveAll(tmp)
			return "", "", err
		}
		rc, err := m.store.Get(ctx, obj.Key)
		if err != nil {
			_ = os.RemoveAll(tmp)
			return "", "", err
		}
		f, err := destination.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
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

func (m *Manager) materializeLocal(v *Viewer) (string, string, error) {
	sourceRoot, err := os.OpenRoot(m.outputDir)
	if err != nil {
		return "", "", err
	}
	defer func() { _ = sourceRoot.Close() }()
	rel, err := cleanMaterializedPath(path.Join(v.RunID, v.StepName, v.Artifact))
	if err != nil {
		return "", "", err
	}
	source, err := sourceRoot.OpenRoot(rel)
	if os.IsNotExist(err) {
		// Preserve the historical lazy-start behavior for drivers that can
		// tolerate an artifact appearing after startup, without handing them an
		// unchecked request-derived host path.
		return sourceRoot.Name(), "", nil
	}
	if err != nil {
		return "", "", err
	}
	defer func() { _ = source.Close() }()
	return source.Name(), "", nil
}

func cleanMaterializedPath(value string) (string, error) {
	raw := strings.ReplaceAll(strings.TrimSpace(value), `\`, "/")
	if strings.HasPrefix(raw, "/") {
		return "", fmt.Errorf("path must be relative")
	}
	clean := path.Clean(raw)
	if clean == "." || clean == ".." || strings.HasPrefix(clean, "../") {
		return "", fmt.Errorf("path escapes materialization root")
	}
	return filepath.FromSlash(clean), nil
}

func genID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return "viewer-" + hex.EncodeToString(b)
}
