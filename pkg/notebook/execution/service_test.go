package execution

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/loykin/piper/pkg/notebook"
	"github.com/loykin/piper/pkg/notebook/execution/jupyter"
	"github.com/loykin/piper/pkg/security"
)

// --- fakes ---------------------------------------------------------------

type fakeRepo struct {
	mu       sync.Mutex
	kernels  map[string]*KernelSession
	execs    map[string]*NotebookExecution
	policies map[string]string
}

func newFakeRepo() *fakeRepo {
	return &fakeRepo{kernels: map[string]*KernelSession{}, execs: map[string]*NotebookExecution{}, policies: map[string]string{}}
}

func (r *fakeRepo) CreateKernelSession(_ context.Context, k *KernelSession) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	cp := *k
	r.kernels[k.ID] = &cp
	return nil
}

func (r *fakeRepo) GetKernelSession(_ context.Context, projectID, id string) (*KernelSession, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	k, ok := r.kernels[id]
	if !ok || k.ProjectID != projectID {
		return nil, nil
	}
	cp := *k
	return &cp, nil
}

func (r *fakeRepo) ListKernelSessions(_ context.Context, projectID, notebookName, createdBy string, _, _ int) ([]*KernelSession, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []*KernelSession
	for _, k := range r.kernels {
		if k.ProjectID != projectID || k.NotebookName != notebookName {
			continue
		}
		if createdBy != "" && k.CreatedBy != createdBy {
			continue
		}
		cp := *k
		out = append(out, &cp)
	}
	return out, nil
}

func (r *fakeRepo) UpdateKernelSession(_ context.Context, k *KernelSession) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	existing, ok := r.kernels[k.ID]
	if !ok || existing.ProjectID != k.ProjectID {
		return ErrNotFound
	}
	cp := *k
	r.kernels[k.ID] = &cp
	return nil
}

func (r *fakeRepo) CountOpenKernelSessions(_ context.Context, projectID, notebookName string) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	n := 0
	for _, k := range r.kernels {
		if k.ProjectID == projectID && k.NotebookName == notebookName && k.Status != KernelStatusClosed && k.Status != KernelStatusFailed {
			n++
		}
	}
	return n, nil
}

func (r *fakeRepo) ListStaleKernelSessions(context.Context, time.Time) ([]*KernelSession, error) {
	return nil, nil
}

func (r *fakeRepo) CreateExecution(_ context.Context, e *NotebookExecution) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if e.IdempotencyKey != "" {
		for _, existing := range r.execs {
			if existing.ProjectID == e.ProjectID && existing.NotebookName == e.NotebookName &&
				existing.RequestedBy == e.RequestedBy && existing.IdempotencyKey == e.IdempotencyKey {
				return ErrConflict
			}
		}
	}
	cp := *e
	r.execs[e.ID] = &cp
	return nil
}

func (r *fakeRepo) GetExecution(_ context.Context, projectID, id string) (*NotebookExecution, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	e, ok := r.execs[id]
	if !ok || e.ProjectID != projectID {
		return nil, nil
	}
	cp := *e
	return &cp, nil
}

func (r *fakeRepo) FindExecutionByIdempotencyKey(_ context.Context, projectID, notebookName, requestedBy, idempotencyKey string) (*NotebookExecution, error) {
	if idempotencyKey == "" {
		return nil, nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, e := range r.execs {
		if e.ProjectID == projectID && e.NotebookName == notebookName && e.RequestedBy == requestedBy && e.IdempotencyKey == idempotencyKey {
			cp := *e
			return &cp, nil
		}
	}
	return nil, nil
}

func (r *fakeRepo) ListExecutions(_ context.Context, projectID, notebookName string, _, _ int) ([]*NotebookExecution, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []*NotebookExecution
	for _, e := range r.execs {
		if e.ProjectID == projectID && e.NotebookName == notebookName {
			cp := *e
			out = append(out, &cp)
		}
	}
	return out, nil
}

func (r *fakeRepo) CountExecutions(_ context.Context, projectID, notebookName string) (int, error) {
	list, _ := r.ListExecutions(context.Background(), projectID, notebookName, 0, 0)
	return len(list), nil
}

func (r *fakeRepo) UpdateExecution(_ context.Context, e *NotebookExecution) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	existing, ok := r.execs[e.ID]
	if !ok || existing.ProjectID != e.ProjectID {
		return ErrNotFound
	}
	cp := *e
	r.execs[e.ID] = &cp
	return nil
}

