package piper

import (
	"context"
	"fmt"
	"io"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/loykin/piper/pkg/project"
	"github.com/loykin/piper/pkg/storage"
)

// StorageSettingsView exposes the editable storage configuration together with
// the current runtime capability state.
type StorageSettingsView struct {
	ConfigPath      string                `json:"config_path"`
	Config          StorageConfig         `json:"config"`
	Effective       ArtifactStoreSettings `json:"effective"`
	RestartRequired bool                  `json:"restart_required"`
}

// StorageTestResult reports whether a candidate storage configuration is
// actually reachable, without persisting it or restarting the server.
type StorageTestResult struct {
	OK      bool   `json:"ok"`
	Message string `json:"message"`
}

// StorageObjectInfo exposes object store contents to the UI.
type StorageObjectInfo struct {
	Key         string `json:"key"`
	Size        int64  `json:"size"`
	ModifiedAt  string `json:"modified_at"`
	DownloadURL string `json:"download_url"`
}

type storageSettingsFile struct {
	Storage StorageConfig `yaml:"storage"`
}

func loadStorageSettings(path string, fallback StorageConfig) (StorageConfig, bool, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return fallback, false, nil
		}
		return StorageConfig{}, false, err
	}
	var file storageSettingsFile
	if err := yaml.Unmarshal(data, &file); err != nil {
		return StorageConfig{}, false, fmt.Errorf("parse %s: %w", path, err)
	}
	return file.Storage, true, nil
}

func (p *Piper) storageSettingsPath() string {
	return filepath.Join(p.cfg.OutputDir, "storage.yaml")
}

func (p *Piper) readStorageSettings() (StorageConfig, bool, error) {
	return loadStorageSettings(p.storageSettingsPath(), p.cfg.Storage)
}

func (p *Piper) writeStorageSettings(cfg StorageConfig) error {
	settingPath := p.storageSettingsPath()
	if err := os.MkdirAll(filepath.Dir(settingPath), 0755); err != nil {
		return err
	}
	raw, err := yaml.Marshal(storageSettingsFile{Storage: cfg})
	if err != nil {
		return err
	}
	tmp := settingPath + ".tmp"
	if err := os.WriteFile(tmp, raw, 0600); err != nil {
		return err
	}
	return os.Rename(tmp, settingPath)
}

// StorageSettings returns the editable storage configuration and runtime status.
func (p *Piper) StorageSettings() (StorageSettingsView, error) {
	cfg, exists, err := p.readStorageSettings()
	if err != nil {
		return StorageSettingsView{}, err
	}
	if !exists {
		cfg = p.cfg.Storage
	}
	view := StorageSettingsView{
		ConfigPath: p.storageSettingsPath(),
		Config:     cfg,
		Effective:  p.Settings().ArtifactStore,
	}
	if exists && cfg != p.cfg.Storage {
		view.RestartRequired = true
	}
	return view, nil
}

// UpdateStorageSettings persists the edited storage config for the next restart.
func (p *Piper) UpdateStorageSettings(cfg StorageConfig) (StorageSettingsView, error) {
	if err := p.writeStorageSettings(cfg); err != nil {
		return StorageSettingsView{}, err
	}
	return p.StorageSettings()
}

// TestStorageSettings opens the given candidate configuration (without
// persisting it or touching the running server's own store) and attempts a
// bounded-timeout List to confirm the backend is actually reachable — as
// opposed to just well-formed. A misconfigured but "successfully opened"
// client (e.g. an S3 client pointed at an unreachable endpoint) only fails
// on first real use, which is exactly what this forces to happen now,
// bounded, instead of on some later unbounded request.
func (p *Piper) TestStorageSettings(ctx context.Context, cfg StorageConfig) StorageTestResult {
	if cfg.Disabled {
		return StorageTestResult{OK: false, Message: "storage is disabled"}
	}
	rawURL := strings.TrimSpace(cfg.URL)
	if rawURL == "" {
		outputDir := p.cfg.OutputDir
		if outputDir == "" {
			outputDir = "./piper-outputs"
		}
		rawURL = "file://" + filepath.Join(outputDir, "store")
	}
	if cfg.CredentialRef != "" {
		injected, err := injectStorageCredential(ctx, p.credentials, rawURL, cfg.CredentialRef)
		if err != nil {
			return StorageTestResult{OK: false, Message: err.Error()}
		}
		rawURL = injected
	}

	testCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	st, err := storage.Open(rawURL, cfg.Token)
	if err != nil {
		return StorageTestResult{OK: false, Message: err.Error()}
	}
	if closer, ok := st.(interface{ Close() error }); ok {
		defer func() { _ = closer.Close() }()
	}
	if _, err := st.List(testCtx, ""); err != nil {
		return StorageTestResult{OK: false, Message: err.Error()}
	}
	return StorageTestResult{OK: true, Message: "connected"}
}

