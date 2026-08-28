package execution

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/loykin/piper/pkg/notebook"
	"github.com/loykin/piper/pkg/notebook/execution/jupyter"
)

// ContentEntry describes one file or directory returned by
// NotebookGateway.ListContents.
type ContentEntry struct {
	Name         string
	Path         string
	Type         string // "directory" | "notebook" | "file"
	Size         int64
	LastModified time.Time
}

// KernelSessionInfo is what NotebookGateway returns after creating or
// looking up a Jupyter-native session — the Jupyter-side identifiers Piper
// stores internally on KernelSession but never returns to a caller.
type KernelSessionInfo struct {
	JupyterSessionID string
	KernelID         string
	KernelName       string
	Status           string
}

// NotebookGateway abstracts Jupyter Server access behind the runtime
// boundary (design doc §4.2): every runtime (baremetal/docker/k8s) already
// exposes the same Jupyter Server REST/WebSocket API through
// NotebookServer.Endpoint, so one implementation serves all three — unlike
// pkg/notebook's own Driver interface, there is no per-runtime variant
// here.
//
// The Jupyter token (server.Token) is read by the implementation directly
// off the passed *notebook.NotebookServer and is never returned in any
// value this interface produces, nor included in any wrapped error message
// (see jupyter.opaqueError).
type NotebookGateway interface {
	ListContents(ctx context.Context, server *notebook.NotebookServer, path string) ([]ContentEntry, error)
	// ReadNotebook returns the parsed document and the sha256 hex content
	// hash of its canonical encoding (design doc §5.3/§6.1).
	ReadNotebook(ctx context.Context, server *notebook.NotebookServer, path string) (*jupyter.Notebook, string, error)
	SaveNotebook(ctx context.Context, server *notebook.NotebookServer, path string, doc *jupyter.Notebook) error

	// CreateKernelSession starts a new Jupyter session+kernel bound to
	// notebookPath. piperSessionID becomes the Jupyter messaging-protocol
	// "session" field used to correlate kernel channel messages.
	CreateKernelSession(ctx context.Context, server *notebook.NotebookServer, notebookPath, kernelName string) (*KernelSessionInfo, error)
	GetKernelSession(ctx context.Context, server *notebook.NotebookServer, jupyterSessionID string) (*KernelSessionInfo, error)
	DeleteKernelSession(ctx context.Context, server *notebook.NotebookServer, jupyterSessionID string) error
	InterruptKernel(ctx context.Context, server *notebook.NotebookServer, kernelID string) error
	RestartKernel(ctx context.Context, server *notebook.NotebookServer, kernelID string) error

	// OpenChannel opens the kernel channels WebSocket used to execute
	// cells. Callers hold it open for the duration of one
	// NotebookExecution run (across all its cells) and Close it when done —
	// design doc §5.1: "한 Kernel에는 동시에 하나의 execute 요청만 전달한다".
	OpenChannel(ctx context.Context, server *notebook.NotebookServer, kernelID, piperSessionID string) (KernelChannel, error)
}

// KernelChannel is the minimal surface Service needs from an open kernel
// channels connection. *jupyter.Channel satisfies this structurally, so
// gatewayImpl needs no adapter — this interface exists purely so tests can
// substitute a fake channel instead of dialing a real Jupyter WebSocket
// (see service_test.go). Defined here rather than in the jupyter package
// because it's a Service-side testing seam, not part of the Jupyter wire
// protocol jupyter.Channel implements.
type KernelChannel interface {
	ExecuteCell(ctx context.Context, code string, sink jupyter.OutputSink) (*jupyter.ExecuteResult, error)
	Close() error
}

// gatewayImpl is the only NotebookGateway implementation — see the type doc
// comment for why one implementation covers every runtime.type.
type gatewayImpl struct{}

// NewGateway constructs the default NotebookGateway.
func NewGateway() NotebookGateway { return gatewayImpl{} }

func (gatewayImpl) client(server *notebook.NotebookServer) *jupyter.Client {
	base := jupyter.BuildBaseURL(server.Endpoint, server.ProjectID, server.Name)
	return jupyter.NewClient(base, server.Token)
}

func (g gatewayImpl) ListContents(ctx context.Context, server *notebook.NotebookServer, path string) ([]ContentEntry, error) {
	model, err := g.client(server).GetContents(ctx, path)
	if err != nil {
		return nil, mapGatewayErr("list contents", err)
	}
	if model.Type != "directory" {
		return []ContentEntry{{Name: model.Name, Path: model.Path, Type: model.Type, Size: sizeOf(model), LastModified: parseJupyterTime(model.LastModified)}}, nil
	}
	var entries []jupyter.ContentModel
	if len(model.Content) > 0 {
		if err := json.Unmarshal(model.Content, &entries); err != nil {
			return nil, fmt.Errorf("execution: decode directory listing: %w", err)
		}
	}
	out := make([]ContentEntry, 0, len(entries))
	for _, e := range entries {
		out = append(out, ContentEntry{Name: e.Name, Path: e.Path, Type: e.Type, Size: sizeOf(&e), LastModified: parseJupyterTime(e.LastModified)})
	}
	return out, nil
}

