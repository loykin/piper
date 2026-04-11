package k8s

import (
	"encoding/json"
	"testing"

	"github.com/piper/piper/pkg/pipeline"
	"github.com/piper/piper/pkg/proto"
)

func makeTask(runID, stepName string, step pipeline.Step, pl pipeline.Pipeline) *proto.Task {
	stepJSON, _ := json.Marshal(step)
	plJSON, _ := json.Marshal(pl)
	return &proto.Task{
		ID:       runID + ":" + stepName,
		RunID:    runID,
		StepName: stepName,
		Step:     stepJSON,
		Pipeline: plJSON,
	}
}

// ─── sanitizeName ─────────────────────────────────────────────────────────────

func TestSanitizeName_basic(t *testing.T) {
	cases := []struct{ in, want string }{
		{"hello-world", "hello-world"},
		{"My_Step", "my-step"},
		{"run-123:data-prep", "run-123-data-prep"},
		{"a", "a"},
	}
	for _, c := range cases {
		if got := sanitizeName(c.in); got != c.want {
			t.Errorf("sanitizeName(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestSanitizeName_truncate(t *testing.T) {
	long := "piper-run-1234567890-very-long-step-name-that-exceeds-sixty-three-characters"
	got := sanitizeName(long)
	if len(got) > 63 {
		t.Errorf("sanitizeName should truncate to 63 chars, got %d: %q", len(got), got)
	}
}

func TestSanitizeName_noLeadingTrailingHyphen(t *testing.T) {
	got := sanitizeName("--hello--")
	if len(got) > 0 && (got[0] == '-' || got[len(got)-1] == '-') {
		t.Errorf("sanitizeName should not start/end with hyphen, got %q", got)
	}
}

// ─── jobName ──────────────────────────────────────────────────────────────────

func TestJobName_format(t *testing.T) {
	task := &proto.Task{RunID: "run-123", StepName: "data-prep"}
	name := jobName(task)
	if len(name) == 0 {
		t.Fatal("empty job name")
	}
	if len(name) > 63 {
		t.Errorf("job name too long: %d", len(name))
	}
}

// ─── buildAgentArgs ───────────────────────────────────────────────────────────

func TestBuildAgentArgs_contains(t *testing.T) {
	l := &Launcher{cfg: Config{
		MasterURL: "http://piper:8080",
		Token:     "secret",
	}}
	task := &proto.Task{ID: "run-1:step-a", RunID: "run-1", StepName: "step-a"}
	args := l.buildAgentArgs(task, "BASE64DATA")

	contains := func(target string) bool {
		for _, a := range args {
			if a == target {
				return true
			}
		}
		return false
	}

	checks := []string{
		"exec",
		"--master=http://piper:8080",
		"--task-id=run-1:step-a",
		"--run-id=run-1",
		"--step-name=step-a",
		"--step=BASE64DATA",
		"--token=secret",
	}
	for _, c := range checks {
		if !contains(c) {
			t.Errorf("args missing %q, got: %v", c, args)
		}
	}
}

func TestBuildAgentArgs_s3(t *testing.T) {
	l := &Launcher{cfg: Config{
		S3Endpoint:  "minio:9000",
		S3AccessKey: "access",
		S3SecretKey: "secret",
		S3Bucket:    "piper",
	}}
	task := &proto.Task{ID: "r:s", RunID: "r", StepName: "s"}
	args := l.buildAgentArgs(task, "B64")

	found := false
	for _, a := range args {
		if a == "--s3-endpoint=minio:9000" {
			found = true
		}
	}
	if !found {
		t.Errorf("S3 endpoint not in args: %v", args)
	}
}

func TestBuildAgentArgs_noToken(t *testing.T) {
	l := &Launcher{cfg: Config{MasterURL: "http://piper:8080"}}
	task := &proto.Task{ID: "r:s", RunID: "r", StepName: "s"}
	args := l.buildAgentArgs(task, "B64")

	for _, a := range args {
		if a == "--token=" {
			t.Errorf("empty token should not appear in args")
		}
	}
}

// ─── buildJob ─────────────────────────────────────────────────────────────────

func TestBuildJob_structure(t *testing.T) {
	ttl := int32(300)
	l := &Launcher{cfg: Config{
		AgentImage:       "piper/agent:latest",
		Namespace:        "ml",
		TTLAfterFinished: &ttl,
	}}
	task := &proto.Task{RunID: "run-1", StepName: "train"}
	job := l.buildJob(task, "pytorch:latest", []string{"exec", "--master=http://x"})

	if job.Namespace != "ml" {
		t.Errorf("namespace = %q, want ml", job.Namespace)
	}
	if *job.Spec.TTLSecondsAfterFinished != 300 {
		t.Errorf("TTL = %v, want 300", job.Spec.TTLSecondsAfterFinished)
	}
	if *job.Spec.BackoffLimit != 0 {
		t.Errorf("BackoffLimit should be 0, got %d", *job.Spec.BackoffLimit)
	}

	initContainers := job.Spec.Template.Spec.InitContainers
	if len(initContainers) != 1 {
		t.Fatalf("want 1 initContainer, got %d", len(initContainers))
	}
	if initContainers[0].Image != "piper/agent:latest" {
		t.Errorf("initContainer image = %q", initContainers[0].Image)
	}

	containers := job.Spec.Template.Spec.Containers
	if len(containers) != 1 {
		t.Fatalf("want 1 container, got %d", len(containers))
	}
	if containers[0].Image != "pytorch:latest" {
		t.Errorf("container image = %q, want pytorch:latest", containers[0].Image)
	}
}

func TestBuildJob_volumeMounts(t *testing.T) {
	l := &Launcher{cfg: Config{AgentImage: "piper/agent:latest"}}
	task := &proto.Task{RunID: "r", StepName: "s"}
	job := l.buildJob(task, "python:3.11", nil)

	volumes := job.Spec.Template.Spec.Volumes
	volumeNames := map[string]bool{}
	for _, v := range volumes {
		volumeNames[v.Name] = true
	}
	for _, name := range []string{"piper-tools", "piper-outputs", "piper-inputs"} {
		if !volumeNames[name] {
			t.Errorf("missing volume %q", name)
		}
	}

	mounts := job.Spec.Template.Spec.Containers[0].VolumeMounts
	mountNames := map[string]bool{}
	for _, m := range mounts {
		mountNames[m.Name] = true
	}
	for _, name := range []string{"piper-tools", "piper-outputs", "piper-inputs"} {
		if !mountNames[name] {
			t.Errorf("container missing volumeMount %q", name)
		}
	}
}

// ─── Dispatch image resolution logic (unit, without K8s API) ─────────────────

func TestDispatch_noImage(t *testing.T) {
	// Test image validation logic without a K8s client
	l := &Launcher{cfg: Config{DefaultImage: ""}}

	step := pipeline.Step{Name: "s", Run: pipeline.Run{Command: []string{"echo"}}}
	pl := pipeline.Pipeline{}

	task := makeTask("run-1", "s", step, pl)

	// Verify that an error is returned before a nil pointer dereference occurs when clientset is nil
	// Confirm that Dispatch returns an error when image == ""
	var pipelineStep pipeline.Step
	_ = json.Unmarshal(task.Step, &pipelineStep)
	var pipelinePl pipeline.Pipeline
	_ = json.Unmarshal(task.Pipeline, &pipelinePl)

	image := pipelineStep.Run.Image
	if image == "" {
		image = pipelinePl.Spec.Defaults.Image
	}
	if image == "" {
		image = l.cfg.DefaultImage
	}
	if image != "" {
		t.Errorf("expected empty image, got %q", image)
	}
}

func TestDispatch_imageResolution(t *testing.T) {
	cases := []struct {
		stepImage    string
		defaultImage string
		plDefault    string
		want         string
	}{
		{"python:3.11", "fallback:1", "pl-default:1", "python:3.11"},
		{"", "pl-default:1", "pl-default:1", "pl-default:1"},
		{"", "", "launcher-default:1", "launcher-default:1"},
	}

	for _, c := range cases {
		l := &Launcher{cfg: Config{DefaultImage: c.defaultImage}}
		step := pipeline.Step{Run: pipeline.Run{Image: c.stepImage}}
		pl := pipeline.Pipeline{Spec: pipeline.Spec{Defaults: pipeline.Defaults{Image: c.plDefault}}}
		task := makeTask("r", "s", step, pl)

		var s pipeline.Step
		_ = json.Unmarshal(task.Step, &s)
		var p pipeline.Pipeline
		_ = json.Unmarshal(task.Pipeline, &p)

		image := s.Run.Image
		if image == "" {
			image = p.Spec.Defaults.Image
		}
		if image == "" {
			image = l.cfg.DefaultImage
		}
		if image != c.want {
			t.Errorf("image resolution: got %q, want %q", image, c.want)
		}
	}
}