// ListStorageObjects returns a prefix-filtered object listing from the current store.
func (p *Piper) ListStorageObjects(ctx context.Context, prefix string) ([]StorageObjectInfo, error) {
	if p.store == nil {
		return nil, fmt.Errorf("artifact store is unavailable")
	}
	projectPrefix, err := projectStoragePrefix(ctx)
	if err != nil {
		return nil, err
	}
	relativePrefix, err := cleanProjectStorageKey(prefix, true)
	if err != nil {
		return nil, err
	}
	objs, err := p.store.List(ctx, projectPrefix+relativePrefix)
	if err != nil {
		return nil, err
	}
	sort.Slice(objs, func(i, j int) bool { return objs[i].Key < objs[j].Key })
	out := make([]StorageObjectInfo, 0, len(objs))
	for _, obj := range objs {
		key := strings.TrimPrefix(obj.Key, projectPrefix)
		out = append(out, StorageObjectInfo{
			Key:         key,
			Size:        obj.Size,
			ModifiedAt:  obj.ModifiedAt.UTC().Format(time.RFC3339),
			DownloadURL: "/api/projects/" + projectID(ctx) + "/storage/object?key=" + url.QueryEscape(key),
		})
	}
	return out, nil
}

// OpenStorageObject opens an object for download.
func (p *Piper) OpenStorageObject(ctx context.Context, key string) (io.ReadCloser, string, error) {
	if p.store == nil {
		return nil, "", fmt.Errorf("artifact store is unavailable")
	}
	fullKey, err := projectStorageKey(ctx, key)
	if err != nil {
		return nil, "", err
	}
	rc, err := p.store.Get(ctx, fullKey)
	if err != nil {
		return nil, "", err
	}
	return rc, filepath.Base(strings.TrimSuffix(key, "/")), nil
}

// DeleteStorageObject removes one or more objects from the current store.
func (p *Piper) DeleteStorageObject(ctx context.Context, keys ...string) error {
	if p.store == nil {
		return fmt.Errorf("artifact store is unavailable")
	}
	fullKeys := make([]string, len(keys))
	for i, key := range keys {
		fullKey, err := projectStorageKey(ctx, key)
		if err != nil {
			return err
		}
		fullKeys[i] = fullKey
	}
	return p.store.Delete(ctx, fullKeys...)
}

// UploadStorageObject stores a single uploaded file under the given key.
func (p *Piper) UploadStorageObject(ctx context.Context, key string, r io.Reader, size int64) error {
	if p.store == nil {
		return fmt.Errorf("artifact store is unavailable")
	}
	if key == "" {
		return fmt.Errorf("missing key")
	}
	fullKey, err := projectStorageKey(ctx, key)
	if err != nil {
		return err
	}
	return p.store.Put(ctx, fullKey, r, size)
}

func projectID(ctx context.Context) string {
	projectContext, _ := project.FromContext(ctx)
	return projectContext.ID
}

func projectStoragePrefix(ctx context.Context) (string, error) {
	id := projectID(ctx)
	if id == "" {
		return "", fmt.Errorf("project context is required")
	}
	return "projects/" + id + "/uploads/", nil
}

func projectStorageKey(ctx context.Context, key string) (string, error) {
	prefix, err := projectStoragePrefix(ctx)
	if err != nil {
		return "", err
	}
	cleaned, err := cleanProjectStorageKey(key, false)
	if err != nil {
		return "", err
	}
	return prefix + cleaned, nil
}

func cleanProjectStorageKey(key string, allowEmpty bool) (string, error) {
	key = strings.TrimSpace(strings.ReplaceAll(key, "\\", "/"))
	for _, segment := range strings.Split(key, "/") {
		if segment == ".." {
			return "", fmt.Errorf("invalid key")
		}
	}
	cleaned := strings.TrimPrefix(path.Clean("/"+key), "/")
	if cleaned == "." {
		cleaned = ""
	}
	if cleaned == "" && !allowEmpty {
		return "", fmt.Errorf("missing key")
	}
	return cleaned, nil
}

func (p *Piper) storageBackendName() string {
	if p.store == nil {
		return ""
	}
	switch st := p.store.(type) {
	case *storage.LocalStore:
		return "file"
	case *storage.HTTPStore:
		return "http"
	case *storage.S3Store:
		return "s3"
	case *storage.CloudStore:
		return st.Scheme()
	default:
		return ""
	}
}
