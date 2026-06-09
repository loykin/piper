package k8s

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/piper/piper/internal/proto"
	"github.com/piper/piper/pkg/manifest"
	"github.com/piper/piper/pkg/pipeline"
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

func TestBuildJob_agentPullPolicyDoesNotControlStepImage(t *testing.T) {
	l := &Launcher{cfg: Config{
		AgentImage:           "piper/agent:latest",
		AgentImagePullPolicy: string(corev1.PullNever),
	}}
	task := &proto.Task{RunID: "r", StepName: "s"}
	job := l.buildJob(task, "alpine:3.20", []string{agentSubcmd, agentExecSubcmd, "--task=TASK"})

	init := job.Spec.Template.Spec.InitContainers[0]
	if init.ImagePullPolicy != corev1.PullNever {
		t.Fatalf("agent init pull policy = %q, want Never", init.ImagePullPolicy)
	}
	step := job.Spec.Template.Spec.Containers[0]
	if step.ImagePullPolicy != corev1.PullIfNotPresent {
		t.Fatalf("step pull policy = %q, want IfNotPresent", step.ImagePullPolicy)
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
		Options: manifest.SpecOptions{Env: []manifest.EnvVar{
			{Name: "MASTER_ADDR", Value: "trainer-0"},
			{Name: "WORLD_SIZE", Value: "4"},
		}},
		Driver: manifest.DriverSpec{
			Resources: manifest.ResourceSpec{CPU: "2", Memory: "4Gi", GPU: "1"},
			K8s: &manifest.DriverK8sSpec{
				PodTemplate: func() corev1.PodTemplateSpec {
					var tpl corev1.PodTemplateSpec
					tpl.Spec.NodeSelector = map[string]string{"accelerator": "nvidia-a100"}
					tpl.Spec.Tolerations = []corev1.Toleration{{
						Key:      "nvidia.com/gpu",
						Operator: "Exists",
						Effect:   "NoSchedule",
					}}
					return tpl
				}(),
			},
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
	if len(jobs.Items) != 1 || jobs.Items[0].Labels["piper.io/run-id"] != "run-2" {
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
	l.ReconcileJobs(context.Background(), func(_ context.Context, result proto.TaskResult) error {
		gotTaskID = result.TaskID
		gotStatus = result.Status
		gotErr = result.Error
		return nil
	})
	if gotTaskID != task.ID || gotStatus != proto.TaskStatusFailed || gotErr != "job failed" {
		t.Fatalf("report = (%q, %q, %q), want (%q, %q, %q)", gotTaskID, gotStatus, gotErr, task.ID, proto.TaskStatusFailed, "job failed")
	}
	if len(l.watched) != 0 {
		t.Fatalf("failed job remained watched: %#v", l.watched)
	}
}

func TestRecoverJobsRestoresActiveTaskIDs(t *testing.T) {
	client := fake.NewSimpleClientset(
		&batchv1.Job{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "job-1",
				Namespace: "jobs",
				Labels: map[string]string{
					"app.kubernetes.io/managed-by": "piper",
					"piper.io/worker-id":           "Worker-1",
				},
				Annotations: map[string]string{
					"piper.io/task-id": "run-1:step-1",
					"piper.io/attempt": "3",
				},
			},
		},
		&batchv1.Job{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "other-worker-job",
				Namespace: "jobs",
				Labels: map[string]string{
					"app.kubernetes.io/managed-by": "piper",
					"piper.io/worker-id":           "worker-2",
				},
				Annotations: map[string]string{
					"piper.io/task-id": "run-2:step-1",
				},
			},
		},
	)
	launcher := NewWithClient(Config{Namespace: "jobs", WorkerID: "Worker 1"}, client)

	launcher.RecoverJobs(context.Background())
	taskIDs := launcher.ActiveTaskIDs()
	if len(taskIDs) != 1 || taskIDs[0] != "run-1:step-1" {
		t.Fatalf("active task IDs = %v", taskIDs)
	}
	launcher.mu.Lock()
	rec := launcher.watched["job-1"]
	launcher.mu.Unlock()
	if rec.WorkerID != "Worker 1" {
		t.Fatalf("worker ID = %q, want Worker 1", rec.WorkerID)
	}
	if rec.Attempt != 3 {
		t.Fatalf("attempt = %d, want 3", rec.Attempt)
	}
}

func TestCreateJobUsesPreparedExecutionContract(t *testing.T) {
	step := pipeline.Step{
		Name:   "train",
		Run:    pipeline.Run{Command: []string{"sh", "-c", "echo train"}},
		Driver: manifest.DriverSpec{Image: "python:3.11"},
	}
	pl := pipeline.Pipeline{Metadata: manifest.ObjectMeta{Name: "pipe"}}
	task := makeTask("run-1", "train", step, pl)
	task.WorkDir = "/work"
	task.OutputDir = "/out"
	task.RunParams = map[string]any{"lr": "0.2"}
	task.Attempt = 2

	l := &Launcher{
		cfg: Config{
			AgentImage: "loykin/piper:agent",
			Namespace:  "default",
		},
		clientset: fake.NewSimpleClientset(),
	}
	preparedArgs := []string{
		"agent",
		"exec",
		"--task=encoded",
		"--master=http://master:8080",
		"--report-mode=file",
		"--result-file=/dev/termination-log",
	}
	if _, err := l.CreateJob(context.Background(), task, "", "python:3.11", preparedArgs, nil); err != nil {
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
	if init.ImagePullPolicy != "Always" {
		t.Fatalf("init pull policy = %q, want Always", init.ImagePullPolicy)
	}
	stepContainer := job.Spec.Template.Spec.Containers[0]
	if stepContainer.Image != "python:3.11" {
		t.Fatalf("step image = %q, want python:3.11", stepContainer.Image)
	}
	if !hasArgPrefix(stepContainer.Args, "--task=") {
		t.Fatalf("agent args missing --task: %v", stepContainer.Args)
	}
	if !hasArg(stepContainer.Args, "--master=http://master:8080") {
		t.Fatalf("agent args missing master URL: %v", stepContainer.Args)
	}
	if hasArg(stepContainer.Args, "--") || hasArg(stepContainer.Args, "echo") || hasArg(stepContainer.Args, "train") {
		t.Fatalf("launcher must not append command override args: %v", stepContainer.Args)
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