func (r *fakeRepo) CountRunningExecutions(_ context.Context, projectID, notebookName string) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	n := 0
	for _, e := range r.execs {
		if e.ProjectID == projectID && e.NotebookName == notebookName && e.Status == StatusRunning {
			n++
		}
	}
	return n, nil
}

func (r *fakeRepo) CountQueuedExecutions(_ context.Context, projectID string) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	n := 0
	for _, e := range r.execs {
		if e.ProjectID == projectID && (e.Status == StatusQueued || e.Status == StatusAwaitingApproval) {
			n++
		}
	}
	return n, nil
}

func (r *fakeRepo) ListExecutionsByStatus(_ context.Context, statuses []string) ([]*NotebookExecution, error) {
	want := map[string]bool{}
	for _, s := range statuses {
		want[s] = true
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []*NotebookExecution
	for _, e := range r.execs {
		if want[e.Status] {
			cp := *e
			out = append(out, &cp)
		}
	}
	return out, nil
}

func (r *fakeRepo) GetExecutionPolicy(_ context.Context, projectID string) (string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.policies[projectID], nil
}

func (r *fakeRepo) SetExecutionPolicy(_ context.Context, projectID, policy, _ string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.policies[projectID] = policy
	return nil
}

// fakeNotebookRepo implements notebook.Repository with a single in-memory
// server map, enough to drive Service.getRunningServer.
type fakeNotebookRepo struct {
	mu      sync.Mutex
	servers map[string]*notebook.NotebookServer
}

func newFakeNotebookRepo() *fakeNotebookRepo {
	return &fakeNotebookRepo{servers: map[string]*notebook.NotebookServer{}}
}

func nbKey(projectID, name string) string { return projectID + "/" + name }

func (r *fakeNotebookRepo) put(nb *notebook.NotebookServer) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.servers[nbKey(nb.ProjectID, nb.Name)] = nb
}

func (r *fakeNotebookRepo) Create(_ context.Context, nb *notebook.NotebookServer) error {
	r.put(nb)
	return nil
}
func (r *fakeNotebookRepo) Get(_ context.Context, projectID, name string) (*notebook.NotebookServer, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.servers[nbKey(projectID, name)], nil
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
	if s, ok := r.servers[nbKey(projectID, name)]; ok {
		s.Status = status
	}
	return nil
}
func (r *fakeNotebookRepo) List(context.Context, string) ([]*notebook.NotebookServer, error) {
	return nil, nil
}
func (r *fakeNotebookRepo) Delete(_ context.Context, projectID, name string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.servers, nbKey(projectID, name))
	return nil
}
func (r *fakeNotebookRepo) AppendHistory(context.Context, *notebook.NotebookServer) error { return nil }
func (r *fakeNotebookRepo) ListHistory(context.Context, string, int, int) ([]*notebook.NotebookHistory, error) {
	return nil, nil
}
func (r *fakeNotebookRepo) CountHistory(context.Context, string) (int, error) { return 0, nil }

// fakeGateway implements NotebookGateway entirely in memory — no real
// Jupyter server or network call involved. executeFn controls what
// "running a cell" does; nil means "succeed immediately with no output".
type fakeGateway struct {
	mu             sync.Mutex
	docs           map[string]*jupyter.Notebook
	files          map[string]*FileContent
	kernelCounter  int
	interruptCalls int
	executeFn      func(ctx context.Context, code string, sink jupyter.OutputSink) (*jupyter.ExecuteResult, error)
}

func newFakeGateway() *fakeGateway {
	return &fakeGateway{docs: map[string]*jupyter.Notebook{}, files: map[string]*FileContent{}}
}

func (g *fakeGateway) putDoc(path string, doc *jupyter.Notebook) {
	raw, _ := doc.Marshal()
	cp, _ := jupyter.ParseNotebook(raw)
	g.mu.Lock()
	defer g.mu.Unlock()
	g.docs[path] = cp
}

func (g *fakeGateway) ListContents(context.Context, *notebook.NotebookServer, string) ([]ContentEntry, error) {
	return nil, nil
}

