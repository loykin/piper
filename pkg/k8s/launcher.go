// Package k8s provides an ExecutionBackend implementation that runs piper tasks as K8s Jobs.
//
// How it works (agent injection pattern):
//  1. An initContainer copies the /piper binary from the piper image into an emptyDir
//  2. The step container's entrypoint is replaced with /piper-tools/piper agent exec ... -- <original command>
//  3. piper agent downloads input artifacts from S3, runs the command, uploads outputs to S3, and reports completion to the master
//
// Runs natively on K8s without modifying user images.
package k8s

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"sort"
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

	"github.com/piper/piper/pkg/pipeline"
	"github.com/piper/piper/pkg/proto"
	"github.com/piper/piper/pkg/runner"
)

// Config is the Launcher configuration.
// Maps 1:1 to piper.K8sConfig.
type Config struct {
	// AgentImage: image containing the piper CLI binary (used as initContainer)
	AgentImage string

	// Namespace: K8s namespace in which to create Jobs
	Namespace string

	// InCluster: if true, use in-cluster config
	InCluster bool

	// Kubeconfig: path to kubeconfig file for out-of-cluster execution (defaults to KUBECONFIG or ~/.kube/config)
	Kubeconfig string

	// MasterURL: piper server URL accessible from within a Pod
	MasterURL string

	// Token: auth token for the piper server
	Token string

	// S3 artifact sharing configuration
	S3Endpoint  string
	S3AccessKey string
	S3SecretKey string
	S3Bucket    string
	S3UseSSL    bool

	// DefaultImage: fallback container image when a step has no image configured
	DefaultImage string

	// TTLAfterFinished: seconds after which a finished Job is automatically deleted. nil means no auto-deletion.
	TTLAfterFinished *int32
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

// Launcher implements backend.ExecutionBackend.
// The queue calls Dispatch whenever a task becomes ready.
type Launcher struct {
	cfg       Config
	clientset kubernetes.Interface
	mu        sync.Mutex
	watched   map[string]watchedJob
}

type watchedJob struct {
	TaskID    string
	Attempt   int
	StartedAt time.Time
}

// New creates a Launcher.
// If InCluster is true, it uses in-cluster config;
// otherwise it uses the kubeconfig at Kubeconfig path (or the default location).
func New(cfg Config) (*Launcher, error) {
	if cfg.Namespace == "" {
		cfg.Namespace = "default"
	}
	if cfg.AgentImage == "" {
		cfg.AgentImage = "piper/piper:latest"
	}

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

	return &Launcher{cfg: cfg, clientset: clientset, watched: make(map[string]watchedJob)}, nil
}

// Dispatch creates a K8s Job for the given task.
// It does not wait for the Job to complete — the piper agent command inside the Job reports results to the master.
func (l *Launcher) Dispatch(ctx context.Context, task *proto.Task) error {
	var step pipeline.Step
	if err := json.Unmarshal(task.Step, &step); err != nil {
		return fmt.Errorf("unmarshal step: %w", err)
	}

	var pl pipeline.Pipeline
	if err := json.Unmarshal(task.Pipeline, &pl); err != nil {
		return fmt.Errorf("unmarshal pipeline: %w", err)
	}

	// Resolve container image: step > pipeline defaults > launcher default
	image := step.Run.Image
	if image == "" {
		image = pl.Spec.Defaults.Image
	}
	if image == "" {
		image = l.cfg.DefaultImage
	}
	if image == "" {
		return fmt.Errorf("step %q: no container image configured (set step.run.image, spec.defaults.image, or k8s.default_image)", step.Name)
	}

	// Base64-encode the step JSON and pass it to the agent
	stepJSON, err := json.Marshal(step)
	if err != nil {
		return err
	}
	stepB64 := base64.StdEncoding.EncodeToString(stepJSON)

	taskB64, err := runner.EncodeTask(task)
	if err != nil {
		return err
	}
	agentArgs := l.buildAgentArgs(task, taskB64, stepB64)
	if len(step.Run.Command) > 0 {
		agentArgs = append(agentArgs, "--")
		agentArgs = append(agentArgs, step.Run.Command...)
	}

	job := l.buildJob(task, image, agentArgs)

	_, err = l.clientset.BatchV1().Jobs(l.cfg.Namespace).Create(ctx, job, metav1.CreateOptions{})
	if err != nil {
		return fmt.Errorf("create job %s: %w", job.Name, err)
	}
	l.watchJob(job.Name, task)
	return nil
}

// ReconcileJobs polls dispatched K8s Jobs and reports failed or disappeared Jobs
// back to the queue. Successful Jobs are expected to report completion through
// the piper agent before Kubernetes TTL cleanup removes them.
func (l *Launcher) ReconcileJobs(ctx context.Context, report func(context.Context, string, string, string, time.Time, time.Time, int) error) {
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
		LabelSelector: "app.kubernetes.io/managed-by=piper",
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
			l.reportWatchedJob(ctx, report, name, rec, proto.TaskStatusFailed, "k8s job disappeared before reporting completion")
		case jobSucceeded(job):
			l.reportWatchedJob(ctx, report, name, rec, proto.TaskStatusDone, "")
		case jobFailed(job):
			l.reportWatchedJob(ctx, report, name, rec, proto.TaskStatusFailed, k8sJobFailureMessage(job))
		}
	}
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
		Attempt:   attempt,
		StartedAt: time.Now().UTC(),
	}
}

