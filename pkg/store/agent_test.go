package store

import (
	"testing"
)

// ─── Agent ────────────────────────────────────────────────────────────────────

func TestCreateAndGetAgent(t *testing.T) {
	st := openTestStore(t)
	a := &Agent{
		ID:       "agent-1",
		TaskID:   "task-1",
		RunID:    "run-1",
		StepName: "train",
	}
	if err := st.CreateAgent(a); err != nil {
		t.Fatal(err)
	}

	got, err := st.GetAgent("agent-1")
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != AgentStatusPending {
		t.Errorf("want pending, got %q", got.Status)
	}
	if got.StartedAt != nil {
		t.Error("started_at should be nil on creation")
	}
}

func TestStartAgent(t *testing.T) {
	st := openTestStore(t)
	_ = st.CreateAgent(&Agent{ID: "a1", TaskID: "t1", RunID: "r1", StepName: "s1"})

	if err := st.StartAgent("a1"); err != nil {
		t.Fatal(err)
	}

	got, _ := st.GetAgent("a1")
	if got.Status != AgentStatusRunning {
		t.Errorf("want running, got %q", got.Status)
	}
	if got.StartedAt == nil {
		t.Error("started_at should be set after StartAgent")
	}
}

func TestFinishAgent_succeeded(t *testing.T) {
	st := openTestStore(t)
	_ = st.CreateAgent(&Agent{ID: "a1", TaskID: "t1", RunID: "r1", StepName: "s1"})
	_ = st.StartAgent("a1")

	if err := st.FinishAgent("a1", AgentStatusSucceeded, ""); err != nil {
		t.Fatal(err)
	}

	got, _ := st.GetAgent("a1")
	if got.Status != AgentStatusSucceeded {
		t.Errorf("want succeeded, got %q", got.Status)
	}
	if got.EndedAt == nil {
		t.Error("ended_at should be set after FinishAgent")
	}
	if got.Error != "" {
		t.Errorf("error should be empty, got %q", got.Error)
	}
}

func TestFinishAgent_failed(t *testing.T) {
	st := openTestStore(t)
	_ = st.CreateAgent(&Agent{ID: "a1", TaskID: "t1", RunID: "r1", StepName: "s1"})
	_ = st.StartAgent("a1")

	if err := st.FinishAgent("a1", AgentStatusFailed, "OOM killed"); err != nil {
		t.Fatal(err)
	}

	got, _ := st.GetAgent("a1")
	if got.Status != AgentStatusFailed {
		t.Errorf("want failed, got %q", got.Status)
	}
	if got.Error != "OOM killed" {
		t.Errorf("error: want 'OOM killed', got %q", got.Error)
	}
}

func TestListAgentsByRun(t *testing.T) {
	st := openTestStore(t)
	_ = st.CreateAgent(&Agent{ID: "a1", TaskID: "t1", RunID: "run-1", StepName: "preprocess"})
	_ = st.CreateAgent(&Agent{ID: "a2", TaskID: "t2", RunID: "run-1", StepName: "train"})
	_ = st.CreateAgent(&Agent{ID: "a3", TaskID: "t3", RunID: "run-2", StepName: "train"})

	agents, err := st.ListAgentsByRun("run-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(agents) != 2 {
		t.Errorf("want 2 agents for run-1, got %d", len(agents))
	}

	agents, _ = st.ListAgentsByRun("run-2")
	if len(agents) != 1 {
		t.Errorf("want 1 agent for run-2, got %d", len(agents))
	}
}

func TestListAgentsByRun_empty(t *testing.T) {
	st := openTestStore(t)
	agents, err := st.ListAgentsByRun("no-such-run")
	if err != nil {
		t.Fatal(err)
	}
	if len(agents) != 0 {
		t.Errorf("want 0, got %d", len(agents))
	}
}
