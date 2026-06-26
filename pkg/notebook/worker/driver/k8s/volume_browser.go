package notebookworker

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/google/uuid"
	corev1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"

	k8smanifest "github.com/piper/piper/pkg/manifest/k8s"
	"github.com/piper/piper/pkg/notebook"
)

const (
	viewerIdleTTL                = 5 * time.Minute
	viewerReadyTimeout           = 30 * time.Second
	viewerAnnotationLastAccessed = "piper.io/last-accessed-at"
	viewerLabelKind              = "notebook-volume-browser"
)

type browserEndpoint struct {
	ServiceHost string
	Token       string
}

type volumeBrowserManager struct {
	client   kubernetes.Interface
	ns       string
	workerID string
	image    string
}

func newVolumeBrowserManager(client kubernetes.Interface, ns, workerID, image string) *volumeBrowserManager {
	return &volumeBrowserManager{client: client, ns: ns, workerID: workerID, image: image}
}

// Ensure creates or reuses the viewer Pod+Service for the given volumeID.
// Must be called while holding the volume lock.
func (m *volumeBrowserManager) Ensure(ctx context.Context, volumeID string) (*browserEndpoint, error) {
	podName := viewerPodName(volumeID)

	pod, err := m.client.CoreV1().Pods(m.ns).Get(ctx, podName, metav1.GetOptions{})
	if err != nil && !k8serrors.IsNotFound(err) {
		return nil, err
	}
	if err == nil {
		switch pod.Status.Phase {
		case corev1.PodRunning:
			if isPodReady(pod) {
				_ = m.touchAnnotation(ctx, pod)
				return m.endpointFromPod(pod), nil
			}
			return nil, &transitioningError{"viewer pod not ready"}
		case corev1.PodPending:
			return nil, &transitioningError{"viewer pod pending"}
		default:
			_ = m.deletePodAndService(ctx, volumeID)
		}
	}

	token := uuid.NewString()
	if err := m.createPodAndService(ctx, volumeID, token); err != nil {
		return nil, err
	}

	readyPod, waitErr := m.waitForReady(ctx, podName, viewerReadyTimeout)
	if waitErr != nil {
		return nil, &transitioningError{"waiting for viewer pod: " + waitErr.Error()}
	}
	return m.endpointFromPod(readyPod), nil
}

// Stop deletes the viewer Pod and Service for the given volumeID.
func (m *volumeBrowserManager) Stop(ctx context.Context, volumeID string) error {
	return m.deletePodAndService(ctx, volumeID)
}

// WaitForDeletion waits until the viewer Pod is fully gone or ctx expires.
func (m *volumeBrowserManager) WaitForDeletion(ctx context.Context, volumeID string) error {
	podName := viewerPodName(volumeID)
	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		_, err := m.client.CoreV1().Pods(m.ns).Get(ctx, podName, metav1.GetOptions{})
		if k8serrors.IsNotFound(err) {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(2 * time.Second):
		}
	}
}

// ViewerExists returns true if a viewer Pod exists for the volumeID.
func (m *volumeBrowserManager) ViewerExists(ctx context.Context, volumeID string) bool {
	_, err := m.client.CoreV1().Pods(m.ns).Get(ctx, viewerPodName(volumeID), metav1.GetOptions{})
	return err == nil
}

// Reconcile deletes idle/failed viewer resources. Called from the Observe loop.
func (m *volumeBrowserManager) Reconcile(ctx context.Context) {
	pods, err := m.client.CoreV1().Pods(m.ns).List(ctx, metav1.ListOptions{
		LabelSelector: k8smanifest.ManagedSelector() + "," + k8smanifest.LabelWorkloadKind + "=" + viewerLabelKind,
	})
	if err != nil {
		return
	}
	for i := range pods.Items {
		pod := &pods.Items[i]
		volumeID := pod.Annotations[k8smanifest.AnnotationVolumeID]

		if pod.Status.Phase == corev1.PodFailed || pod.Status.Phase == corev1.PodSucceeded {
			_ = m.deletePodAndService(ctx, volumeID)
			continue
		}

		if lastAccStr, ok := pod.Annotations[viewerAnnotationLastAccessed]; ok {
			if last, parseErr := time.Parse(time.RFC3339, lastAccStr); parseErr == nil {
				if time.Since(last) > viewerIdleTTL {
					_ = m.deletePodAndService(ctx, volumeID)
					continue
				}
			}
		}
	}

	// Clean up orphan services.
	svcs, err := m.client.CoreV1().Services(m.ns).List(ctx, metav1.ListOptions{
		LabelSelector: k8smanifest.ManagedSelector() + "," + k8smanifest.LabelWorkloadKind + "=" + viewerLabelKind,
	})
	if err != nil {
		return
	}
	for i := range svcs.Items {
		svc := &svcs.Items[i]
		if _, podErr := m.client.CoreV1().Pods(m.ns).Get(ctx, svc.Name, metav1.GetOptions{}); k8serrors.IsNotFound(podErr) {
			_ = m.client.CoreV1().Services(m.ns).Delete(ctx, svc.Name, metav1.DeleteOptions{})
		}
	}
}

