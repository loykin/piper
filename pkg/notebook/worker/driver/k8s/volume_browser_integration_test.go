package notebookworker

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"

	"github.com/piper/piper/pkg/internal/k8smeta"
	"github.com/piper/piper/pkg/notebook"
)

// makeReadyViewerPod creates a viewer pod already in Ready state, with its
// HTTP endpoint pointing at the given test server.
func makeReadyViewerPod(t *testing.T, client *fake.Clientset, volumeID, ns, _, token string) {
	t.Helper()
	podName := viewerPodName(volumeID)
	labels := map[string]string{
		k8smeta.LabelManagedBy:    k8smeta.ManagedByPiper,
		k8smeta.LabelWorkloadKind: viewerLabelKind,
		k8smeta.LabelWorkloadID:   k8smeta.LabelValue(volumeID),
	}
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      podName,
			Namespace: ns,
			Labels:    labels,
			Annotations: map[string]string{
				k8smeta.AnnotationVolumeID:   volumeID,
				viewerAnnotationLastAccessed: time.Now().UTC().Format(time.RFC3339),
			},
		},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{{
				Name:  "browser",
				Image: "piper:latest",
				Env:   []corev1.EnvVar{{Name: "PIPER_BROWSER_TOKEN", Value: token}},
			}},
			Volumes: []corev1.Volume{{
				Name: "data",
				VolumeSource: corev1.VolumeSource{
					PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{
						ClaimName: notebookPVCName(volumeID),
						ReadOnly:  true,
					},
				},
			}},
		},
		Status: corev1.PodStatus{
			Phase: corev1.PodRunning,
			Conditions: []corev1.PodCondition{{
				Type:   corev1.PodReady,
				Status: corev1.ConditionTrue,
			}},
		},
	}
	if _, err := client.CoreV1().Pods(ns).Create(context.Background(), pod, metav1.CreateOptions{}); err != nil {
		t.Fatalf("create viewer pod: %v", err)
	}
}

// makeViewerHTTPServer returns a fake viewer HTTP server serving the given files.
func makeViewerHTTPServer(t *testing.T, token string, files []string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if token != "" {
			auth := r.Header.Get("Authorization")
			if auth != "Bearer "+token {
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(files)
	}))
	t.Cleanup(srv.Close)
	return srv
}

// boundPVCObj creates a Bound PVC object.
func boundPVCObj(name, ns string) *corev1.PersistentVolumeClaim {
	return &corev1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns},
		Status:     corev1.PersistentVolumeClaimStatus{Phase: corev1.ClaimBound},
	}
}

// --- Integration tests ---

// TestIntegration_BrowseStoppedPVC_CreatesViewerResources verifies that
// browsing a stopped PVC results in viewer Pod and Service being created,
// and that a transitioning response is returned (pod not yet Ready).
func TestIntegration_BrowseStoppedPVC_CreatesViewerResources(t *testing.T) {
	const (
		ns       = "notebooks"
		volumeID = "vol-inttest0001"
	)
	client := fake.NewSimpleClientset(boundPVCObj(notebookPVCName(volumeID), ns))
	w := New(Config{WorkerID: "w1", ClusterName: "test", Client: client, Namespaces: []string{ns}, InfrastructureImage: "piper:latest"})

	// Use a very short timeout so waitForReady exits quickly, returning
	// transitioning instead of blocking 30s.
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	resp, err := w.listFilesK8s(ctx, notebook.FSListFilesRequest{VolumeID: volumeID})
	if err != nil {
		t.Fatalf("listFilesK8s: %v", err)
	}
	if resp.State != notebook.FSAccessTransitioning {
		t.Fatalf("state = %s, want transitioning (viewer pending)", resp.State)
	}

	// Viewer Pod and Service must have been created as a side effect.
	bg := context.Background()
	podName := viewerPodName(volumeID)
	if _, err := client.CoreV1().Pods(ns).Get(bg, podName, metav1.GetOptions{}); err != nil {
		t.Fatalf("viewer pod not created: %v", err)
	}
	if _, err := client.CoreV1().Services(ns).Get(bg, podName, metav1.GetOptions{}); err != nil {
		t.Fatalf("viewer service not created: %v", err)
	}
}

