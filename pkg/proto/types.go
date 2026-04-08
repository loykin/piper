package proto

import "time"

// Task는 server가 worker에게 전달하는 실행 단위
type Task struct {
	ID         string            `json:"id"`
	RunID      string            `json:"run_id"`
	StepName   string            `json:"step_name"`
	Step       []byte            `json:"step"`       // pipeline.Step JSON
	Pipeline   []byte            `json:"pipeline"`   // pipeline.Pipeline JSON
	WorkDir    string            `json:"work_dir"`
	OutputDir  string            `json:"output_dir"`
	CreatedAt  time.Time         `json:"created_at"`
	Label      string            `json:"label"`      // 이 task를 처리할 worker label
}

// TaskResult는 worker가 server에 보고하는 결과
type TaskResult struct {
	TaskID    string    `json:"task_id"`
	Status    string    `json:"status"` // done | failed
	Error     string    `json:"error,omitempty"`
	StartedAt time.Time `json:"started_at"`
	EndedAt   time.Time `json:"ended_at"`
	Attempts  int       `json:"attempts"`
}

// RunRequest는 클라이언트가 파이프라인 실행을 요청할 때 사용
type RunRequest struct {
	PipelineYAML string            `json:"pipeline_yaml"`
	Params       map[string]any    `json:"params,omitempty"`
	WorkDir      string            `json:"work_dir,omitempty"`
	OutputDir    string            `json:"output_dir,omitempty"`
}

// RunResponse는 실행 요청에 대한 응답
type RunResponse struct {
	RunID string `json:"run_id"`
}