// ListFiles calls the viewer's HTTP /files endpoint.
func (m *volumeBrowserManager) ListFiles(ctx context.Context, ep *browserEndpoint, req notebook.FSListFilesRequest) (*notebook.FSListFilesResponse, error) {
	q := url.Values{}
	if len(req.Ext) > 0 {
		q.Set("ext", strings.Join(req.Ext, ","))
	}
	if req.Path != "" {
		q.Set("path", req.Path)
	}
	maxFiles := req.MaxFiles
	if maxFiles <= 0 {
		maxFiles = 500
	}
	q.Set("max_files", fmt.Sprintf("%d", maxFiles))

	reqURL := "http://" + ep.ServiceHost + "/files?" + q.Encode()
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
	if err != nil {
		return nil, err
	}
	if ep.Token != "" {
		httpReq.Header.Set("Authorization", "Bearer "+ep.Token)
	}

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(httpReq)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("viewer HTTP %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	var files []string
	if err := json.Unmarshal(body, &files); err != nil {
		return nil, err
	}
	if files == nil {
		files = []string{}
	}
	return &notebook.FSListFilesResponse{
		Files:     files,
		State:     notebook.FSAccessReady,
		Truncated: resp.Header.Get("X-Piper-Files-Truncated") == "true",
	}, nil
}

func (m *volumeBrowserManager) createPodAndService(ctx context.Context, volumeID, token string) error {
	podName := viewerPodName(volumeID)
	pvcName := notebookPVCName(volumeID)
	labels := m.viewerLabels(volumeID)
	annotations := map[string]string{
		k8smanifest.AnnotationVolumeID: volumeID,
		viewerAnnotationLastAccessed:   time.Now().UTC().Format(time.RFC3339),
	}

	allowPrivilegeEscalation := false
	runAsNonRoot := true
	readOnlyRootFS := true

	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: podName, Namespace: m.ns, Labels: labels, Annotations: annotations},
		Spec: corev1.PodSpec{
			RestartPolicy: corev1.RestartPolicyNever,
			Containers: []corev1.Container{{
				Name:    "browser",
				Image:   m.image,
				Command: []string{"piper"},
				Args:    []string{"internal", "volume-browser", "--root", "/data", "--addr", ":8080"},
				Ports:   []corev1.ContainerPort{{Name: "http", ContainerPort: 8080}},
				Env:     []corev1.EnvVar{{Name: "PIPER_BROWSER_TOKEN", Value: token}},
				SecurityContext: &corev1.SecurityContext{
					AllowPrivilegeEscalation: &allowPrivilegeEscalation,
					RunAsNonRoot:             &runAsNonRoot,
					ReadOnlyRootFilesystem:   &readOnlyRootFS,
					Capabilities:             &corev1.Capabilities{Drop: []corev1.Capability{"ALL"}},
				},
				VolumeMounts: []corev1.VolumeMount{
					{Name: "data", MountPath: "/data", ReadOnly: true},
					{Name: "tmp", MountPath: "/tmp"},
				},
				Resources: corev1.ResourceRequirements{
					Requests: corev1.ResourceList{
						corev1.ResourceCPU:    resource.MustParse("50m"),
						corev1.ResourceMemory: resource.MustParse("64Mi"),
					},
					Limits: corev1.ResourceList{
						corev1.ResourceCPU:    resource.MustParse("200m"),
						corev1.ResourceMemory: resource.MustParse("256Mi"),
					},
				},
			}},
			Volumes: []corev1.Volume{
				{Name: "data", VolumeSource: corev1.VolumeSource{
					PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{ClaimName: pvcName, ReadOnly: true},
				}},
				{Name: "tmp", VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{}}},
			},
		},
	}
	if _, err := m.client.CoreV1().Pods(m.ns).Create(ctx, pod, metav1.CreateOptions{}); err != nil && !k8serrors.IsAlreadyExists(err) {
		return fmt.Errorf("create viewer pod: %w", err)
	}

	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{Name: podName, Namespace: m.ns, Labels: labels, Annotations: annotations},
		Spec: corev1.ServiceSpec{
			Type:     corev1.ServiceTypeClusterIP,
			Selector: labels,
			Ports:    []corev1.ServicePort{{Name: "http", Port: 8080}},
		},
	}
	if _, err := m.client.CoreV1().Services(m.ns).Create(ctx, svc, metav1.CreateOptions{}); err != nil && !k8serrors.IsAlreadyExists(err) {
		return fmt.Errorf("create viewer service: %w", err)
	}
	return nil
}

