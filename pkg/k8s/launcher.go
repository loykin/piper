// Package k8s provides prepared Kubernetes Job lifecycle primitives.
//
// Execution policy such as image resolution and agent argument construction
// belongs to pkg/taskruntime/k8s.Driver. Launcher only creates, observes,
// recovers, and deletes Jobs from already prepared execution inputs.
package k8s

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"

	"github.com/piper/piper/pkg/internal/k8smeta"
	"github.com/piper/piper/pkg/pipeline"
	"github.com/piper/piper/pkg/proto"
	"github.com/piper/piper/pkg/taskruntime"
)

// Config is the Kubernetes Job primitive configuration.
type Config struct {
	// AgentImage: image containing the piper CLI binary (used as initContainer)
	AgentImage string

	// Namespace: K8s namespace in which to create Jobs
	Namespace string

	// InCluster: if true, use in-cluster config
	InCluster bool

	// Kubeconfig: path to kubeconfig file for out-of-cluster execution (defaults to KUBECONFIG or ~/.kube/config)
	Kubeconfig string

	// AgentImagePullPolicy: image pull policy for the agent initContainer.
	// Defaults to corev1.PullAlways when empty.
	AgentImagePullPolicy string

	// TTLAfterFinished: seconds after which a finished Job is automatically deleted. nil means no auto-deletion.
	TTLAfterFinished *int32

	// WorkerID is the stable identity of this K8s worker.
	// Jobs are labeled piper.io/worker-id=WorkerID so RecoverJobs only observes
	// workloads owned by this worker in a shared namespace.
	WorkerID string
}

const (
	// agentBinarySrc is the path of the piper CLI binary inside the agent image.
	agentBinarySrc = "/piper"
	// agentBinaryDst is where the binary is copied to in the shared emptyDir volume.
	agentBinaryDst = "/piper-tools/piper"
	// agentSubcmd and agentExecSubcmd are the piper CLI subcommands for step execution.
	agentSubcmd     = "agent"
	agentExecSubcmd = "exec"
)

// Launcher owns Kubernetes API operations for prepared Jobs.
type Launcher struct {
	cfg       Config
	clientset kubernetes.Interface
	mu        sync.Mutex
	watched   map[string]watchedJob
}

type watchedJob struct {
	TaskID    string
	WorkerID  string
	RunID     string
	StepName  string
	Attempt   int
	StartedAt time.Time
}

// JobHandle is the runtime metadata needed to recover a K8s Job.
type JobHandle struct {
	RuntimeKey string
	TaskID     string
	RunID      string
	StepName   string
	Attempt    int
}

// New creates a Launcher.
// If InCluster is true, it uses in-cluster config;
// otherwise it uses the kubeconfig at Kubeconfig path (or the default location).
func New(cfg Config) (*Launcher, error) {
	cfg = normalizeConfig(cfg)

	var restCfg *rest.Config
	var err error
	if cfg.InCluster {
		restCfg, err = rest.InClusterConfig()
	} else {
		kubeconfig := cfg.Kubeconfig
		if kubeconfig == "" {
			kubeconfig = os.Getenv("KUBECONFIG")
		}
		if kubeconfig == "" {
			home, _ := os.UserHomeDir()
			kubeconfig = home + "/.kube/config"
		}
		restCfg, err = clientcmd.BuildConfigFromFlags("", kubeconfig)
	}
	if err != nil {
		return nil, fmt.Errorf("k8s config: %w", err)
	}

	clientset, err := kubernetes.NewForConfig(restCfg)
	if err != nil {
		return nil, fmt.Errorf("k8s clientset: %w", err)
	}

	return NewWithClient(cfg, clientset), nil
}

func NewWithClient(cfg Config, clientset kubernetes.Interface) *Launcher {
	cfg = normalizeConfig(cfg)
	return &Launcher{cfg: cfg, clientset: clientset, watched: make(map[string]watchedJob)}
}

func normalizeConfig(cfg Config) Config {
	if cfg.Namespace == "" {
		cfg.Namespace = "default"
	}
	if cfg.AgentImage == "" {
		cfg.AgentImage = "piper/piper:latest"
	}
	return cfg
}

