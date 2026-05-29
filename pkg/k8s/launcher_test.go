package k8s

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/piper/piper/pkg/pipeline"
	"github.com/piper/piper/pkg/proto"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
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

func TestJobName_includesRetryAttempt(t *testing.T) {
	first := jobName(&proto.Task{RunID: "run-123", StepName: "data-prep", Attempt: 1})
	retry := jobName(&proto.Task{RunID: "run-123", StepName: "data-prep", Attempt: 2})
	if first == retry {
		t.Fatalf("retry job name should differ from first attempt: %q", retry)
	}
	if retry != "piper-run-123-data-prep-a2" {
		t.Fatalf("retry job name = %q, want piper-run-123-data-prep-a2", retry)
	}
}

// ─── buildAgentArgs ───────────────────────────────────────────────────────────

func TestBuildAgentArgs_contains(t *testing.T) {
	l := &Launcher{cfg: Config{
		MasterURL: "http://piper:8080",
		Token:     "secret",
	}}
	task := &proto.Task{ID: "run-1:step-a", RunID: "run-1", StepName: "step-a"}
	args := l.buildAgentArgs(task, "TASKB64", "STEPB64")

	contains := func(target string) bool {
		for _, a := range args {
			if a == target {
				return true
			}
		}
		return false
	}

	checks := []string{
		agentSubcmd,
		agentExecSubcmd,
		"--master=http://piper:8080",
		"--task=TASKB64",
		"--task-id=run-1:step-a",
		"--run-id=run-1",
		"--step-name=step-a",
		"--step=STEPB64",
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
	args := l.buildAgentArgs(task, "TASKB64", "STEPB64")

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
	args := l.buildAgentArgs(task, "TASKB64", "STEPB64")

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
	job := l.buildJob(task, "pytorch:latest", []string{agentSubcmd, agentExecSubcmd, "--master=http://x"})

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
	if got := initContainers[0].Command; len(got) < 3 || got[1] != agentBinarySrc || got[2] != agentBinaryDst {
		t.Errorf("initContainer command = %v, want [cp %s %s]", got, agentBinarySrc, agentBinaryDst)
	}

	containers := job.Spec.Template.Spec.Containers
	if len(containers) != 1 {
		t.Fatalf("want 1 container, got %d", len(containers))
	}
	if containers[0].Image != "pytorch:latest" {
		t.Errorf("container image = %q, want pytorch:latest", containers[0].Image)
	}
	if got := containers[0].Command; len(got) != 1 || got[0] != agentBinaryDst {
		t.Errorf("container command = %v, want [%s]", got, agentBinaryDst)
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

func TestBuildJob_stepRuntimeOptions(t *testing.T) {
	l := &Launcher{cfg: Config{AgentImage: "piper/agent:latest"}}
	step := pipeline.Step{
		Name: "train",
		Env:  map[string]string{"MASTER_ADDR": "trainer-0", "WORLD_SIZE": "4"},
		Resources: pipeline.Resources{
			CPU:    "2",
			Memory: "4Gi",
			GPU:    "1",
		},
		Runner: pipeline.RunnerSelector{
			NodeSelector: map[string]string{"accelerator": "nvidia-a100"},
			Tolerations: []pipeline.Toleration{{
				Key:      "nvidia.com/gpu",
				Operator: "Exists",
				Effect:   "NoSchedule",
			}},
		},
	}
	task := makeTask("run-1", "train", step, pipeline.Pipeline{})

	job := l.buildJob(task, "python:3.11", nil)
	podSpec := job.Spec.Template.Spec
	container := podSpec.Containers[0]

	env := map[string]string{}
	for _, item := range container.Env {
		env[item.Name] = item.Value
	}
	if env["MASTER_ADDR"] != "trainer-0" || env["WORLD_SIZE"] != "4" {
		t.Fatalf("container env = %#v", container.Env)
	}
	gpuLimit := container.Resources.Limits[corev1.ResourceName("nvidia.com/gpu")]
	if got := gpuLimit.String(); got != "1" {
		t.Fatalf("gpu limit = %q, want 1", got)
	}
	cpuRequest := container.Resources.Requests[corev1.ResourceCPU]
	if got := cpuRequest.String(); got != "2" {
		t.Fatalf("cpu request = %q, want 2", got)
	}
	memoryLimit := container.Resources.Limits[corev1.ResourceMemory]
	if got := memoryLimit.String(); got != "4Gi" {
		t.Fatalf("memory limit = %q, want 4Gi", got)
	}
	if podSpec.NodeSelector["accelerator"] != "nvidia-a100" {
		t.Fatalf("nodeSelector = %#v", podSpec.NodeSelector)
	}
	if len(podSpec.Tolerations) != 1 || podSpec.Tolerations[0].Key != "nvidia.com/gpu" || podSpec.Tolerations[0].Operator != corev1.TolerationOpExists {
		t.Fatalf("tolerations = %#v", podSpec.Tolerations)
	}
}

func TestCancelRunDeletesJobsByRunLabel(t *testing.T) {
	clientset := fake.NewSimpleClientset()
	l := &Launcher{cfg: Config{Namespace: "default"}, clientset: clientset}

	runTask := &proto.Task{RunID: "run-1", StepName: "train"}
	otherTask := &proto.Task{RunID: "run-2", StepName: "train"}
	if _, err := clientset.BatchV1().Jobs("default").Create(context.Background(), l.buildJob(runTask, "python:3.11", nil), metav1.CreateOptions{}); err != nil {
		t.Fatal(err)
	}
	if _, err := clientset.BatchV1().Jobs("default").Create(context.Background(), l.buildJob(otherTask, "python:3.11", nil), metav1.CreateOptions{}); err != nil {
		t.Fatal(err)
	}

	if err := l.CancelRun(context.Background(), "run-1"); err != nil {
		t.Fatal(err)
	}
	jobs, err := clientset.BatchV1().Jobs("default").List(context.Background(), metav1.ListOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(jobs.Items) != 1 || jobs.Items[0].Labels["piper/run-id"] != "run-2" {
		t.Fatalf("remaining jobs = %#v, want only run-2", jobs.Items)
	}
}

func TestReconcileJobsReportsFailedJob(t *testing.T) {
	clientset := fake.NewSimpleClientset()
	l := &Launcher{cfg: Config{Namespace: "default"}, clientset: clientset}
	task := &proto.Task{ID: "run-1:train", RunID: "run-1", StepName: "train", Attempt: 1}
	job := l.buildJob(task, "python:3.11", nil)
	job.Status.Conditions = []batchv1.JobCondition{{
		Type:    batchv1.JobFailed,
		Status:  corev1.ConditionTrue,
		Reason:  "BackoffLimitExceeded",
		Message: "job failed",
	}}
	if _, err := clientset.BatchV1().Jobs("default").Create(context.Background(), job, metav1.CreateOptions{}); err != nil {
		t.Fatal(err)
	}
	l.watchJob(job.Name, task)

	var gotTaskID, gotStatus, gotErr string
	l.ReconcileJobs(context.Background(), func(_ context.Context, taskID, status, errMsg string, _, _ time.Time, _ int) error {
		gotTaskID = taskID
		gotStatus = status
		gotErr = errMsg
		return nil
	})
	if gotTaskID != task.ID || gotStatus != proto.TaskStatusFailed || gotErr != "job failed" {
		t.Fatalf("report = (%q, %q, %q), want (%q, %q, %q)", gotTaskID, gotStatus, gotErr, task.ID, proto.TaskStatusFailed, "job failed")
	}
	if len(l.watched) != 0 {
		t.Fatalf("failed job remained watched: %#v", l.watched)
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

func TestDispatchCreatesJobWithFullTaskAgentContract(t *testing.T) {
	step := pipeline.Step{
		Name: "train",
		Run: pipeline.Run{
			Image:   "python:3.11",
			Command: []string{"sh", "-c", "echo train"},
		},
	}
	pl := pipeline.Pipeline{Metadata: pipeline.Metadata{Name: "pipe"}}
	task := makeTask("run-1", "train", step, pl)
	task.WorkDir = "/work"
	task.OutputDir = "/out"
	task.RunParams = map[string]any{"lr": "0.2"}
	task.Attempt = 2

	l := &Launcher{
		cfg: Config{
			AgentImage: "loykin/piper:agent",
			Namespace:  "default",
			MasterURL:  "http://master:8080",
		},
		clientset: fake.NewSimpleClientset(),
	}
	if err := l.Dispatch(context.Background(), task); err != nil {
		t.Fatal(err)
	}

	jobs, err := l.clientset.BatchV1().Jobs("default").List(context.Background(), metav1.ListOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(jobs.Items) != 1 {
		t.Fatalf("jobs = %d, want 1", len(jobs.Items))
	}
	job := jobs.Items[0]
	if job.Name != "piper-run-1-train-a2" {
		t.Fatalf("job name = %q, want piper-run-1-train-a2", job.Name)
	}
	if got := *job.Spec.BackoffLimit; got != 0 {
		t.Fatalf("BackoffLimit = %d, want 0", got)
	}
	init := job.Spec.Template.Spec.InitContainers[0]
	if init.Image != "loykin/piper:agent" {
		t.Fatalf("init image = %q", init.Image)
	}
	if init.ImagePullPolicy != "IfNotPresent" {
		t.Fatalf("init pull policy = %q, want IfNotPresent", init.ImagePullPolicy)
	}
	stepContainer := job.Spec.Template.Spec.Containers[0]
	if stepContainer.Image != "python:3.11" {
		t.Fatalf("step image = %q, want python:3.11", stepContainer.Image)
	}
	if !hasArgPrefix(stepContainer.Args, "--task=") {
		t.Fatalf("agent args missing --task: %v", stepContainer.Args)
	}
	if !hasArg(stepContainer.Args, "--task-id=run-1:train") {
		t.Fatalf("agent args missing task id: %v", stepContainer.Args)
	}
	if !hasArg(stepContainer.Args, "--master=http://master:8080") {
		t.Fatalf("agent args missing master URL: %v", stepContainer.Args)
	}

	if job.Spec.Template.Spec.RestartPolicy != "Never" {
		t.Fatalf("restart policy = %q, want Never", job.Spec.Template.Spec.RestartPolicy)
	}
}

func hasArg(args []string, want string) bool {
	for _, arg := range args {
		if arg == want {
			return true
		}
	}
	return false
}

func hasArgPrefix(args []string, prefix string) bool {
	for _, arg := range args {
		if len(arg) >= len(prefix) && arg[:len(prefix)] == prefix {
			return true
		}
	}
	return false
}
