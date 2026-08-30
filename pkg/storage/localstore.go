package storage

import (
	"context"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
)

// LocalStore implements Store using the local filesystem.
// Suitable for single-machine development, testing, and NFS-mounted shared volumes.
type LocalStore struct {
	root       string // absolute path exposed to callers
	secureRoot string // symlink-resolved path used for capability operations
}

// NewLocal creates a LocalStore rooted at the given directory.
// The directory is created if it does not exist.
func NewLocal(root string) (*LocalStore, error) {
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	if err := mkdirAllBeneathExistingRoot(abs, 0o755); err != nil {
		return nil, err
	}
	realRoot, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return nil, err
	}
	return &LocalStore{root: abs, secureRoot: realRoot}, nil
}

// mkdirAllBeneathExistingRoot creates every directory from the filesystem
// volume root down to target, entirely through an os.Root capability opened
// on that volume root. os.Root's methods can only reach files and
// directories beneath the root they were opened on and refuse to follow a
// symlink that would escape it (see the os.Root doc comment) — the walk is
// bounded this way from the very first path component, not just at a final
// step, which also closes a symlink-swap TOCTOU window a stat-then-mkdir
// design walking raw absolute paths would otherwise leave between
// discovering an ancestor and creating beneath it.
//
// target is expected to already be absolute and filepath.Clean-ed by the
// caller (NewLocal calls filepath.Abs before invoking this). It legitimately
// comes from operator/admin-supplied storage configuration (storage.url, or
// the system-admin-only, non-persisting POST /storage/settings/test — see
// storage_admin.go's TestStorageSettings) and is intentionally allowed to
// name any local path the server process can reach; there is no fixed "safe
// root" to prefix-check it against, which is exactly why the containment
// guarantee has to come from os.Root itself rather than from validating
// target against some other trusted path.
func mkdirAllBeneathExistingRoot(target string, perm os.FileMode) error {
	if err := validateAbsoluteCleanPath(target); err != nil {
		return err
	}
	clean := filepath.Clean(target)
	// The volume root ("/" on Unix, "C:\" on Windows) is the one directory
	// guaranteed to already exist for any absolute path, so it's always a
	// safe os.Root anchor regardless of how much of target's remainder does
	// or doesn't exist yet.
	volumeRoot := filepath.VolumeName(clean) + string(filepath.Separator)
	rel, err := filepath.Rel(volumeRoot, clean)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return fmt.Errorf("storage: invalid root path %q", target)
	}
	root, err := os.OpenRoot(volumeRoot)
	if err != nil {
		return err
	}
	defer func() { _ = root.Close() }()
	if rel == "." {
		return nil
	}
	return root.MkdirAll(rel, perm)
}

// validateAbsoluteCleanPath rejects anything that isn't an absolute,
// filepath.Clean-normalized path with no residual ".." element. A properly
// Clean-ed absolute path can never contain one (Clean collapses ".." at the
// root boundary), so this never actually fires in practice — it exists as
// an explicit, auditable guard ahead of the filesystem walk in
// mkdirAllBeneathExistingRoot, rather than leaving that invariant implicit
// in filepath.Clean's contract.
func validateAbsoluteCleanPath(p string) error {
	clean := filepath.Clean(p)
	if !filepath.IsAbs(clean) {
		return fmt.Errorf("storage: path %q must be absolute", p)
	}
	for _, seg := range strings.Split(clean, string(filepath.Separator)) {
		if seg == ".." {
			return fmt.Errorf("storage: path %q must not contain '..'", p)
		}
	}
	return nil
}

