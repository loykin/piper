package piper

import (
	"context"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/piper/piper/pkg/storage"
)

// StorageSettingsView exposes the editable storage configuration together with
// the current runtime capability state.
type StorageSettingsView struct {
	ConfigPath      string                `json:"config_path"`
	Config          StorageConfig         `json:"config"`
	Effective       ArtifactStoreSettings `json:"effective"`
	RestartRequired bool                  `json:"restart_required"`
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
	path := p.storageSettingsPath()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	raw, err := yaml.Marshal(storageSettingsFile{Storage: cfg})
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, raw, 0600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
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

// ListStorageObjects returns a prefix-filtered object listing from the current store.
func (p *Piper) ListStorageObjects(ctx context.Context, prefix string) ([]StorageObjectInfo, error) {
	if p.store == nil {
		return nil, fmt.Errorf("artifact store is unavailable")
	}
	objs, err := p.store.List(ctx, prefix)
	if err != nil {
		return nil, err
	}
	sort.Slice(objs, func(i, j int) bool { return objs[i].Key < objs[j].Key })
	out := make([]StorageObjectInfo, 0, len(objs))
	for _, obj := range objs {
		out = append(out, StorageObjectInfo{
			Key:         obj.Key,
			Size:        obj.Size,
			ModifiedAt:  obj.ModifiedAt.UTC().Format(time.RFC3339),
			DownloadURL: "/api/storage/object?key=" + url.QueryEscape(obj.Key),
		})
	}
	return out, nil
}

// OpenStorageObject opens an object for download.
func (p *Piper) OpenStorageObject(ctx context.Context, key string) (io.ReadCloser, string, error) {
	if p.store == nil {
		return nil, "", fmt.Errorf("artifact store is unavailable")
	}
	rc, err := p.store.Get(ctx, key)
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
	return p.store.Delete(ctx, keys...)
}

// UploadStorageObject stores a single uploaded file under the given key.
func (p *Piper) UploadStorageObject(ctx context.Context, key string, r io.Reader, size int64) error {
	if p.store == nil {
		return fmt.Errorf("artifact store is unavailable")
	}
	if key == "" {
		return fmt.Errorf("missing key")
	}
	return p.store.Put(ctx, key, r, size)
}

func (p *Piper) storageBackendName() string {
	if p.store == nil {
		return ""
	}
	switch p.store.(type) {
	case *storage.LocalStore:
		return "file"
	case *storage.HTTPStore:
		return "http"
	case *storage.S3Store:
		return "s3"
	case *storage.CloudStore:
		return "cloud"
	default:
		return ""
	}
}
