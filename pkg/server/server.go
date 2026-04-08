package server

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	"github.com/piper/piper/pkg/pipeline"
	"github.com/piper/piper/pkg/proto"
)

type Server struct {
	queue     *Queue
	outputDir string
	mux       *http.ServeMux
}

func New(outputDir string) *Server {
	s := &Server{
		queue:     NewQueue(),
		outputDir: outputDir,
		mux:       http.NewServeMux(),
	}
	s.routes()
	return s
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mux.ServeHTTP(w, r)
}

func (s *Server) routes() {
	s.mux.HandleFunc("/api/runs", s.handleRuns)
	s.mux.HandleFunc("/api/tasks/next", s.handleTaskNext)
	s.mux.HandleFunc("/api/tasks/", s.handleTaskResult)
}

// POST /api/runs
func (s *Server) handleRuns(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req proto.RunRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	p, err := pipeline.Parse([]byte(req.PipelineYAML))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	dag, err := pipeline.BuildDAG(p)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	runID := fmt.Sprintf("run-%d", time.Now().UnixMilli())
	workDir := req.WorkDir
	outputDir := req.OutputDir
	if outputDir == "" {
		outputDir = filepath.Join(s.outputDir, runID)
	}

	s.queue.AddRun(p, dag, runID, workDir, outputDir)
	slog.Info("run registered", "run_id", runID, "steps", len(p.Spec.Steps))

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(proto.RunResponse{RunID: runID})
}

// GET /api/tasks/next?label=gpu
func (s *Server) handleTaskNext(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	label := r.URL.Query().Get("label")
	task := s.queue.Next(label)
	if task == nil {
		w.WriteHeader(http.StatusNoContent)
		return
	}

	slog.Info("task assigned", "task_id", task.ID, "label", label)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(task)
}

// POST /api/tasks/{id}/done  또는  /api/tasks/{id}/failed
func (s *Server) handleTaskResult(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/api/tasks/"), "/")
	if len(parts) != 2 {
		http.Error(w, "invalid path", http.StatusBadRequest)
		return
	}
	taskID, action := parts[0], parts[1]
	if action != "done" && action != "failed" {
		http.Error(w, "unknown action", http.StatusBadRequest)
		return
	}

	var result proto.TaskResult
	if err := json.NewDecoder(r.Body).Decode(&result); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	result.TaskID = taskID
	result.Status = action

	if err := s.queue.Complete(&result); err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}

	slog.Info("task completed", "task_id", taskID, "status", action)
	w.WriteHeader(http.StatusOK)
}