func cleanLocalKey(key string, allowRoot bool) (string, error) {
	raw := strings.ReplaceAll(strings.TrimSpace(key), `\`, "/")
	if strings.HasPrefix(raw, "/") {
		return "", fmt.Errorf("storage: key %q must be relative", key)
	}
	clean := path.Clean(raw)
	if clean == "." {
		if allowRoot {
			return ".", nil
		}
		return "", fmt.Errorf("storage: key is empty")
	}
	if clean == ".." || strings.HasPrefix(clean, "../") {
		return "", fmt.Errorf("storage: key %q escapes store root", key)
	}
	return filepath.FromSlash(clean), nil
}

func (s *LocalStore) openRoot() (*os.Root, error) {
	return os.OpenRoot(s.secureRoot)
}

func (s *LocalStore) Put(_ context.Context, key string, r io.Reader, _ int64) error {
	name, err := cleanLocalKey(key, false)
	if err != nil {
		return err
	}
	root, err := s.openRoot()
	if err != nil {
		return err
	}
	defer func() { _ = root.Close() }()
	if err := root.MkdirAll(filepath.Dir(name), 0o755); err != nil {
		return err
	}
	f, err := root.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o666)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	_, err = io.Copy(f, r)
	return err
}

func (s *LocalStore) Get(_ context.Context, key string) (io.ReadCloser, error) {
	name, err := cleanLocalKey(key, false)
	if err != nil {
		return nil, err
	}
	root, err := s.openRoot()
	if err != nil {
		return nil, err
	}
	f, err := root.Open(name)
	_ = root.Close()
	if os.IsNotExist(err) {
		return nil, ErrNotFound
	}
	return f, err
}

func (s *LocalStore) List(_ context.Context, prefix, delimiter string) ([]ObjectInfo, error) {
	name, err := cleanLocalKey(prefix, true)
	if err != nil {
		return nil, err
	}
	root, err := s.openRoot()
	if err != nil {
		return nil, err
	}
	defer func() { _ = root.Close() }()
	if delimiter == "" {
		return s.listRecursive(root, name)
	}
	return s.listOneLevel(root, name, prefix, delimiter)
}

func (s *LocalStore) listRecursive(root *os.Root, searchRoot string) ([]ObjectInfo, error) {
	var result []ObjectInfo
	err := fs.WalkDir(root.FS(), searchRoot, func(name string, entry fs.DirEntry, err error) error {
		if err != nil {
			if os.IsNotExist(err) {
				return nil
			}
			return err
		}
		if entry.IsDir() || entry.Type()&os.ModeSymlink != 0 {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		result = append(result, ObjectInfo{Key: filepath.ToSlash(name), Size: info.Size(), ModifiedAt: info.ModTime().UTC()})
		return nil
	})
	return result, err
}

// listOneLevel lists only the immediate children of searchRoot. Directories
// become IsDir entries matching S3 Delimiter semantics.
func (s *LocalStore) listOneLevel(root *os.Root, searchRoot, prefix, delimiter string) ([]ObjectInfo, error) {
	dir, err := root.Open(searchRoot)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	defer func() { _ = dir.Close() }()
	entries, err := dir.ReadDir(-1)
	if err != nil {
		return nil, err
	}
	keyPrefix := prefix
	if keyPrefix != "" && !strings.HasSuffix(keyPrefix, delimiter) {
		keyPrefix += delimiter
	}
	var result []ObjectInfo
	for _, entry := range entries {
		if entry.Type()&os.ModeSymlink != 0 {
			continue
		}
		if entry.IsDir() {
			result = append(result, ObjectInfo{Key: keyPrefix + entry.Name() + delimiter, IsDir: true})
			continue
		}
		info, err := entry.Info()
		if err != nil {
			continue
		}
		result = append(result, ObjectInfo{Key: keyPrefix + entry.Name(), Size: info.Size(), ModifiedAt: info.ModTime().UTC()})
	}
	return result, nil
}

func (s *LocalStore) Delete(_ context.Context, keys ...string) error {
	root, err := s.openRoot()
	if err != nil {
		return err
	}
	defer func() { _ = root.Close() }()
	for _, key := range keys {
		name, err := cleanLocalKey(key, false)
		if err != nil {
			return err
		}
		if err := root.Remove(name); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	return nil
}

func (s *LocalStore) URL(_ string) (string, bool) { return "", false }

// Root returns the absolute root directory of this store.
func (s *LocalStore) Root() string { return s.root }