func (l *Launcher) unwatchJob(name string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.watched, name)
}

func (l *Launcher) reportWatchedJob(ctx context.Context, report func(context.Context, string, string, string, time.Time, time.Time, int) error, name string, rec watchedJob, status, msg string) {
	now := time.Now().UTC()
	if err := report(ctx, rec.TaskID, status, msg, rec.StartedAt, now, rec.Attempt); err == nil || strings.Contains(err.Error(), "not found in queue") {
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
	selector := "piper/run-id=" + sanitizeLabel(runID)
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

func (l *Launcher) buildAgentArgs(task *proto.Task, taskB64, stepB64 string) []string {
	args := []string{
		agentSubcmd,
		agentExecSubcmd,
		"--master=" + l.cfg.MasterURL,
		"--task=" + taskB64,
		"--task-id=" + task.ID,
		"--run-id=" + task.RunID,
		"--step-name=" + task.StepName,
		"--step=" + stepB64,
		"--output-dir=/piper-outputs",
		"--input-dir=/piper-inputs",
	}
	if l.cfg.Token != "" {
		args = append(args, "--token="+l.cfg.Token)
	}
	if l.cfg.S3Endpoint != "" {
		args = append(args,
			"--s3-endpoint="+l.cfg.S3Endpoint,
			"--s3-access-key="+l.cfg.S3AccessKey,
			"--s3-secret-key="+l.cfg.S3SecretKey,
			"--s3-bucket="+l.cfg.S3Bucket,
		)
		if l.cfg.S3UseSSL {
			args = append(args, "--s3-use-ssl")
		}
	}
	return args
}

func (l *Launcher) buildJob(task *proto.Task, image string, agentArgs []string) *batchv1.Job {
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
				"piper/task-id": task.ID,
			},
			Labels: map[string]string{
				"app.kubernetes.io/managed-by": "piper",
				"piper/run-id":                 sanitizeLabel(task.RunID),
				"piper/step-name":              sanitizeLabel(task.StepName),
			},
		},
		Spec: batchv1.JobSpec{
			TTLSecondsAfterFinished: ttl,
			BackoffLimit:            &backoffLimit,
			Template: corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{
					RestartPolicy: corev1.RestartPolicyNever,
					NodeSelector:  step.Runner.NodeSelector,
					Tolerations:   buildTolerations(step.Runner.Tolerations),
					// initContainer: copy the piper CLI binary into the emptyDir
					InitContainers: []corev1.Container{
						{
							Name:            "agent-init",
							Image:           l.cfg.AgentImage,
							ImagePullPolicy: corev1.PullAlways,
							// Copy the agent binary from the agent image into the shared volume
							Command: []string{"cp", agentBinarySrc, agentBinaryDst},
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
							Env:             buildEnvVars(step.Env),
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

func buildEnvVars(env map[string]string) []corev1.EnvVar {
	if len(env) == 0 {
		return nil
	}
	keys := make([]string, 0, len(env))
	for k := range env {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make([]corev1.EnvVar, 0, len(keys))
	for _, k := range keys {
		out = append(out, corev1.EnvVar{Name: k, Value: env[k]})
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

// sanitizeLabel normalizes a string to comply with K8s label value rules.
// Allows [a-zA-Z0-9._-], max 63 characters.
func sanitizeLabel(s string) string {
	var b strings.Builder
	for _, c := range s {
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z',
			c >= '0' && c <= '9', c == '-', c == '_', c == '.':
			b.WriteRune(c)
		default:
			b.WriteRune('-')
		}
	}
	v := b.String()
	if len(v) > 63 {
		v = v[:63]
	}
	return v
}