// pullPolicy returns the configured image pull policy, defaulting to PullAlways.
func (l *Launcher) pullPolicy() corev1.PullPolicy {
	if l.cfg.AgentImagePullPolicy != "" {
		return corev1.PullPolicy(l.cfg.AgentImagePullPolicy)
	}
	return corev1.PullAlways
}

// CreateJob creates a Job from execution inputs already resolved by a runtime Driver.
func (l *Launcher) CreateJob(ctx context.Context, task *proto.Task, runtimeKey, image string, agentArgs, env []string) (string, error) {
	if task == nil {
		return "", fmt.Errorf("task is required")
	}
	if image == "" {
		return "", fmt.Errorf("container image is required")
	}
	job := l.buildJob(task, image, agentArgs, env)
	if runtimeKey != "" {
		job.Name = sanitizeName(runtimeKey)
	}

	_, err := l.clientset.BatchV1().Jobs(l.cfg.Namespace).Create(ctx, job, metav1.CreateOptions{})
	if err != nil {
		return "", fmt.Errorf("create job %s: %w", job.Name, err)
	}
	l.watchJob(job.Name, task)
	return job.Name, nil
}

// DeleteJob deletes one Job and removes it from the launcher's watch set.
func (l *Launcher) DeleteJob(ctx context.Context, name string) error {
	if name == "" {
		return fmt.Errorf("job name is required")
	}
	err := l.clientset.BatchV1().Jobs(l.cfg.Namespace).Delete(ctx, name, metav1.DeleteOptions{})
	if err != nil {
		return err
	}
	l.unwatchJob(name)
	return nil
}

// ReconcileJobs polls dispatched K8s Jobs and reports failed or disappeared Jobs
// back to the queue. Successful Jobs are expected to report completion through
// the piper agent before Kubernetes TTL cleanup removes them.
func (l *Launcher) ReconcileJobs(ctx context.Context, report func(context.Context, proto.TaskResult) error) {
	if report == nil {
		return
	}
	l.mu.Lock()
	if len(l.watched) == 0 {
		l.mu.Unlock()
		return
	}
	watched := make(map[string]watchedJob, len(l.watched))
	for name, rec := range l.watched {
		watched[name] = rec
	}
	l.mu.Unlock()

	jobs, err := l.clientset.BatchV1().Jobs(l.cfg.Namespace).List(ctx, metav1.ListOptions{
		LabelSelector: k8smeta.ManagedSelector(),
	})
	if err != nil {
		return
	}
	present := make(map[string]batchv1.Job, len(jobs.Items))
	for _, job := range jobs.Items {
		present[job.Name] = job
	}

	for name, rec := range watched {
		job, ok := present[name]
		switch {
		case !ok:
			// Job disappeared before TTL cleanup — infra failure, no termination log.
			l.reportWatchedJob(ctx, report, name, rec, proto.TaskStatusFailed, "k8s job disappeared before reporting completion")
		case jobSucceeded(job) || jobFailed(job):
			// Try to read the AgentResult from the pod termination log first.
			if result, ok := l.readTerminationResult(ctx, job, rec); ok {
				result.WorkerID = rec.WorkerID
				if err := report(ctx, result); err == nil || strings.Contains(err.Error(), "not found in queue") {
					l.unwatchJob(name)
				}
			} else if jobFailed(job) {
				l.reportWatchedJob(ctx, report, name, rec, proto.TaskStatusFailed, k8sJobFailureMessage(job))
			} else {
				// Job succeeded but no valid termination log — infra issue.
				l.reportWatchedJob(ctx, report, name, rec, proto.TaskStatusFailed, "termination_result_invalid: missing or unreadable result")
			}
		}
	}
}

// readTerminationResult reads the AgentResult JSON from the pod termination message.
// Returns (result, true) on success, (zero, false) if the message is missing or invalid.
func (l *Launcher) readTerminationResult(ctx context.Context, job batchv1.Job, rec watchedJob) (proto.TaskResult, bool) {
	pods, err := l.clientset.CoreV1().Pods(l.cfg.Namespace).List(ctx, metav1.ListOptions{
		LabelSelector: "job-name=" + job.Name,
	})
	if err != nil || len(pods.Items) == 0 {
		return proto.TaskResult{}, false
	}
	// Use the most recently created pod.
	pod := pods.Items[0]
	for _, p := range pods.Items[1:] {
		if p.CreationTimestamp.After(pod.CreationTimestamp.Time) {
			pod = p
		}
	}
	for _, cs := range pod.Status.ContainerStatuses {
		if cs.State.Terminated == nil || cs.State.Terminated.Message == "" {
			continue
		}
		result, err := taskruntime.ReadAgentResult([]byte(cs.State.Terminated.Message))
		if err != nil {
			slog.Warn("k8s: termination message parse failed", "job", job.Name, "err", err)
			return proto.TaskResult{}, false
		}
		if result.TaskID == "" {
			result.TaskID = rec.TaskID
		}
		if result.Attempt == 0 {
			result.Attempt = rec.Attempt
		}
		return result, true
	}
	return proto.TaskResult{}, false
}