func (m *volumeBrowserManager) deletePodAndService(ctx context.Context, volumeID string) error {
	podName := viewerPodName(volumeID)
	propagation := metav1.DeletePropagationForeground
	opts := metav1.DeleteOptions{PropagationPolicy: &propagation}
	podErr := m.client.CoreV1().Pods(m.ns).Delete(ctx, podName, opts)
	if podErr != nil && !k8serrors.IsNotFound(podErr) {
		return podErr
	}
	svcErr := m.client.CoreV1().Services(m.ns).Delete(ctx, podName, metav1.DeleteOptions{})
	if svcErr != nil && !k8serrors.IsNotFound(svcErr) {
		return svcErr
	}
	return nil
}

func (m *volumeBrowserManager) waitForReady(ctx context.Context, podName string, timeout time.Duration) (*corev1.Pod, error) {
	deadline := time.Now().Add(timeout)
	for {
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("timeout waiting for viewer pod ready")
		}
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		pod, err := m.client.CoreV1().Pods(m.ns).Get(ctx, podName, metav1.GetOptions{})
		if err != nil {
			return nil, err
		}
		if isPodReady(pod) {
			return pod, nil
		}
		if pod.Status.Phase == corev1.PodFailed {
			return nil, fmt.Errorf("viewer pod failed")
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(2 * time.Second):
		}
	}
}

func (m *volumeBrowserManager) touchAnnotation(ctx context.Context, pod *corev1.Pod) error {
	lastStr := pod.Annotations[viewerAnnotationLastAccessed]
	if lastStr != "" {
		if last, err := time.Parse(time.RFC3339, lastStr); err == nil && time.Since(last) < 30*time.Second {
			return nil
		}
	}
	fresh, err := m.client.CoreV1().Pods(m.ns).Get(ctx, pod.Name, metav1.GetOptions{})
	if err != nil {
		return err
	}
	if fresh.Annotations == nil {
		fresh.Annotations = make(map[string]string)
	}
	fresh.Annotations[viewerAnnotationLastAccessed] = time.Now().UTC().Format(time.RFC3339)
	_, err = m.client.CoreV1().Pods(m.ns).Update(ctx, fresh, metav1.UpdateOptions{})
	return err
}

func (m *volumeBrowserManager) endpointFromPod(pod *corev1.Pod) *browserEndpoint {
	token := ""
	if len(pod.Spec.Containers) > 0 {
		for _, env := range pod.Spec.Containers[0].Env {
			if env.Name == "PIPER_BROWSER_TOKEN" {
				token = env.Value
			}
		}
	}
	svcName := pod.Name
	return &browserEndpoint{
		ServiceHost: fmt.Sprintf("%s.%s.svc.cluster.local:8080", svcName, m.ns),
		Token:       token,
	}
}

func (m *volumeBrowserManager) viewerLabels(volumeID string) map[string]string {
	return map[string]string{
		k8smanifest.LabelManagedBy:    k8smanifest.ManagedByPiper,
		k8smanifest.LabelWorkloadKind: viewerLabelKind,
		k8smanifest.LabelWorkloadID:   k8smanifest.LabelValue(volumeID),
		k8smanifest.LabelWorkerID:     k8smanifest.LabelValue(m.workerID),
	}
}

func isPodReady(pod *corev1.Pod) bool {
	if pod.Status.Phase != corev1.PodRunning {
		return false
	}
	for _, cond := range pod.Status.Conditions {
		if cond.Type == corev1.PodReady && cond.Status == corev1.ConditionTrue {
			return true
		}
	}
	return false
}

func viewerPodName(volumeID string) string {
	clean := strings.ReplaceAll(volumeID, "-", "")
	if len(clean) > 12 {
		clean = clean[:12]
	}
	return "piper-nb-browser-" + clean
}

type transitioningError struct{ msg string }

func (e *transitioningError) Error() string { return e.msg }

func isTransitioningError(err error) bool {
	_, ok := err.(*transitioningError)
	return ok
}
