package notebook

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

// WorkspaceFile describes one regular file found by WorkspaceReader.ListFiles.
type WorkspaceFile struct {
	// Rel is the file's path relative to the directory that was listed.
	Rel  string
	Size int64
}

// WorkspaceReader reads files out of a notebook volume's live workspace.
// vol.WorkDir alone isn't enough to locate those files: it is a real host
// path only when the volume has local (non-network) affinity; for network
// volumes (K8s CSI — see NotebookVolume.RuntimeID's doc comment) it is a path
// inside a remote pod's container filesystem. Implementations resolve that
// difference; callers must go through this interface rather than touching
// vol.WorkDir directly.
type WorkspaceReader interface {
	// Stat reports whether path (relative to vol's workspace root) is a
	// directory, and its size when it is a regular file.
	Stat(ctx context.Context, vol *NotebookVolume, path string) (isDir bool, size int64, err error)
	// Open returns the contents of the regular file at path.
	Open(ctx context.Context, vol *NotebookVolume, path string) (io.ReadCloser, error)
	// ListFiles returns every regular file under the directory at path,
	// relative to path itself (e.g. "sub/file.txt").
	ListFiles(ctx context.Context, vol *NotebookVolume, path string) ([]WorkspaceFile, error)
}

// LocalWorkspaceReader reads directly from vol.WorkDir on the local host —
// correct whenever the notebook volume is a real host directory (baremetal
// and docker direct-runtime, where RuntimeID is set).
type LocalWorkspaceReader struct{}

func (LocalWorkspaceReader) Stat(_ context.Context, vol *NotebookVolume, path string) (bool, int64, error) {
	root, name, err := openLocalWorkspace(vol, path)
	if err != nil {
		return false, 0, err
	}
	defer func() { _ = root.Close() }()
	info, err := root.Stat(name)
	if err != nil {
		return false, 0, err
	}
	return info.IsDir(), info.Size(), nil
}

func (LocalWorkspaceReader) Open(_ context.Context, vol *NotebookVolume, path string) (io.ReadCloser, error) {
	root, name, err := openLocalWorkspace(vol, path)
	if err != nil {
		return nil, err
	}
	file, err := root.Open(name)
	_ = root.Close()
	return file, err
}

func (LocalWorkspaceReader) ListFiles(_ context.Context, vol *NotebookVolume, path string) ([]WorkspaceFile, error) {
	root, name, err := openLocalWorkspace(vol, path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = root.Close() }()
	var files []WorkspaceFile
	err = fs.WalkDir(root.FS(), name, func(current string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.Type()&os.ModeSymlink != 0 {
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if entry.IsDir() {
			return nil
		}
		info, infoErr := entry.Info()
		if infoErr != nil {
			return infoErr
		}
		if !info.Mode().IsRegular() {
			return nil
		}
		rel, relErr := pathpkgRel(filepath.ToSlash(name), filepath.ToSlash(current))
		if relErr != nil {
			return relErr
		}
		files = append(files, WorkspaceFile{Rel: rel, Size: info.Size()})
		return nil
	})
	return files, err
}

// CleanWorkspacePath validates a user-supplied workspace-relative path and
// returns a normalized slash-separated representation. Workspace callers use
// the same validation for local and Kubernetes runtimes so a manifest cannot
// escape the notebook volume with an absolute path or a parent traversal.
func CleanWorkspacePath(p string) (string, error) {
	raw := strings.ReplaceAll(strings.TrimSpace(p), `\`, "/")
	if strings.HasPrefix(raw, "/") {
		return "", fmt.Errorf("notebook: workspace path must be relative")
	}
	clean := path.Clean(raw)
	if clean == "." {
		return "", nil
	}
	if clean == ".." || strings.HasPrefix(clean, "../") {
		return "", fmt.Errorf("notebook: workspace path escapes volume")
	}
	return clean, nil
}

func openLocalWorkspace(vol *NotebookVolume, requested string) (*os.Root, string, error) {
	if vol == nil || strings.TrimSpace(vol.WorkDir) == "" {
		return nil, "", fmt.Errorf("notebook: workspace directory is empty")
	}
	rel, err := CleanWorkspacePath(requested)
	if err != nil {
		return nil, "", err
	}
	root, err := os.OpenRoot(vol.WorkDir)
	if err != nil {
		return nil, "", fmt.Errorf("notebook: open workspace root: %w", err)
	}
	name := filepath.FromSlash(rel)
	if name == "" {
		name = "."
	}
	return root, name, nil
}

// pathpkgRel is a small wrapper around path.Rel semantics. path has no Rel,
// so filepath.Rel is used only after both operands are normalized to slashes;
// the resulting value is normalized again for the API response.
func pathpkgRel(base, target string) (string, error) {
	rel, err := filepath.Rel(filepath.FromSlash(base), filepath.FromSlash(target))
	return filepath.ToSlash(rel), err
}