func (l *Launcher) watchJob(name string, task *proto.Task) {
	attempt := task.Attempt
	if attempt < 1 {
		attempt = 1
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.watched == nil {
		l.watched = make(map[string]watchedJob)
	}
	l.watched[name] = watchedJob{
		TaskID:    task.ID,
		WorkerID:  task.WorkerID,
		RunID:     task.RunID,
		StepName:  task.StepName,
		Attempt:   attempt,
		StartedAt: time.Now().UTC(),
	}
}

func (l *Launcher) unwatchJob(name string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.watched, name)
}

func (l *Launcher) ActiveTaskIDs() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	taskIDs := make([]string, 0, len(l.watched))
	for _, rec := range l.watched {
		taskIDs = append(taskIDs, rec.TaskID)
	}
	return taskIDs
}

// ActiveJobs returns recoverable metadata for every watched Job.
func (l *Launcher) ActiveJobs() []JobHandle {
	l.mu.Lock()
	defer l.mu.Unlock()
	handles := make([]JobHandle, 0, len(l.watched))
	for runtimeKey, rec := range l.watched {
		handles = append(handles, JobHandle{
			RuntimeKey: runtimeKey,
			TaskID:     rec.TaskID,
			RunID:      rec.RunID,
			StepName:   rec.StepName,
			Attempt:    rec.Attempt,
		})
	}
	return handles
}

