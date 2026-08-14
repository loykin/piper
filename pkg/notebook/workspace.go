package notebook

import (
	"context"
	"fmt"
	"io"
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
// volumes (K8s CSI — see NotebookVolume.WorkerID's doc comment) it is a path
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
// and docker direct-runtime, where WorkerID is set).
type LocalWorkspaceReader struct{}

func (LocalWorkspaceReader) Stat(_ context.Context, vol *NotebookVolume, path string) (bool, int64, error) {
	full, err := localWorkspacePath(vol, path)
	if err != nil {
		return false, 0, err
	}
	info, err := os.Stat(full)
	if err != nil {
		return false, 0, err
	}
	return info.IsDir(), info.Size(), nil
}

func (LocalWorkspaceReader) Open(_ context.Context, vol *NotebookVolume, path string) (io.ReadCloser, error) {
	full, err := localWorkspacePath(vol, path)
	if err != nil {
		return nil, err
	}
	return os.Open(full)
}

func (LocalWorkspaceReader) ListFiles(_ context.Context, vol *NotebookVolume, path string) ([]WorkspaceFile, error) {
	root, err := localWorkspacePath(vol, path)
	if err != nil {
		return nil, err
	}
	var files []WorkspaceFile
	err = filepath.Walk(root, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			if info.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if info.IsDir() {
			return nil
		}
		rel, relErr := filepath.Rel(root, p)
		if relErr != nil {
			return relErr
		}
		files = append(files, WorkspaceFile{Rel: filepath.ToSlash(rel), Size: info.Size()})
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

func localWorkspacePath(vol *NotebookVolume, requested string) (string, error) {
	if vol == nil || strings.TrimSpace(vol.WorkDir) == "" {
		return "", fmt.Errorf("notebook: workspace directory is empty")
	}
	rel, err := CleanWorkspacePath(requested)
	if err != nil {
		return "", err
	}
	root, err := filepath.Abs(vol.WorkDir)
	if err != nil {
		return "", fmt.Errorf("notebook: resolve workspace root: %w", err)
	}
	full, err := filepath.Abs(filepath.Join(root, filepath.FromSlash(rel)))
	if err != nil {
		return "", fmt.Errorf("notebook: resolve workspace path: %w", err)
	}
	within, err := filepath.Rel(root, full)
	if err != nil || within == ".." || strings.HasPrefix(within, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("notebook: workspace path escapes volume")
	}

	// Resolve existing symlinks before opening the target. Without this check,
	// a path such as workspace/link/secret could pass the lexical containment
	// test while link points outside the volume.
	realRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return "", fmt.Errorf("notebook: resolve workspace root symlinks: %w", err)
	}
	realFull, err := filepath.EvalSymlinks(full)
	if err != nil {
		return "", err
	}
	realWithin, err := filepath.Rel(realRoot, realFull)
	if err != nil || realWithin == ".." || strings.HasPrefix(realWithin, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("notebook: workspace path escapes volume through symlink")
	}
	return realFull, nil
}
