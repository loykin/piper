package serving

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/client-go/kubernetes"
)

// Manager handles the lifecycle of ModelService deployments.
type Manager struct {
	repo     Repository
	modelDir string                // base directory for downloaded model artifacts
	k8s      *kubernetes.Clientset // nil when k8s mode is not configured
}

// SetK8sClientset injects a Kubernetes clientset at runtime.
func (m *Manager) SetK8sClientset(cs *kubernetes.Clientset) {
	m.k8s = cs
}

// New creates a Manager.
// modelDir is the root directory for local model artifact downloads.
// clientset may be nil if K8s mode is not used.
func New(repo Repository, modelDir string, clientset *kubernetes.Clientset) *Manager {
	return &Manager{repo: repo, modelDir: modelDir, k8s: clientset}
}

// Deploy starts a ModelService. Artifact download and process/pod creation happen here.
func (m *Manager) Deploy(ctx context.Context, svc ModelService, artifactDir string) error {
	name := svc.Metadata.Name
	mode := svc.Spec.Runtime.Mode
	if mode == "" {
		mode = "local"
	}

	rec := &Service{
		Name:     name,
		Artifact: svc.Spec.Model.FromArtifact.Step + "/" + svc.Spec.Model.FromArtifact.Artifact,
		Status:   StatusRunning,
		YAML:     "", // caller may set this
	}

	switch mode {
	case "local":
		if err := m.deployLocal(ctx, svc, artifactDir, rec); err != nil {
			return err
		}
	case "k8s":
		if m.k8s == nil {
			return fmt.Errorf("k8s mode requested but no k8s clientset configured")
		}
		s3URI := artifactDir // in k8s mode the caller passes an S3 URI
		if err := m.deployK8s(ctx, svc, s3URI, rec); err != nil {
			return err
		}
	default:
		return fmt.Errorf("unknown runtime mode: %q", mode)
	}

	return m.repo.Upsert(ctx, rec)
}

// Stop terminates a running service.
func (m *Manager) Stop(ctx context.Context, name string) error {
	svc, err := m.repo.Get(ctx, name)
	if err != nil {
		return fmt.Errorf("get service: %w", err)
	}
	if svc == nil {
		return fmt.Errorf("service %q not found", name)
	}

	if svc.PID > 0 {
		proc, err := os.FindProcess(svc.PID)
		if err == nil {
			_ = proc.Kill()
		}
	} else if m.k8s != nil {
		// K8s mode: delete Deployment and Service
		ns := m.k8sNamespace(ctx, name)
		_ = m.k8s.AppsV1().Deployments(ns).Delete(ctx, k8sName(name), metav1.DeleteOptions{})
		_ = m.k8s.CoreV1().Services(ns).Delete(ctx, k8sName(name), metav1.DeleteOptions{})
	}

	return m.repo.SetStatus(ctx, name, StatusStopped)
}

// Restart stops and re-deploys a service with the given artifact directory.
func (m *Manager) Restart(ctx context.Context, svc ModelService, artifactDir string) error {
	_ = m.Stop(ctx, svc.Metadata.Name)
	return m.Deploy(ctx, svc, artifactDir)
}

// SetYAML stores the original YAML on the service record.
func (m *Manager) SetYAML(ctx context.Context, name, yaml string) error {
	svc, err := m.repo.Get(ctx, name)
	if err != nil || svc == nil {
		return fmt.Errorf("service %q not found", name)
	}
	svc.YAML = yaml
	return m.repo.Update(ctx, svc)
}

// --- local mode ---