// RecoverJobs rebuilds the in-memory watch set after a worker restart.
func (l *Launcher) RecoverJobs(ctx context.Context) {
	selector := k8smeta.ManagedSelector()
	if l.cfg.WorkerID != "" {
		selector = k8smeta.WorkerSelector(l.cfg.WorkerID)
	}
	jobs, err := l.clientset.BatchV1().Jobs(l.cfg.Namespace).List(ctx, metav1.ListOptions{
		LabelSelector: selector,
	})
	if err != nil {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	for i := range jobs.Items {
		job := &jobs.Items[i]
		taskID := job.Annotations[k8smeta.AnnotationTaskID]
		if taskID == "" {
			continue
		}
		if _, exists := l.watched[job.Name]; exists {
			continue
		}
		startedAt := job.CreationTimestamp.Time
		if startedAt.IsZero() {
			startedAt = time.Now().UTC()
		}
		attempt, _ := strconv.Atoi(job.Annotations[k8smeta.AnnotationAttempt])
		if attempt < 1 {
			attempt = 1
		}
		runID := job.Annotations[k8smeta.AnnotationRunID]
		if runID == "" {
			runID = job.Labels[k8smeta.LabelRunID]
		}
		stepName := job.Annotations[k8smeta.AnnotationStepName]
		if stepName == "" {
			stepName = job.Labels[k8smeta.LabelStepName]
		}
		l.watched[job.Name] = watchedJob{
			TaskID:    taskID,
			WorkerID:  l.cfg.WorkerID,
			RunID:     runID,
			StepName:  stepName,
			Attempt:   attempt,
			StartedAt: startedAt,
		}
	}
}

func (l *Launcher) reportWatchedJob(ctx context.Context, report func(context.Context, proto.TaskResult) error, name string, rec watchedJob, status, msg string) {
	now := time.Now().UTC()
	if err := report(ctx, proto.TaskResult{
		TaskID:    rec.TaskID,
		WorkerID:  rec.WorkerID,
		Status:    status,
		Error:     msg,
		StartedAt: rec.StartedAt,
		EndedAt:   now,
		Attempt:   rec.Attempt,
	}); err == nil || strings.Contains(err.Error(), "not found in queue") {
		l.unwatchJob(name)
	}
}

func jobSucceeded(job batchv1.Job) bool {
	if job.Status.Succeeded > 0 {
		return true
	}
	for _, cond := range job.Status.Conditions {
		if cond.Type == batchv1.JobComplete && cond.Status == corev1.ConditionTrue {
			return true
		}
	}
	return false
}

func jobFailed(job batchv1.Job) bool {
	if job.Status.Failed > 0 {
		return true
	}
	for _, cond := range job.Status.Conditions {
		if cond.Type == batchv1.JobFailed && cond.Status == corev1.ConditionTrue {
			return true
		}
	}
	return false
}

func k8sJobFailureMessage(job batchv1.Job) string {
	for _, cond := range job.Status.Conditions {
		if cond.Type == batchv1.JobFailed && cond.Message != "" {
			return cond.Message
		}
		if cond.Type == batchv1.JobFailed && cond.Reason != "" {
			return cond.Reason
		}
	}
	return "k8s job failed"
}

// CancelRun deletes all piper Jobs associated with a run.
func (l *Launcher) CancelRun(ctx context.Context, runID string) error {
	selector := k8smeta.LabelRunID + "=" + k8smeta.LabelValue(runID)
	jobs, err := l.clientset.BatchV1().Jobs(l.cfg.Namespace).List(ctx, metav1.ListOptions{
		LabelSelector: selector,
	})
	if err != nil {
		return err
	}
	for _, job := range jobs.Items {
		if err := l.clientset.BatchV1().Jobs(l.cfg.Namespace).Delete(ctx, job.Name, metav1.DeleteOptions{}); err != nil {
			return err
		}
	}
	return nil
}

func (l *Launcher) jobLabels(task *proto.Task) map[string]string {
	labels := map[string]string{
		k8smeta.LabelManagedBy: k8smeta.ManagedByPiper,
		k8smeta.LabelRunID:     k8smeta.LabelValue(task.RunID),
		k8smeta.LabelStepName:  k8smeta.LabelValue(task.StepName),
	}
	if l.cfg.WorkerID != "" {
		labels[k8smeta.LabelWorkerID] = k8smeta.LabelValue(l.cfg.WorkerID)
	}
	return labels
}

func (l *Launcher) buildJob(task *proto.Task, image string, agentArgs []string, extraEnv ...[]string) *batchv1.Job {
	backoffLimit := int32(0) // piper queue manages retries
	var ttl *int32
	if l.cfg.TTLAfterFinished != nil && *l.cfg.TTLAfterFinished > 0 {
		ttl = l.cfg.TTLAfterFinished
	}
	step := decodeTaskStep(task)

	return &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			Name:      jobName(task),
			Namespace: l.cfg.Namespace,
			Annotations: map[string]string{
				k8smeta.AnnotationTaskID:   task.ID,
				k8smeta.AnnotationAttempt:  strconv.Itoa(max(task.Attempt, 1)),
				k8smeta.AnnotationRunID:    task.RunID,
				k8smeta.AnnotationStepName: task.StepName,
			},
			Labels: l.jobLabels(task),
		},
		Spec: batchv1.JobSpec{
			TTLSecondsAfterFinished: ttl,
			BackoffLimit:            &backoffLimit,
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels:      step.Runner.PodLabels,
					Annotations: step.Runner.PodAnnotations,
				},
				Spec: corev1.PodSpec{
					RestartPolicy: corev1.RestartPolicyNever,
					SchedulerName: step.Runner.SchedulerName,
					NodeSelector:  step.Runner.NodeSelector,
					Tolerations:   buildTolerations(step.Runner.Tolerations),
					// initContainer: copy the piper CLI binary into the emptyDir
					InitContainers: []corev1.Container{
						{
							Name:            "agent-init",
							Image:           l.cfg.AgentImage,
							ImagePullPolicy: l.pullPolicy(),
							Command:         []string{"cp", agentBinarySrc, agentBinaryDst},
							VolumeMounts: []corev1.VolumeMount{
								{Name: "piper-tools", MountPath: "/piper-tools"},
							},
						},
					},
					// step container: run piper agent as the entrypoint using the original image
					Containers: []corev1.Container{
						{
							Name:            "step",
							Image:           image,
							ImagePullPolicy: corev1.PullIfNotPresent,
							Command:         []string{agentBinaryDst},
							Args:            agentArgs,
							Env:             buildEnvVars(step.Env, extraEnv...),
							Resources:       buildResourceRequirements(step.Resources),
							VolumeMounts: []corev1.VolumeMount{
								{Name: "piper-tools", MountPath: "/piper-tools"},
								{Name: "piper-outputs", MountPath: "/piper-outputs"},
								{Name: "piper-inputs", MountPath: "/piper-inputs"},
							},
						},
					},
					Volumes: []corev1.Volume{
						{
							Name: "piper-tools",
							VolumeSource: corev1.VolumeSource{
								EmptyDir: &corev1.EmptyDirVolumeSource{},
							},
						},
						{
							Name: "piper-outputs",
							VolumeSource: corev1.VolumeSource{
								EmptyDir: &corev1.EmptyDirVolumeSource{},
							},
						},
						{
							Name: "piper-inputs",
							VolumeSource: corev1.VolumeSource{
								EmptyDir: &corev1.EmptyDirVolumeSource{},
							},
						},
					},
				},
			},
		},
	}
}

