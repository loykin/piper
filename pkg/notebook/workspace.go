package notebook

import (
	"context"
	"io"
	"os"
	"path/filepath"
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
	info, err := os.Stat(filepath.Join(vol.WorkDir, filepath.FromSlash(path)))
	if err != nil {
		return false, 0, err
	}
	return info.IsDir(), info.Size(), nil
}

func (LocalWorkspaceReader) Open(_ context.Context, vol *NotebookVolume, path string) (io.ReadCloser, error) {
	return os.Open(filepath.Join(vol.WorkDir, filepath.FromSlash(path)))
}

func (LocalWorkspaceReader) ListFiles(_ context.Context, vol *NotebookVolume, path string) ([]WorkspaceFile, error) {
	root := filepath.Join(vol.WorkDir, filepath.FromSlash(path))
	var files []WorkspaceFile
	err := filepath.Walk(root, func(p string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return err
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
