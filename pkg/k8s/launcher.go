// Package k8s provides a Dispatcher implementation that runs piper tasks as K8s Jobs.
//
// How it works (agent injection pattern):
//  1. An initContainer copies the /piper-agent binary from the piper/agent image into an emptyDir
//  2. The step container's entrypoint is replaced with /piper-tools/piper-agent exec ... -- <original command>
//  3. piper-agent downloads input artifacts from S3, runs the command, uploads outputs to S3, and reports completion to the master
//
// Runs natively on K8s without modifying user images.
package k8s

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"

	"github.com/piper/piper/pkg/pipeline"
	"github.com/piper/piper/pkg/proto"
)

// Config is the Launcher configuration.
// Maps 1:1 to piper.K8sConfig.
type Config struct {
	// AgentImage: image containing the piper-agent binary (used as initContainer)
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
	// agentBinarySrc is the path of the piper-agent binary inside the agent image.
	agentBinarySrc = "/piper-agent"
	// agentBinaryDst is where the binary is copied to in the shared emptyDir volume.
	agentBinaryDst = "/piper-tools/piper"
	// agentExecSubcmd is the subcommand the agent binary exposes for step execution.
	agentExecSubcmd = "exec"
)

// Launcher implements proto.Dispatcher.
// The queue calls Dispatch whenever a task becomes ready.
type Launcher struct {
	cfg       Config
	clientset *kubernetes.Clientset
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

	return &Launcher{cfg: cfg, clientset: clientset}, nil
}

// Dispatch creates a K8s Job for the given task.
// It does not wait for the Job to complete — the piper-agent inside the Job reports results to the master.
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

	agentArgs := l.buildAgentArgs(task, stepB64)
	if len(step.Run.Command) > 0 {
		agentArgs = append(agentArgs, "--")
		agentArgs = append(agentArgs, step.Run.Command...)
	}

	job := l.buildJob(task, image, agentArgs)

	_, err = l.clientset.BatchV1().Jobs(l.cfg.Namespace).Create(ctx, job, metav1.CreateOptions{})
	if err != nil {
		return fmt.Errorf("create job %s: %w", job.Name, err)
	}
	return nil
}

func (l *Launcher) buildAgentArgs(task *proto.Task, stepB64 string) []string {
	args := []string{
		agentExecSubcmd,
		"--master=" + l.cfg.MasterURL,
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

	return &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			Name:      jobName(task),
			Namespace: l.cfg.Namespace,
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
					// initContainer: copy the piper-agent binary into the emptyDir
					InitContainers: []corev1.Container{
						{
							Name:            "agent-init",
							Image:           l.cfg.AgentImage,
							ImagePullPolicy: corev1.PullIfNotPresent,
							// Copy the agent binary from the agent image into the shared volume
							Command: []string{"cp", agentBinarySrc, agentBinaryDst},
							VolumeMounts: []corev1.VolumeMount{
								{Name: "piper-tools", MountPath: "/piper-tools"},
							},
						},
					},
					// step container: run piper-agent as the entrypoint using the original image
					Containers: []corev1.Container{
						{
							Name:            "step",
							Image:           image,
							ImagePullPolicy: corev1.PullIfNotPresent,
							Command:         []string{agentBinaryDst},
							Args:            agentArgs,
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

// jobName generates the K8s Job name.
// Format: piper-{runID}-{stepName}, truncated to 63 characters.
func jobName(task *proto.Task) string {
	raw := "piper-" + task.RunID + "-" + task.StepName
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
