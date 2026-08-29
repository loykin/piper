package mcp

import (
	"context"
	"sync"
	"time"

	"github.com/loykin/piper/pkg/notebook"
	"github.com/loykin/piper/pkg/notebook/execution"
	"github.com/loykin/piper/pkg/notebook/execution/jupyter"
)

// --- fake notebook.Repository ---------------------------------------------

type fakeNotebookRepo struct {
	mu      sync.Mutex
	servers map[string]*notebook.NotebookServer // key: projectID+"/"+name
}

func newFakeNotebookRepo() *fakeNotebookRepo {
	return &fakeNotebookRepo{servers: map[string]*notebook.NotebookServer{}}
}

func key(projectID, name string) string { return projectID + "/" + name }

func (r *fakeNotebookRepo) put(nb *notebook.NotebookServer) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.servers[key(nb.ProjectID, nb.Name)] = nb
}

func (r *fakeNotebookRepo) Create(_ context.Context, nb *notebook.NotebookServer) error {
	r.put(nb)
	return nil
}
func (r *fakeNotebookRepo) Get(_ context.Context, projectID, name string) (*notebook.NotebookServer, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.servers[key(projectID, name)], nil
}
func (r *fakeNotebookRepo) GetByVolumeID(context.Context, string, string) (*notebook.NotebookServer, error) {
	return nil, nil
}
func (r *fakeNotebookRepo) Update(_ context.Context, nb *notebook.NotebookServer) error {
	r.put(nb)
	return nil
}
func (r *fakeNotebookRepo) SetStatus(_ context.Context, projectID, name, status string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if nb, ok := r.servers[key(projectID, name)]; ok {
		nb.Status = status
	}
	return nil
}
func (r *fakeNotebookRepo) List(_ context.Context, projectID string) ([]*notebook.NotebookServer, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []*notebook.NotebookServer
	for _, nb := range r.servers {
		if nb.ProjectID == projectID {
			out = append(out, nb)
		}
	}
	return out, nil
}
func (r *fakeNotebookRepo) Delete(_ context.Context, projectID, name string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.servers, key(projectID, name))
	return nil
}
func (r *fakeNotebookRepo) AppendHistory(context.Context, *notebook.NotebookServer) error { return nil }
func (r *fakeNotebookRepo) ListHistory(context.Context, string, int, int) ([]*notebook.NotebookHistory, error) {
	return nil, nil
}
func (r *fakeNotebookRepo) CountHistory(context.Context, string) (int, error) { return 0, nil }

// --- fake execution.Repository ---------------------------------------------

type fakeExecRepo struct {
	mu       sync.Mutex
	kernels  map[string]*execution.KernelSession
	execs    map[string]*execution.NotebookExecution
	policies map[string]string
}

func newFakeExecRepo() *fakeExecRepo {
	return &fakeExecRepo{
		kernels:  map[string]*execution.KernelSession{},
		execs:    map[string]*execution.NotebookExecution{},
		policies: map[string]string{},
	}
}

func (r *fakeExecRepo) seedExecution(e *execution.NotebookExecution) {
	r.mu.Lock()
	defer r.mu.Unlock()
	cp := *e
	r.execs[e.ID] = &cp
}

func (r *fakeExecRepo) CreateKernelSession(_ context.Context, k *execution.KernelSession) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	cp := *k
	r.kernels[k.ID] = &cp
	return nil
}
func (r *fakeExecRepo) GetKernelSession(_ context.Context, projectID, id string) (*execution.KernelSession, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	k, ok := r.kernels[id]
	if !ok || k.ProjectID != projectID {
		return nil, nil
	}
	cp := *k
	return &cp, nil
}
func (r *fakeExecRepo) ListKernelSessions(context.Context, string, string, string, int, int) ([]*execution.KernelSession, error) {
	return nil, nil
}
func (r *fakeExecRepo) UpdateKernelSession(_ context.Context, k *execution.KernelSession) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.kernels[k.ID] = k
	return nil
}
func (r *fakeExecRepo) CountOpenKernelSessions(context.Context, string, string) (int, error) {
	return 0, nil
}
func (r *fakeExecRepo) ListStaleKernelSessions(context.Context, time.Time) ([]*execution.KernelSession, error) {
	return nil, nil
}