func (g *fakeGateway) ReadNotebook(_ context.Context, _ *notebook.NotebookServer, path string) (*jupyter.Notebook, string, error) {
	g.mu.Lock()
	doc, ok := g.docs[path]
	g.mu.Unlock()
	if !ok {
		return nil, "", newErr(ErrCodePathInvalid, false, "not found: %s", path)
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

func (g *fakeGateway) SaveNotebook(_ context.Context, _ *notebook.NotebookServer, path string, doc *jupyter.Notebook) error {
	g.putDoc(path, doc)
	return nil
}

func (g *fakeGateway) ReadFile(_ context.Context, _ *notebook.NotebookServer, path string) (*FileContent, error) {
	g.mu.Lock()
	fc, ok := g.files[path]
	g.mu.Unlock()
	if !ok {
		return nil, newErr(ErrCodePathInvalid, false, "not found: %s", path)
	}
	cp := *fc
	return &cp, nil
}

func (g *fakeGateway) CreateKernelSession(_ context.Context, _ *notebook.NotebookServer, _, kernelName string) (*KernelSessionInfo, error) {
	g.mu.Lock()
	g.kernelCounter++
	id := g.kernelCounter
	g.mu.Unlock()
	return &KernelSessionInfo{
		JupyterSessionID: fmt.Sprintf("jsess-%d", id),
		KernelID:         fmt.Sprintf("kernel-%d", id),
		KernelName:       kernelName,
		Status:           "idle",
	}, nil
}

func (g *fakeGateway) GetKernelSession(_ context.Context, _ *notebook.NotebookServer, jupyterSessionID string) (*KernelSessionInfo, error) {
	return &KernelSessionInfo{JupyterSessionID: jupyterSessionID, Status: "idle"}, nil
}

func (g *fakeGateway) DeleteKernelSession(context.Context, *notebook.NotebookServer, string) error {
	return nil
}

func (g *fakeGateway) InterruptKernel(context.Context, *notebook.NotebookServer, string) error {
	g.mu.Lock()
	g.interruptCalls++
	g.mu.Unlock()
	return nil
}

func (g *fakeGateway) RestartKernel(context.Context, *notebook.NotebookServer, string) error {
	return nil
}

func (g *fakeGateway) OpenChannel(context.Context, *notebook.NotebookServer, string, string) (KernelChannel, error) {
	return &fakeChannel{gw: g}, nil
}

type fakeChannel struct{ gw *fakeGateway }

func (c *fakeChannel) Close() error { return nil }

func (c *fakeChannel) ExecuteCell(ctx context.Context, code string, sink jupyter.OutputSink) (*jupyter.ExecuteResult, error) {
	if c.gw.executeFn != nil {
		return c.gw.executeFn(ctx, code, sink)
	}
	return &jupyter.ExecuteResult{Status: "ok", ExecutionCount: 1}, nil
}

// --- test harness ----------------------------------------------------------

const testProject = "proj-1"
const testNotebook = "nb-1"

type harness struct {
	repo      *fakeRepo
	notebooks *fakeNotebookRepo
	gateway   *fakeGateway
	svc       *Service
}

func newHarness(t *testing.T, policy string) *harness {
	t.Helper()
	repo := newFakeRepo()
	notebooks := newFakeNotebookRepo()
	notebooks.put(&notebook.NotebookServer{ProjectID: testProject, Name: testNotebook, Status: notebook.StatusRunning, Endpoint: "http://fake"})
	gw := newFakeGateway()
	svc := NewService(context.Background(), Deps{
		Repo:          repo,
		Notebooks:     notebooks,
		Gateway:       gw,
		Limits:        Limits{ExecutionTimeout: 5 * time.Second, CellTimeout: 2 * time.Second, MaxRunningPerNotebook: 1, MaxKernelsPerNotebook: 2, MaxQueuedPerProject: 20, InlineOutputBytes: 65536, FileReadBytes: 1 << 20},
		PolicyDefault: policy,
	})
	return &harness{repo: repo, notebooks: notebooks, gateway: gw, svc: svc}
}

func (h *harness) seedNotebook(path string, codeCells int) {
	doc := jupyter.EmptyNotebook()
	for i := 0; i < codeCells; i++ {
		doc.AppendCodeCell(fmt.Sprintf("cell-%d", i), fmt.Sprintf("x = %d", i))
	}
	h.gateway.putDoc(path, doc)
}

func waitTerminal(t *testing.T, svc *Service, projectID, id string, timeout time.Duration) *NotebookExecution {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		e, err := svc.GetExecution(context.Background(), projectID, id)
		if err != nil {
			t.Fatalf("GetExecution: %v", err)
		}
		if IsTerminalExecutionStatus(e.Status) {
			return e
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("execution %s did not reach a terminal status within %s", id, timeout)
	return nil
}

func waitStatus(t *testing.T, svc *Service, projectID, id, status string, timeout time.Duration) *NotebookExecution {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		e, err := svc.GetExecution(context.Background(), projectID, id)
		if err != nil {
			t.Fatalf("GetExecution: %v", err)
		}
		if e.Status == status {
			return e
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("execution %s did not reach status %q within %s", id, status, timeout)
	return nil
}

var memberActor = Actor{ID: "alice", Role: security.ProjectRoleMember, ClientID: "rest"}
var adminActor = Actor{ID: "admin-1", Role: security.ProjectRoleAdmin, ClientID: "rest"}

// --- tests ------------------------------------------------------------------

func TestCreateExecution_PolicyAllowed_RunsToSuccess(t *testing.T) {
	h := newHarness(t, PolicyAllowed)
	h.seedNotebook("nb.ipynb", 2)

	exec, replayed, err := h.svc.CreateExecution(context.Background(), memberActor, testProject, testNotebook, CreateExecutionRequest{
		Kind: KindNotebook,
		Path: "nb.ipynb",
	}, "")
	if err != nil {
		t.Fatalf("CreateExecution: %v", err)
	}
	if replayed {
		t.Fatal("first CreateExecution reported replayed=true")
	}
	if exec.Status != StatusQueued {
		t.Fatalf("initial status = %q, want %q", exec.Status, StatusQueued)
	}

	final := waitTerminal(t, h.svc, testProject, exec.ID, 2*time.Second)
	if final.Status != StatusSucceeded {
		t.Fatalf("final status = %q (code=%s msg=%s), want succeeded", final.Status, final.ErrorCode, final.ErrorMessage)
	}
	if final.TotalCells != 2 || final.CurrentCell != 2 {
		t.Fatalf("progress = %d/%d, want 2/2", final.CurrentCell, final.TotalCells)
	}

	// The original document must have been overwritten with executed
	// outputs since the content hash never changed underneath.
	doc, _, err := h.gateway.ReadNotebook(context.Background(), nil, "nb.ipynb")
	if err != nil {
		t.Fatalf("ReadNotebook(original): %v", err)
	}
	if doc.Cells[0].ExecutionCount == nil {
		t.Fatal("original notebook was not updated with execution results")
	}

	// A recovery copy must also exist at ResultPath (design doc §6.1 step 10).
	if _, _, err := h.gateway.ReadNotebook(context.Background(), nil, final.ResultPath); err != nil {
		t.Fatalf("ReadNotebook(result path): %v", err)
	}
}

func TestCreateExecution_ApprovalRequired_ThenApprove(t *testing.T) {
	h := newHarness(t, PolicyApprovalRequired)
	h.seedNotebook("nb.ipynb", 1)

	exec, _, err := h.svc.CreateExecution(context.Background(), memberActor, testProject, testNotebook, CreateExecutionRequest{
		Kind: KindNotebook,
		Path: "nb.ipynb",
	}, "")
	if err != nil {
		t.Fatalf("CreateExecution: %v", err)
	}
	if exec.Status != StatusAwaitingApproval {
		t.Fatalf("status = %q, want awaiting_approval", exec.Status)
	}

	// A member cannot approve — only the route's admin-only middleware
	// plus this ownership-agnostic check protects it in production; here
	// we just confirm the state machine itself still requires the
	// approval step to actually move the row before it's ever dispatched.
	if got, err := h.svc.GetExecution(context.Background(), testProject, exec.ID); err != nil || got.Status != StatusAwaitingApproval {
		t.Fatalf("execution moved out of awaiting_approval without an explicit Approve call")
	}

	if err := h.svc.ApproveExecution(context.Background(), adminActor, testProject, exec.ID); err != nil {
		t.Fatalf("ApproveExecution: %v", err)
	}
	approved, err := h.svc.GetExecution(context.Background(), testProject, exec.ID)
	if err != nil {
		t.Fatalf("GetExecution after approve: %v", err)
	}
	if approved.ApprovedBy != adminActor.ID {
		t.Fatalf("ApprovedBy = %q, want %q", approved.ApprovedBy, adminActor.ID)
	}

	final := waitTerminal(t, h.svc, testProject, exec.ID, 2*time.Second)
	if final.Status != StatusSucceeded {
		t.Fatalf("final status = %q, want succeeded", final.Status)
	}
}

func TestCreateExecution_DisabledPolicyRejected(t *testing.T) {
	h := newHarness(t, PolicyDisabled)
	h.seedNotebook("nb.ipynb", 1)

	_, _, err := h.svc.CreateExecution(context.Background(), memberActor, testProject, testNotebook, CreateExecutionRequest{
		Kind: KindNotebook,
		Path: "nb.ipynb",
	}, "")
	if err == nil {
		t.Fatal("CreateExecution succeeded despite mcp_policy=disabled")
	}
	if !isCode(err, ErrCodeApprovalRequired) {
		t.Fatalf("err = %v, want ErrCodeApprovalRequired", err)
	}
}

func TestCreateExecution_IdempotencyReplayAndPayloadConflict(t *testing.T) {
	h := newHarness(t, PolicyAllowed)
	h.seedNotebook("nb.ipynb", 1)

	req := CreateExecutionRequest{Kind: KindNotebook, Path: "nb.ipynb"}
	first, replayed, err := h.svc.CreateExecution(context.Background(), memberActor, testProject, testNotebook, req, "my-key")
	if err != nil {
		t.Fatalf("first CreateExecution: %v", err)
	}
	if replayed {
		t.Fatal("first CreateExecution reported replayed=true")
	}

	second, replayed2, err := h.svc.CreateExecution(context.Background(), memberActor, testProject, testNotebook, req, "my-key")
	if err != nil {
		t.Fatalf("second CreateExecution (replay): %v", err)
	}
	if !replayed2 {
		t.Fatal("second CreateExecution with the same key+payload did not report replayed=true")
	}
	if second.ID != first.ID {
		t.Fatalf("replay returned a different execution ID: %s != %s", second.ID, first.ID)
	}

	h.seedNotebook("other.ipynb", 1)
	differentReq := CreateExecutionRequest{Kind: KindNotebook, Path: "other.ipynb"}
	_, _, err = h.svc.CreateExecution(context.Background(), memberActor, testProject, testNotebook, differentReq, "my-key")
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("CreateExecution with same key but different payload: err = %v, want ErrConflict", err)
	}

	waitTerminal(t, h.svc, testProject, first.ID, 2*time.Second)
}

func TestCreateExecution_ConflictDetection(t *testing.T) {
	h := newHarness(t, PolicyAllowed)
	h.seedNotebook("nb.ipynb", 1)

	// Simulate a human editing the notebook in JupyterLab while the
	// execution's kernel is busy: mutate the "original" document via the
	// gateway (bypassing Piper) the moment the fake kernel executes.
	h.gateway.executeFn = func(_ context.Context, _ string, _ jupyter.OutputSink) (*jupyter.ExecuteResult, error) {
		concurrentEdit := jupyter.EmptyNotebook()
		concurrentEdit.AppendCodeCell("human-cell", "# edited concurrently in Jupyter UI")
		h.gateway.putDoc("nb.ipynb", concurrentEdit)
		return &jupyter.ExecuteResult{Status: "ok", ExecutionCount: 1}, nil
	}

	exec, _, err := h.svc.CreateExecution(context.Background(), memberActor, testProject, testNotebook, CreateExecutionRequest{
		Kind: KindNotebook,
		Path: "nb.ipynb",
	}, "")
	if err != nil {
		t.Fatalf("CreateExecution: %v", err)
	}

	final := waitTerminal(t, h.svc, testProject, exec.ID, 2*time.Second)
	if final.Status != StatusConflicted {
		t.Fatalf("final status = %q, want conflicted", final.Status)
	}
	if final.ErrorCode != ErrCodeContentConflict {
		t.Fatalf("ErrorCode = %q, want %q", final.ErrorCode, ErrCodeContentConflict)
	}

	// The recovery copy must still exist and contain the executed result,
	// even though the original was never overwritten.
	resultDoc, _, err := h.gateway.ReadNotebook(context.Background(), nil, final.ResultPath)
	if err != nil {
		t.Fatalf("ReadNotebook(result path): %v", err)
	}
	if resultDoc.Cells[0].ExecutionCount == nil {
		t.Fatal("result notebook missing execution output")
	}

	// The original must be exactly the human's concurrent edit — untouched
	// by Piper.
	original, _, err := h.gateway.ReadNotebook(context.Background(), nil, "nb.ipynb")
	if err != nil {
		t.Fatalf("ReadNotebook(original): %v", err)
	}
	if len(original.Cells) != 1 || original.Cells[0].ID != "human-cell" {
		t.Fatalf("original notebook was clobbered: %#v", original.Cells)
	}
}

func TestCancelExecution_AwaitingApproval(t *testing.T) {
	h := newHarness(t, PolicyApprovalRequired)
	h.seedNotebook("nb.ipynb", 1)

	exec, _, err := h.svc.CreateExecution(context.Background(), memberActor, testProject, testNotebook, CreateExecutionRequest{
		Kind: KindNotebook,
		Path: "nb.ipynb",
	}, "")
	if err != nil {
		t.Fatalf("CreateExecution: %v", err)
	}

	if err := h.svc.CancelExecution(context.Background(), memberActor, testProject, exec.ID); err != nil {
		t.Fatalf("CancelExecution: %v", err)
	}
	got, err := h.svc.GetExecution(context.Background(), testProject, exec.ID)
	if err != nil {
		t.Fatalf("GetExecution: %v", err)
	}
	if got.Status != StatusCancelled {
		t.Fatalf("status = %q, want cancelled", got.Status)
	}
	if got.ErrorCode != ErrCodeExecutionCancelled {
		t.Fatalf("ErrorCode = %q, want %q", got.ErrorCode, ErrCodeExecutionCancelled)
	}

	// A different member cannot cancel someone else's execution.
	exec2, _, err := h.svc.CreateExecution(context.Background(), memberActor, testProject, testNotebook, CreateExecutionRequest{Kind: KindNotebook, Path: "nb.ipynb"}, "")
	if err != nil {
		t.Fatalf("CreateExecution 2: %v", err)
	}
	other := Actor{ID: "eve", Role: security.ProjectRoleMember, ClientID: "rest"}
	if err := h.svc.CancelExecution(context.Background(), other, testProject, exec2.ID); !errors.Is(err, ErrForbidden) {
		t.Fatalf("CancelExecution by non-owner: err = %v, want ErrForbidden", err)
	}
	// Admin can cancel anyone's execution.
	if err := h.svc.CancelExecution(context.Background(), adminActor, testProject, exec2.ID); err != nil {
		t.Fatalf("CancelExecution by admin: %v", err)
	}
}

func TestCancelExecution_Running(t *testing.T) {
	h := newHarness(t, PolicyAllowed)
	h.seedNotebook("nb.ipynb", 1)

	started := make(chan struct{})
	h.gateway.executeFn = func(ctx context.Context, _ string, _ jupyter.OutputSink) (*jupyter.ExecuteResult, error) {
		close(started)
		<-ctx.Done()
		return nil, ctx.Err()
	}

	exec, _, err := h.svc.CreateExecution(context.Background(), memberActor, testProject, testNotebook, CreateExecutionRequest{
		Kind: KindNotebook,
		Path: "nb.ipynb",
	}, "")
	if err != nil {
		t.Fatalf("CreateExecution: %v", err)
	}

	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("execution never reached the running cell")
	}
	waitStatus(t, h.svc, testProject, exec.ID, StatusRunning, time.Second)

	if err := h.svc.CancelExecution(context.Background(), memberActor, testProject, exec.ID); err != nil {
		t.Fatalf("CancelExecution: %v", err)
	}

	final := waitTerminal(t, h.svc, testProject, exec.ID, 2*time.Second)
	if final.Status != StatusCancelled {
		t.Fatalf("final status = %q, want cancelled", final.Status)
	}
	if h.gateway.interruptCalls == 0 {
		t.Fatal("cancel of a running execution did not call InterruptKernel")
	}
}

func TestRecoverOnStartup_RunningIsMarkedUncertain(t *testing.T) {
	h := newHarness(t, PolicyAllowed)
	id := uuid.NewString()
	now := time.Now().UTC()
	if err := h.repo.CreateExecution(context.Background(), &NotebookExecution{
		ID: id, ProjectID: testProject, NotebookName: testNotebook, NotebookPath: "nb.ipynb",
		ResultPath: resultPathFor(id), Kind: KindNotebook, Status: StatusRunning,
		RequestedBy: "alice", QueuedAt: now, StartedAt: &now, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("seed running execution: %v", err)
	}

	if err := h.svc.RecoverOnStartup(context.Background()); err != nil {
		t.Fatalf("RecoverOnStartup: %v", err)
	}

	got, err := h.svc.GetExecution(context.Background(), testProject, id)
	if err != nil {
		t.Fatalf("GetExecution: %v", err)
	}
	if got.Status != StatusFailed {
		t.Fatalf("status = %q, want failed", got.Status)
	}
	if got.ErrorCode != ErrCodeRecoveryUncertain {
		t.Fatalf("ErrorCode = %q, want %q", got.ErrorCode, ErrCodeRecoveryUncertain)
	}
}

func TestRecoverOnStartup_QueuedIsRedispatched(t *testing.T) {
	h := newHarness(t, PolicyAllowed)
	h.seedNotebook("nb.ipynb", 1)
	_, baseHash, err := h.gateway.ReadNotebook(context.Background(), nil, "nb.ipynb")
	if err != nil {
		t.Fatalf("read seeded notebook hash: %v", err)
	}
	id := uuid.NewString()
	now := time.Now().UTC()
	if err := h.repo.CreateExecution(context.Background(), &NotebookExecution{
		ID: id, ProjectID: testProject, NotebookName: testNotebook, NotebookPath: "nb.ipynb",
		ResultPath: resultPathFor(id), Kind: KindNotebook, Status: StatusQueued,
		RequestedBy: "alice", QueuedAt: now, UpdatedAt: now, BaseContentHash: baseHash,
	}); err != nil {
		t.Fatalf("seed queued execution: %v", err)
	}

	if err := h.svc.RecoverOnStartup(context.Background()); err != nil {
		t.Fatalf("RecoverOnStartup: %v", err)
	}

	final := waitTerminal(t, h.svc, testProject, id, 2*time.Second)
	if final.Status != StatusSucceeded {
		t.Fatalf("recovered queued execution ended as %q (code=%s), want succeeded", final.Status, final.ErrorCode)
	}
}

func TestRecoverOnStartup_QueuedFailsWhenServerNotRunning(t *testing.T) {
	h := newHarness(t, PolicyAllowed)
	h.notebooks.put(&notebook.NotebookServer{ProjectID: testProject, Name: testNotebook, Status: notebook.StatusStopped})
	id := uuid.NewString()
	now := time.Now().UTC()
	if err := h.repo.CreateExecution(context.Background(), &NotebookExecution{
		ID: id, ProjectID: testProject, NotebookName: testNotebook, NotebookPath: "nb.ipynb",
		ResultPath: resultPathFor(id), Kind: KindNotebook, Status: StatusQueued,
		RequestedBy: "alice", QueuedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("seed queued execution: %v", err)
	}

	if err := h.svc.RecoverOnStartup(context.Background()); err != nil {
		t.Fatalf("RecoverOnStartup: %v", err)
	}

	got, err := h.svc.GetExecution(context.Background(), testProject, id)
	if err != nil {
		t.Fatalf("GetExecution: %v", err)
	}
	if got.Status != StatusFailed || got.ErrorCode != ErrCodeNotebookNotRunning {
		t.Fatalf("status/code = %s/%s, want failed/notebook_not_running", got.Status, got.ErrorCode)
	}
}

func TestCellExecution_AppendAndReplace(t *testing.T) {
	h := newHarness(t, PolicyAllowed)
	h.seedNotebook("nb.ipynb", 0)

	exec, _, err := h.svc.CreateExecution(context.Background(), memberActor, testProject, testNotebook, CreateExecutionRequest{
		Kind: KindCell,
		Path: "nb.ipynb",
		Edit: &CellEdit{Mode: CellEditAppend, Code: "df.describe()"},
	}, "")
	if err != nil {
		t.Fatalf("CreateExecution (append): %v", err)
	}
	if exec.SourceSHA256 == "" {
		t.Fatal("SourceSHA256 was not recorded for a cell execution")
	}
	final := waitTerminal(t, h.svc, testProject, exec.ID, 2*time.Second)
	if final.Status != StatusSucceeded {
		t.Fatalf("append execution ended as %q (code=%s)", final.Status, final.ErrorCode)
	}
	doc, _, err := h.gateway.ReadNotebook(context.Background(), nil, "nb.ipynb")
	if err != nil {
		t.Fatalf("ReadNotebook: %v", err)
	}
	if len(doc.Cells) != 1 || doc.Cells[0].Source.String() != "df.describe()" {
		t.Fatalf("appended cell not found in original notebook: %#v", doc.Cells)
	}

	cellID := doc.Cells[0].ID
	exec2, _, err := h.svc.CreateExecution(context.Background(), memberActor, testProject, testNotebook, CreateExecutionRequest{
		Kind: KindCell,
		Path: "nb.ipynb",
		Edit: &CellEdit{Mode: CellEditReplace, Code: "df.info()", CellID: cellID},
	}, "")
	if err != nil {
		t.Fatalf("CreateExecution (replace): %v", err)
	}
	final2 := waitTerminal(t, h.svc, testProject, exec2.ID, 2*time.Second)
	if final2.Status != StatusSucceeded {
		t.Fatalf("replace execution ended as %q (code=%s)", final2.Status, final2.ErrorCode)
	}
	doc2, _, err := h.gateway.ReadNotebook(context.Background(), nil, "nb.ipynb")
	if err != nil {
		t.Fatalf("ReadNotebook after replace: %v", err)
	}
	if len(doc2.Cells) != 1 || doc2.Cells[0].Source.String() != "df.info()" {
		t.Fatalf("replaced cell not applied: %#v", doc2.Cells)
	}
}

func TestCellExecution_CreateIfMissing(t *testing.T) {
	h := newHarness(t, PolicyAllowed)
	// deliberately do not seed "new.ipynb"

	exec, _, err := h.svc.CreateExecution(context.Background(), memberActor, testProject, testNotebook, CreateExecutionRequest{
		Kind:            KindCell,
		Path:            "new.ipynb",
		Edit:            &CellEdit{Mode: CellEditAppend, Code: "1 + 1"},
		CreateIfMissing: true,
	}, "")
	if err != nil {
		t.Fatalf("CreateExecution: %v", err)
	}
	final := waitTerminal(t, h.svc, testProject, exec.ID, 2*time.Second)
	if final.Status != StatusSucceeded {
		t.Fatalf("status = %q (code=%s), want succeeded", final.Status, final.ErrorCode)
	}
	doc, _, err := h.gateway.ReadNotebook(context.Background(), nil, "new.ipynb")
	if err != nil {
		t.Fatalf("ReadNotebook(new.ipynb): %v", err)
	}
	if len(doc.Cells) != 1 {
		t.Fatalf("expected the new notebook to have 1 cell, got %d", len(doc.Cells))
	}
}

func TestCreateExecution_RejectsBadPath(t *testing.T) {
	h := newHarness(t, PolicyAllowed)
	_, _, err := h.svc.CreateExecution(context.Background(), memberActor, testProject, testNotebook, CreateExecutionRequest{
		Kind: KindNotebook,
		Path: "../escape.ipynb",
	}, "")
	if !isCode(err, ErrCodePathInvalid) {
		t.Fatalf("err = %v, want ErrCodePathInvalid", err)
	}
}

func TestCreateExecution_RejectsTooLongIdempotencyKey(t *testing.T) {
	h := newHarness(t, PolicyAllowed)
	longKey := make([]byte, MaxIdempotencyKeyLen+1)
	for i := range longKey {
		longKey[i] = 'a'
	}
	_, _, err := h.svc.CreateExecution(context.Background(), memberActor, testProject, testNotebook, CreateExecutionRequest{
		Kind: KindNotebook,
		Path: "nb.ipynb",
	}, string(longKey))
	if !isCode(err, ErrCodePathInvalid) {
		t.Fatalf("err = %v, want ErrCodePathInvalid", err)
	}
}