func sizeOf(m *jupyter.ContentModel) int64 {
	if m.Size == nil {
		return 0
	}
	return *m.Size
}

func parseJupyterTime(s string) time.Time {
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return time.Time{}
	}
	return t
}

func (g gatewayImpl) ReadNotebook(ctx context.Context, server *notebook.NotebookServer, path string) (*jupyter.Notebook, string, error) {
	model, err := g.client(server).GetContents(ctx, path)
	if err != nil {
		return nil, "", mapGatewayErr("read notebook", err)
	}
	if model.Type != "notebook" {
		return nil, "", newErr(ErrCodePathInvalid, false, "path is not a notebook document")
	}
	doc, err := jupyter.ParseNotebook(model.Content)
	if err != nil {
		return nil, "", fmt.Errorf("execution: %w", err)
	}
	return doc, doc.ContentHash(), nil
}

func (g gatewayImpl) SaveNotebook(ctx context.Context, server *notebook.NotebookServer, path string, doc *jupyter.Notebook) error {
	raw, err := doc.Marshal()
	if err != nil {
		return fmt.Errorf("execution: marshal notebook: %w", err)
	}
	err = g.client(server).PutContents(ctx, path, jupyter.ContentModel{
		Path:    path,
		Type:    "notebook",
		Format:  "json",
		Content: raw,
	})
	if err != nil {
		return mapGatewayErr("save notebook", err)
	}
	return nil
}

func (g gatewayImpl) CreateKernelSession(ctx context.Context, server *notebook.NotebookServer, notebookPath, kernelName string) (*KernelSessionInfo, error) {
	session, err := g.client(server).CreateSession(ctx, notebookPath, kernelName)
	if err != nil {
		return nil, mapGatewayErr("create kernel session", err)
	}
	return &KernelSessionInfo{
		JupyterSessionID: session.ID,
		KernelID:         session.Kernel.ID,
		KernelName:       session.Kernel.Name,
		Status:           session.Kernel.State,
	}, nil
}

func (g gatewayImpl) GetKernelSession(ctx context.Context, server *notebook.NotebookServer, jupyterSessionID string) (*KernelSessionInfo, error) {
	session, err := g.client(server).GetSession(ctx, jupyterSessionID)
	if err != nil {
		return nil, mapGatewayErr("get kernel session", err)
	}
	return &KernelSessionInfo{
		JupyterSessionID: session.ID,
		KernelID:         session.Kernel.ID,
		KernelName:       session.Kernel.Name,
		Status:           session.Kernel.State,
	}, nil
}

func (g gatewayImpl) DeleteKernelSession(ctx context.Context, server *notebook.NotebookServer, jupyterSessionID string) error {
	if err := g.client(server).DeleteSession(ctx, jupyterSessionID); err != nil {
		return mapGatewayErr("delete kernel session", err)
	}
	return nil
}

func (g gatewayImpl) InterruptKernel(ctx context.Context, server *notebook.NotebookServer, kernelID string) error {
	if err := g.client(server).InterruptKernel(ctx, kernelID); err != nil {
		return mapGatewayErr("interrupt kernel", err)
	}
	return nil
}

func (g gatewayImpl) RestartKernel(ctx context.Context, server *notebook.NotebookServer, kernelID string) error {
	if err := g.client(server).RestartKernel(ctx, kernelID); err != nil {
		return mapGatewayErr("restart kernel", err)
	}
	return nil
}

func (g gatewayImpl) OpenChannel(ctx context.Context, server *notebook.NotebookServer, kernelID, piperSessionID string) (KernelChannel, error) {
	ch, err := jupyter.DialChannel(ctx, server.Endpoint, server.ProjectID, server.Name, kernelID, piperSessionID, server.Token)
	if err != nil {
		return nil, mapGatewayErr("open kernel channel", err)
	}
	return ch, nil
}

// mapGatewayErr converts a jupyter client error (which never carries a
// token or full URL — see jupyter.opaqueError) into one of this package's
// stable error codes based on the upstream HTTP status, when available.
func mapGatewayErr(op string, err error) error {
	status, ok := jupyter.AsStatusError(err)
	if !ok {
		return newErr(ErrCodeRuntimeUnavailable, true, "%s: jupyter server unreachable", op)
	}
	switch status {
	case 404:
		return newErr(ErrCodePathInvalid, false, "%s: not found", op)
	case 409:
		return newErr(ErrCodeContentConflict, false, "%s: conflict", op)
	case 401, 403:
		return newErr(ErrCodeRuntimeUnavailable, false, "%s: unauthorized", op)
	default:
		return newErr(ErrCodeRuntimeUnavailable, true, "%s: jupyter server error", op)
	}
}
