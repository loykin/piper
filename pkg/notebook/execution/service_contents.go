package execution

import (
	"context"

	"github.com/loykin/piper/pkg/notebook"
	"github.com/loykin/piper/pkg/notebook/execution/jupyter"
)

// ListContents lists a directory (or stats a single file) under a running
// notebook server's workspace (design doc §7.1, viewer role).
func (s *Service) ListContents(ctx context.Context, projectID, notebookName, path string) ([]ContentEntry, error) {
	clean, err := notebook.CleanWorkspacePath(path)
	if err != nil {
		return nil, newErr(ErrCodePathInvalid, false, "invalid path: %v", err)
	}
	server, err := s.getRunningServer(ctx, projectID, notebookName)
	if err != nil {
		return nil, err
	}
	return s.deps.Gateway.ListContents(ctx, server, clean)
}

// ReadDocument reads a .ipynb document, enforcing the
// notebook_execution.file_read_bytes size limit (design doc §7.1, §11.1).
func (s *Service) ReadDocument(ctx context.Context, projectID, notebookName, path string) (*jupyter.Notebook, string, error) {
	clean, err := validateNotebookPath(path)
	if err != nil {
		return nil, "", err
	}
	server, err := s.getRunningServer(ctx, projectID, notebookName)
	if err != nil {
		return nil, "", err
	}
	doc, hash, err := s.deps.Gateway.ReadNotebook(ctx, server, clean)
	if err != nil {
		return nil, "", err
	}
	if raw, merr := doc.Marshal(); merr == nil && len(raw) > s.scheduler.Limits().FileReadBytes {
		return nil, "", newErr(ErrCodeOutputTooLarge, false, "document exceeds the file_read_bytes limit")
	}
	return doc, hash, nil
}

// ReadFile reads a non-notebook file's raw content, enforcing the
// notebook_execution.file_read_bytes size limit — the generic-file
// counterpart to ReadDocument, added for the MCP Phase 2
// piper://.../files/{path} resource (design doc §7.1/§8.3; see
// gateway.go's FileContent doc comment for why this didn't already exist).
func (s *Service) ReadFile(ctx context.Context, projectID, notebookName, path string) (*FileContent, error) {
	clean, err := notebook.CleanWorkspacePath(path)
	if err != nil {
		return nil, newErr(ErrCodePathInvalid, false, "invalid path: %v", err)
	}
	if clean == "" {
		return nil, newErr(ErrCodePathInvalid, false, "path is required")
	}
	server, err := s.getRunningServer(ctx, projectID, notebookName)
	if err != nil {
		return nil, err
	}
	fc, err := s.deps.Gateway.ReadFile(ctx, server, clean)
	if err != nil {
		return nil, err
	}
	if fc.Size > int64(s.scheduler.Limits().FileReadBytes) {
		return nil, newErr(ErrCodeOutputTooLarge, false, "file exceeds the file_read_bytes limit")
	}
	return fc, nil
}

// WriteDocument creates or replaces a .ipynb document (design doc §7.1,
// member role). When baseHash is non-empty and the document already
// exists, the write is rejected with ErrCodeContentConflict unless
// baseHash matches the document's current content hash — the same
// conflict-avoidance discipline runExecution applies to execution results,
// exposed here for a human/API client editing a document directly.
func (s *Service) WriteDocument(ctx context.Context, actor Actor, projectID, notebookName, path string, doc *jupyter.Notebook, baseHash string) error {
	clean, err := validateNotebookPath(path)
	if err != nil {
		return err
	}
	server, err := s.getRunningServer(ctx, projectID, notebookName)
	if err != nil {
		return err
	}
	if baseHash != "" {
		_, currentHash, rerr := s.deps.Gateway.ReadNotebook(ctx, server, clean)
		if rerr == nil && currentHash != baseHash {
			return newErr(ErrCodeContentConflict, false, "document changed since base_hash was read")
		}
		// rerr != nil (most commonly "not found") is treated as no
		// existing document to conflict with — the write proceeds as a
		// create.
	}
	return s.deps.Gateway.SaveNotebook(ctx, server, clean, doc)
}