func (m *Manager) deployLocal(ctx context.Context, svc ModelService, modelDir string, rec *Service) error {
	rt := svc.Spec.Runtime
	if len(rt.Command) == 0 {
		return fmt.Errorf("runtime.command must not be empty")
	}
	if rt.Port == 0 {
		return fmt.Errorf("runtime.port must be set")
	}

	// Expand $(VAR) in each command argument
	envMap := map[string]string{
		"PIPER_MODEL_DIR":    modelDir,
		"PIPER_SERVICE_NAME": svc.Metadata.Name,
	}
	args := expandArgs(rt.Command, envMap)

	// Use context.Background so the process is not killed when the request context ends.
	cmd := exec.CommandContext(context.Background(), args[0], args[1:]...) //nolint:gosec
	cmd.Env = append(os.Environ(),
		"PIPER_MODEL_DIR="+modelDir,
		"PIPER_SERVICE_NAME="+svc.Metadata.Name,
	)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start runtime process: %w", err)
	}

	endpoint := fmt.Sprintf("http://localhost:%d", rt.Port)

	rec.PID = cmd.Process.Pid
	rec.Endpoint = endpoint
	rec.Status = StatusRunning

	go m.watchProcess(svc.Metadata.Name, cmd)

	// Wait briefly to verify the process started and is listening
	healthPath := rt.HealthPath
	if healthPath == "" {
		healthPath = "/"
	}
	if err := waitReady(ctx, endpoint+healthPath, 10*time.Second); err != nil {
		slog.Warn("service health check timed out; process may still be initialising",
			"name", svc.Metadata.Name, "endpoint", endpoint)
	}

	return nil
}

// watchProcess monitors a local process and updates the service status on exit.
func (m *Manager) watchProcess(name string, cmd *exec.Cmd) {
	ctx := context.Background()
	err := cmd.Wait()
	status := StatusStopped
	if err != nil {
		status = StatusFailed
		slog.Warn("serving process exited with error", "name", name, "err", err)
	} else {
		slog.Info("serving process exited", "name", name)
	}
	if dbErr := m.repo.SetStatus(ctx, name, status); dbErr != nil {
		slog.Warn("update service status failed", "name", name, "err", dbErr)
	}
}

// waitReady polls the given URL until it returns 2xx or the deadline expires.
func waitReady(ctx context.Context, url string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	client := &http.Client{Timeout: 2 * time.Second}
	for time.Now().Before(deadline) {
		resp, err := client.Get(url) //nolint:noctx
		if err == nil {
			_ = resp.Body.Close()
			if resp.StatusCode < 500 {
				return nil
			}
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(500 * time.Millisecond):
		}
	}
	return fmt.Errorf("timed out waiting for %s", url)
}

// expandArgs replaces $VAR, ${VAR}, and $(VAR) placeholders in each argument.
// envMap takes precedence; unknown keys fall back to os.Getenv.
func expandArgs(args []string, envMap map[string]string) []string {
	out := make([]string, len(args))
	for i, a := range args {
		out[i] = expandVars(a, envMap)
	}
	return out
}

// expandVars handles $VAR, ${VAR}, and $(VAR) substitution.
func expandVars(s string, envMap map[string]string) string {
	var b strings.Builder
	i := 0
	for i < len(s) {
		if s[i] != '$' {
			b.WriteByte(s[i])
			i++
			continue
		}
		i++ // consume '$'
		if i >= len(s) {
			b.WriteByte('$')
			break
		}
		var key string
		var end int
		switch s[i] {
		case '{':
			// ${VAR}
			j := strings.IndexByte(s[i+1:], '}')
			if j < 0 {
				b.WriteByte('$')
				continue
			}
			key = s[i+1 : i+1+j]
			end = i + 1 + j + 1
		case '(':
			// $(VAR)
			j := strings.IndexByte(s[i+1:], ')')
			if j < 0 {
				b.WriteByte('$')
				continue
			}
			key = s[i+1 : i+1+j]
			end = i + 1 + j + 1
		default:
			// $VAR — terminated by non-identifier character
			j := i
			for j < len(s) && isIdentChar(s[j]) {
				j++
			}
			key = s[i:j]
			end = j
		}
		if key == "" {
			b.WriteByte('$')
			continue
		}
		if v, ok := envMap[key]; ok {
			b.WriteString(v)
		} else {
			b.WriteString(os.Getenv(key))
		}
		i = end
	}
	return b.String()
}

func isIdentChar(c byte) bool {
	return c == '_' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9')
}

// --- k8s mode ---

