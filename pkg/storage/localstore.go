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

// mkdirAllBeneathExistingRoot finds the nearest existing ancestor and uses an
// os.Root capability for the remaining creation. This preserves support for a
// new nested store directory without performing filesystem mutations through
// an unchecked request-derived path.
func mkdirAllBeneathExistingRoot(target string, perm os.FileMode) error {
	ancestor := filepath.Clean(target)
	for {
		info, err := os.Stat(ancestor)
		if err == nil {
			if !info.IsDir() {
				return fmt.Errorf("storage: root ancestor %q is not a directory", ancestor)
			}
			break
		}
		if !os.IsNotExist(err) {
			return err
		}
		parent := filepath.Dir(ancestor)
		if parent == ancestor {
			return err
		}
		ancestor = parent
	}
	realAncestor, err := filepath.EvalSymlinks(ancestor)
	if err != nil {
		return err
	}
	rel, err := filepath.Rel(ancestor, filepath.Clean(target))
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return fmt.Errorf("storage: invalid root path %q", target)
	}
	root, err := os.OpenRoot(realAncestor)
	if err != nil {
		return err
	}
	defer func() { _ = root.Close() }()
	if rel == "." {
		return nil
	}
	return root.MkdirAll(rel, perm)
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