func decodeTaskStep(task *proto.Task) pipeline.Step {
	var step pipeline.Step
	if len(task.Step) == 0 {
		return step
	}
	_ = json.Unmarshal(task.Step, &step)
	return step
}

func buildEnvVars(env map[string]string, extra ...[]string) []corev1.EnvVar {
	merged := make(map[string]string, len(env))
	for key, value := range env {
		merged[key] = value
	}
	for _, values := range extra {
		for _, entry := range values {
			key, value, ok := strings.Cut(entry, "=")
			if ok && key != "" {
				merged[key] = value
			}
		}
	}
	if len(merged) == 0 {
		return nil
	}
	keys := make([]string, 0, len(merged))
	for k := range merged {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make([]corev1.EnvVar, 0, len(keys))
	for _, k := range keys {
		out = append(out, corev1.EnvVar{Name: k, Value: merged[k]})
	}
	return out
}

func buildResourceRequirements(resources pipeline.Resources) corev1.ResourceRequirements {
	reqs := corev1.ResourceList{}
	limits := corev1.ResourceList{}
	if resources.CPU != "" {
		qty := resource.MustParse(resources.CPU)
		reqs[corev1.ResourceCPU] = qty
		limits[corev1.ResourceCPU] = qty
	}
	if resources.Memory != "" {
		qty := resource.MustParse(resources.Memory)
		reqs[corev1.ResourceMemory] = qty
		limits[corev1.ResourceMemory] = qty
	}
	if resources.GPU != "" {
		qty := resource.MustParse(resources.GPU)
		name := corev1.ResourceName("nvidia.com/gpu")
		reqs[name] = qty
		limits[name] = qty
	}
	return corev1.ResourceRequirements{Requests: reqs, Limits: limits}
}

func buildTolerations(tolerations []pipeline.Toleration) []corev1.Toleration {
	if len(tolerations) == 0 {
		return nil
	}
	out := make([]corev1.Toleration, 0, len(tolerations))
	for _, tol := range tolerations {
		out = append(out, corev1.Toleration{
			Key:               tol.Key,
			Operator:          corev1.TolerationOperator(tol.Operator),
			Value:             tol.Value,
			Effect:            corev1.TaintEffect(tol.Effect),
			TolerationSeconds: tol.TolerationSeconds,
		})
	}
	return out
}

// jobName generates the K8s Job name.
// Format: piper-{runID}-{stepName}[-a{attempt}], truncated to 63 characters.
func jobName(task *proto.Task) string {
	raw := "piper-" + task.RunID + "-" + task.StepName
	if task.Attempt > 1 {
		raw = fmt.Sprintf("%s-a%d", raw, task.Attempt)
	}
	return sanitizeName(raw)
}

// sanitizeName normalizes a string to comply with K8s resource name rules.
// Allows [a-z0-9-], max 63 characters, must start and end with an alphanumeric character.
func sanitizeName(s string) string {
	var b strings.Builder
	for _, c := range strings.ToLower(s) {
		switch {
		case c >= 'a' && c <= 'z', c >= '0' && c <= '9', c == '-':
			b.WriteRune(c)
		default:
			b.WriteRune('-')
		}
	}
	name := strings.Trim(b.String(), "-")
	if len(name) > 63 {
		name = strings.TrimRight(name[:63], "-")
	}
	return name
}