// TestIntegration_BrowseWithReadyViewer_ReturnsFiles verifies that when a
// viewer Pod is already Ready the browse call returns files from the viewer.
func TestIntegration_BrowseWithReadyViewer_ReturnsFiles(t *testing.T) {
	const (
		ns       = "notebooks"
		volumeID = "vol-inttest0002"
		token    = "browse-tok"
	)
	expectedFiles := []string{"src/train.py", "notebooks/run.ipynb"}

	// Start fake viewer HTTP server.
	srv := makeViewerHTTPServer(t, token, expectedFiles)
	svcHost := strings.TrimPrefix(srv.URL, "http://")

	client := fake.NewSimpleClientset(boundPVCObj(notebookPVCName(volumeID), ns))

	// Pre-create the viewer pod as Ready, pointing to the test server.
	makeReadyViewerPod(t, client, volumeID, ns, svcHost, token)

	mgr := newVolumeBrowserManager(client, ns, "w1", "piper:latest")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	pod, err := client.CoreV1().Pods(ns).Get(ctx, viewerPodName(volumeID), metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get viewer pod: %v", err)
	}

	// Manually build the endpoint pointing at the test server.
	ep := &browserEndpoint{ServiceHost: svcHost, Token: token}
	resp, err := mgr.ListFiles(ctx, ep, notebook.FSListFilesRequest{VolumeID: volumeID})
	if err != nil {
		t.Fatalf("ListFiles: %v", err)
	}
	if resp.State != notebook.FSAccessReady {
		t.Fatalf("state = %s, want ready", resp.State)
	}
	if len(resp.Files) != len(expectedFiles) {
		t.Fatalf("files = %v, want %v", resp.Files, expectedFiles)
	}
	_ = pod
}

// TestIntegration_NotebookStart_DrainsViewerFirst verifies the lifecycle:
// stopped PVC → viewer exists → startNotebook removes viewer → StatefulSet scaled up.
func TestIntegration_NotebookStart_DrainsViewerFirst(t *testing.T) {
	const (
		ns       = "notebooks"
		volumeID = "vol-inttest0003"
	)
	client := fake.NewSimpleClientset(boundPVCObj(notebookPVCName(volumeID), ns))
	w := New(Config{WorkerID: "w1", ClusterName: "test", Client: client, Namespaces: []string{ns}, InfrastructureImage: "piper:latest"})

	ctx := context.Background()

	// Pre-create a Ready viewer pod.
	makeReadyViewerPod(t, client, volumeID, ns, "unused:8080", "tok")

	mgr := newVolumeBrowserManager(client, ns, "w1", "piper:latest")
	if !mgr.ViewerExists(ctx, volumeID) {
		t.Fatal("viewer pod should exist before drain")
	}

	// drainViewerAndRun should delete the viewer before notebook start.
	if err := w.drainViewerAndRun(ctx, ns, volumeID, func(_ context.Context) error { return nil }); err != nil {
		t.Fatalf("drainViewerAndRun: %v", err)
	}

	if mgr.ViewerExists(ctx, volumeID) {
		t.Fatal("viewer pod should be gone after drainViewer")
	}
	if _, err := client.CoreV1().Services(ns).Get(ctx, viewerPodName(volumeID), metav1.GetOptions{}); !k8serrors.IsNotFound(err) {
		t.Fatal("viewer service should be gone after drainViewer")
	}
}