func (r *fakeExecRepo) CreateExecution(_ context.Context, e *execution.NotebookExecution) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	cp := *e
	r.execs[e.ID] = &cp
	return nil
}
func (r *fakeExecRepo) GetExecution(_ context.Context, projectID, id string) (*execution.NotebookExecution, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	e, ok := r.execs[id]
	if !ok || e.ProjectID != projectID {
		return nil, nil
	}
	cp := *e
	return &cp, nil
}
func (r *fakeExecRepo) FindExecutionByIdempotencyKey(context.Context, string, string, string, string) (*execution.NotebookExecution, error) {
	return nil, nil
}
func (r *fakeExecRepo) ListExecutions(_ context.Context, projectID, notebookName string, limit, offset int) ([]*execution.NotebookExecution, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []*execution.NotebookExecution
	for _, e := range r.execs {
		if e.ProjectID == projectID && e.NotebookName == notebookName {
			cp := *e
			out = append(out, &cp)
		}
	}
	return out, nil
}
func (r *fakeExecRepo) CountExecutions(_ context.Context, projectID, notebookName string) (int, error) {
	list, _ := r.ListExecutions(context.Background(), projectID, notebookName, 0, 0)
	return len(list), nil
}
func (r *fakeExecRepo) UpdateExecution(_ context.Context, e *execution.NotebookExecution) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.execs[e.ID] = e
	return nil
}
func (r *fakeExecRepo) CountRunningExecutions(context.Context, string, string) (int, error) {
	return 0, nil
}
func (r *fakeExecRepo) CountQueuedExecutions(context.Context, string) (int, error) { return 0, nil }
func (r *fakeExecRepo) ListExecutionsByStatus(context.Context, []string) ([]*execution.NotebookExecution, error) {
	return nil, nil
}
func (r *fakeExecRepo) GetExecutionPolicy(_ context.Context, projectID string) (string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.policies[projectID], nil
}
func (r *fakeExecRepo) SetExecutionPolicy(_ context.Context, projectID, policy, _ string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.policies[projectID] = policy
	return nil
}

// --- fake execution.NotebookGateway -----------------------------------------

type fakeGateway struct {
	mu    sync.Mutex
	docs  map[string]*jupyter.Notebook // key: projectID+"/"+notebookName+"/"+path
	files map[string]*execution.FileContent
}

func newFakeGateway() *fakeGateway {
	return &fakeGateway{docs: map[string]*jupyter.Notebook{}, files: map[string]*execution.FileContent{}}
}

func docKey(server *notebook.NotebookServer, path string) string {
	return server.ProjectID + "/" + server.Name + "/" + path
}

func (g *fakeGateway) putDoc(server *notebook.NotebookServer, path string, doc *jupyter.Notebook) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.docs[docKey(server, path)] = doc
}

func (g *fakeGateway) putFile(server *notebook.NotebookServer, path string, fc *execution.FileContent) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.files[docKey(server, path)] = fc
}

func (g *fakeGateway) ListContents(_ context.Context, server *notebook.NotebookServer, path string) ([]execution.ContentEntry, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	var out []execution.ContentEntry
	prefix := server.ProjectID + "/" + server.Name + "/"
	for k := range g.docs {
		if len(k) > len(prefix) && k[:len(prefix)] == prefix {
			out = append(out, execution.ContentEntry{Name: k[len(prefix):], Path: k[len(prefix):], Type: "notebook"})
		}
	}
	_ = path
	return out, nil
}

func (g *fakeGateway) ReadNotebook(_ context.Context, server *notebook.NotebookServer, path string) (*jupyter.Notebook, string, error) {
	g.mu.Lock()
	doc, ok := g.docs[docKey(server, path)]
	g.mu.Unlock()
	if !ok {
		return nil, "", &execution.Error{Code: execution.ErrCodePathInvalid, Message: "not found"}
	}
	raw, err := doc.Marshal()
	if err != nil {
		return nil, "", err
	}
	cp, err := jupyter.ParseNotebook(raw)
	if err != nil {
		return nil, "", err
	}
	return cp, cp.ContentHash(), nil
}

func (g *fakeGateway) SaveNotebook(_ context.Context, server *notebook.NotebookServer, path string, doc *jupyter.Notebook) error {
	g.putDoc(server, path, doc)
	return nil
}

func (g *fakeGateway) ReadFile(_ context.Context, server *notebook.NotebookServer, path string) (*execution.FileContent, error) {
	g.mu.Lock()
	fc, ok := g.files[docKey(server, path)]
	g.mu.Unlock()
	if !ok {
		return nil, &execution.Error{Code: execution.ErrCodePathInvalid, Message: "not found"}
	}
	cp := *fc
	return &cp, nil
}

func (g *fakeGateway) CreateKernelSession(context.Context, *notebook.NotebookServer, string, string) (*execution.KernelSessionInfo, error) {
	return &execution.KernelSessionInfo{JupyterSessionID: "js-1", KernelID: "k-1", KernelName: "python3"}, nil
}
func (g *fakeGateway) GetKernelSession(context.Context, *notebook.NotebookServer, string) (*execution.KernelSessionInfo, error) {
	return nil, nil
}
func (g *fakeGateway) DeleteKernelSession(context.Context, *notebook.NotebookServer, string) error {
	return nil
}
func (g *fakeGateway) InterruptKernel(context.Context, *notebook.NotebookServer, string) error {
	return nil
}
func (g *fakeGateway) RestartKernel(context.Context, *notebook.NotebookServer, string) error {
	return nil
}
func (g *fakeGateway) OpenChannel(context.Context, *notebook.NotebookServer, string, string) (execution.KernelChannel, error) {
	return nil, nil
}