func (m *Manager) deployK8s(ctx context.Context, svc ModelService, s3URI string, rec *Service) error {
	rt := svc.Spec.Runtime
	k := svc.Spec.K8s
	ns := k.Namespace
	if ns == "" {
		ns = "default"
	}
	replicas := int32(k.Replicas)
	if replicas == 0 {
		replicas = 1
	}

	envMap := map[string]string{
		"PIPER_MODEL_DIR":    s3URI,
		"PIPER_SERVICE_NAME": svc.Metadata.Name,
	}
	command := expandArgs(rt.Command, envMap)

	// Build resource requirements
	resReqs := corev1.ResourceRequirements{
		Requests: corev1.ResourceList{},
		Limits:   corev1.ResourceList{},
	}
	if cpu, ok := k.Resources["cpu"]; ok {
		q := resource.MustParse(cpu)
		resReqs.Requests[corev1.ResourceCPU] = q
		resReqs.Limits[corev1.ResourceCPU] = q
	}
	if mem, ok := k.Resources["memory"]; ok {
		q := resource.MustParse(mem)
		resReqs.Requests[corev1.ResourceMemory] = q
		resReqs.Limits[corev1.ResourceMemory] = q
	}
	if gpu, ok := k.Resources["gpu"]; ok {
		q := resource.MustParse(gpu)
		resReqs.Limits["nvidia.com/gpu"] = q
	}

	name := k8sName(svc.Metadata.Name)
	labels := map[string]string{
		"app.kubernetes.io/managed-by": "piper",
		"piper/service":                svc.Metadata.Name,
	}

	deployment := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns, Labels: labels},
		Spec: appsv1.DeploymentSpec{
			Replicas: &replicas,
			Selector: &metav1.LabelSelector{MatchLabels: labels},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: labels},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Name:            "serving",
							Image:           rt.Image,
							ImagePullPolicy: corev1.PullPolicy(k.ImagePullPolicy),
							Command:         []string{command[0]},
							Args:            command[1:],
							Resources:       resReqs,
							Env: []corev1.EnvVar{
								{Name: "PIPER_MODEL_DIR", Value: s3URI},
								{Name: "PIPER_SERVICE_NAME", Value: svc.Metadata.Name},
							},
							Ports: []corev1.ContainerPort{
								{ContainerPort: int32(rt.Port)},
							},
						},
					},
				},
			},
		},
	}

	k8sSvc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns, Labels: labels},
		Spec: corev1.ServiceSpec{
			Selector: labels,
			Ports: []corev1.ServicePort{
				{Port: int32(rt.Port), TargetPort: intstr.FromInt32(int32(rt.Port))},
			},
		},
	}

	if _, err := m.k8s.AppsV1().Deployments(ns).Create(ctx, deployment, metav1.CreateOptions{}); err != nil {
		return fmt.Errorf("create deployment: %w", err)
	}
	if _, err := m.k8s.CoreV1().Services(ns).Create(ctx, k8sSvc, metav1.CreateOptions{}); err != nil {
		return fmt.Errorf("create service: %w", err)
	}

	// Endpoint is the in-cluster DNS name
	rec.Endpoint = fmt.Sprintf("http://%s.%s.svc.cluster.local:%d", name, ns, rt.Port)
	rec.PID = 0
	rec.Status = StatusRunning
	return nil
}

// k8sName converts a service name to a K8s-safe resource name.
func k8sName(name string) string {
	safe := strings.ToLower(name)
	var b strings.Builder
	for _, c := range safe {
		switch {
		case c >= 'a' && c <= 'z', c >= '0' && c <= '9', c == '-':
			b.WriteRune(c)
		default:
			b.WriteRune('-')
		}
	}
	s := strings.Trim(b.String(), "-")
	if len(s) > 63 {
		s = strings.TrimRight(s[:63], "-")
	}
	return s
}

// k8sNamespace resolves the namespace for a service by looking it up in the repo.
// Falls back to "default" on any error.
func (m *Manager) k8sNamespace(ctx context.Context, name string) string {
	svc, err := m.repo.Get(ctx, name)
	if err != nil || svc == nil {
		return "default"
	}
	// Namespace is not persisted separately; derive from endpoint DNS if present.
	// e.g. "http://fraud-detector.ml-prod.svc.cluster.local:8000"
	// Extract the second label component.
	ep := svc.Endpoint
	ep = strings.TrimPrefix(ep, "http://")
	ep = strings.TrimPrefix(ep, "https://")
	parts := strings.SplitN(ep, ".", 3)
	if len(parts) >= 2 {
		return parts[1]
	}
	return "default"
}

// ArtifactLocalPath returns the local filesystem path where a run's step artifact
// should be (or will be) downloaded.
func ArtifactLocalPath(modelDir, runID, step, artifact string) string {
	return filepath.Join(modelDir, runID, step, artifact)
}