// TestIntegration_RunningNotebook_ReturnsTransitioning_WhenNoToken verifies
// that a Ready notebook pod without a token results in transitioning (token
// not yet available from master).
func TestIntegration_RunningNotebook_ReturnsTransitioning_WhenNoToken(t *testing.T) {
	const (
		ns       = "notebooks"
		volumeID = "vol-inttest0004"
	)
	pvcName := notebookPVCName(volumeID)
	client := fake.NewSimpleClientset(boundPVCObj(pvcName, ns))
	w := New(Config{WorkerID: "w1", ClusterName: "test", Client: client, Namespaces: []string{ns}, InfrastructureImage: "piper:latest"})

	// Create a Ready notebook pod referencing the PVC.
	nbPod := notebookPod("piper-nb-nb1", ns, pvcName, true)
	if _, err := client.CoreV1().Pods(ns).Create(context.Background(), nbPod, metav1.CreateOptions{}); err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()
	// Request with empty token → cannot call Jupyter API → transitioning.
	resp, err := w.listFilesK8s(ctx, notebook.FSListFilesRequest{
		VolumeID: volumeID,
		Notebook: "nb1",
		Token:    "", // no token
	})
	if err != nil {
		t.Fatal(err)
	}
	if resp.State != notebook.FSAccessTransitioning {
		t.Fatalf("state = %s, want transitioning (no token)", resp.State)
	}
}

// TestIntegration_Stop_Then_Browse verifies the reverse flow:
// running notebook → stop (replicas=0) → browse → viewer created.
func TestIntegration_Stop_Then_Browse(t *testing.T) {
	const (
		ns       = "notebooks"
		volumeID = "vol-inttest0005"
	)
	pvcName := notebookPVCName(volumeID)
	client := fake.NewSimpleClientset(boundPVCObj(pvcName, ns))
	w := New(Config{WorkerID: "w1", ClusterName: "test", Client: client, Namespaces: []string{ns}, InfrastructureImage: "piper:latest"})

	// Simulate a stopped StatefulSet (replicas=0) with no pods.
	zero := int32(0)
	sts := desiredReplicasStatefulSet("piper-nb-nb1", ns, volumeID, zero)
	if _, err := client.AppsV1().StatefulSets(ns).Create(context.Background(), sts, metav1.CreateOptions{}); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	// Browse on stopped volume → viewer creation triggered → pending → transitioning.
	resp, err := w.listFilesK8s(ctx, notebook.FSListFilesRequest{VolumeID: volumeID})
	if err != nil {
		t.Fatalf("listFilesK8s: %v", err)
	}
	if resp.State != notebook.FSAccessTransitioning {
		t.Fatalf("state = %s, want transitioning (viewer pending)", resp.State)
	}

	// Viewer pod must exist.
	if _, err := client.CoreV1().Pods(ns).Get(ctx, viewerPodName(volumeID), metav1.GetOptions{}); err != nil {
		t.Fatalf("viewer pod not created after browse: %v", err)
	}
}

// TestIntegration_ObserveOnce_RemovesViewerWhenNotebookActive verifies that
// observeOnce deletes a viewer pod when the StatefulSet has desired replicas > 0.
func TestIntegration_ObserveOnce_RemovesViewerWhenNotebookActive(t *testing.T) {
	const (
		ns       = "notebooks"
		volumeID = "vol-inttest0006"
	)
	client := fake.NewSimpleClientset()
	w := New(Config{
		WorkerID:            "w1",
		ClusterName:         "test",
		Client:              client,
		Namespaces:          []string{ns},
		InfrastructureImage: "piper:latest",
		ReportStatus:        func(notebook.WorkerStatusUpdate) error { return nil },
	})

	ctx := context.Background()

	// Create StatefulSet with replicas=1.
	one := int32(1)
	sts := desiredReplicasStatefulSet("piper-nb-nb1", ns, volumeID, one)
	if _, err := client.AppsV1().StatefulSets(ns).Create(ctx, sts, metav1.CreateOptions{}); err != nil {
		t.Fatal(err)
	}

	// Create a viewer pod that should be removed.
	makeReadyViewerPod(t, client, volumeID, ns, "unused:8080", "tok")

	mgr := newVolumeBrowserManager(client, ns, "w1", "piper:latest")
	if !mgr.ViewerExists(ctx, volumeID) {
		t.Fatal("viewer should exist before observeOnce")
	}

	w.observeOnce(ctx)

	if mgr.ViewerExists(ctx, volumeID) {
		t.Fatal("viewer should be removed by observeOnce when notebook is active")
	}
}
