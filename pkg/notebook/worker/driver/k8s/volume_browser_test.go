package notebookworker

import (
	"context"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"

	"github.com/piper/piper/pkg/internal/k8smeta"
)

const testNS = "notebooks"
const testWorkerID = "worker-1"
const testImage = "piper:latest"

func newTestManager(client *fake.Clientset) *volumeBrowserManager {
	return newVolumeBrowserManager(client, testNS, testWorkerID, testImage)
}

func TestViewerExists_ReturnsFalseWhenNoPod(t *testing.T) {
	client := fake.NewSimpleClientset()
	mgr := newTestManager(client)

	if mgr.ViewerExists(context.Background(), "vol-123") {
		t.Fatal("expected false, pod does not exist")
	}
}

func TestViewerExists_ReturnsTrueWhenPodExists(t *testing.T) {
	client := fake.NewSimpleClientset()
	mgr := newTestManager(client)

	podName := viewerPodName("vol-123")
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: podName, Namespace: testNS},
	}
	if _, err := client.CoreV1().Pods(testNS).Create(context.Background(), pod, metav1.CreateOptions{}); err != nil {
		t.Fatal(err)
	}

	if !mgr.ViewerExists(context.Background(), "vol-123") {
		t.Fatal("expected true")
	}
}

func TestStop_DeletesPodAndService(t *testing.T) {
	client := fake.NewSimpleClientset()
	mgr := newTestManager(client)

	podName := viewerPodName("vol-123")
	pod := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: podName, Namespace: testNS}}
	svc := &corev1.Service{ObjectMeta: metav1.ObjectMeta{Name: podName, Namespace: testNS}}
	_, _ = client.CoreV1().Pods(testNS).Create(context.Background(), pod, metav1.CreateOptions{})
	_, _ = client.CoreV1().Services(testNS).Create(context.Background(), svc, metav1.CreateOptions{})

	if err := mgr.Stop(context.Background(), "vol-123"); err != nil {
		t.Fatalf("Stop error: %v", err)
	}

	if mgr.ViewerExists(context.Background(), "vol-123") {
		t.Fatal("pod should be gone after Stop")
	}
}

func TestStop_Idempotent(t *testing.T) {
	client := fake.NewSimpleClientset()
	mgr := newTestManager(client)

	// Stop on non-existent resources should not error.
	if err := mgr.Stop(context.Background(), "vol-404"); err != nil {
		t.Fatalf("Stop on missing pod returned error: %v", err)
	}
}

func TestReconcile_DeletesIdlePod(t *testing.T) {
	client := fake.NewSimpleClientset()
	mgr := newTestManager(client)

	volumeID := "vol-idle"
	podName := viewerPodName(volumeID)

	// Create a pod with an old last-accessed timestamp.
	old := time.Now().Add(-10 * time.Minute).UTC().Format(time.RFC3339)
	labels := mgr.viewerLabels(volumeID)
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      podName,
			Namespace: testNS,
			Labels:    labels,
			Annotations: map[string]string{
				k8smeta.AnnotationVolumeID:   volumeID,
				viewerAnnotationLastAccessed: old,
			},
		},
		Status: corev1.PodStatus{Phase: corev1.PodRunning},
	}
	if _, err := client.CoreV1().Pods(testNS).Create(context.Background(), pod, metav1.CreateOptions{}); err != nil {
		t.Fatal(err)
	}

	mgr.Reconcile(context.Background())

	if mgr.ViewerExists(context.Background(), volumeID) {
		t.Fatal("idle pod should have been deleted by Reconcile")
	}
}

func TestReconcile_DeletesFailedPod(t *testing.T) {
	client := fake.NewSimpleClientset()
	mgr := newTestManager(client)

	volumeID := "vol-failed"
	podName := viewerPodName(volumeID)
	labels := mgr.viewerLabels(volumeID)
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      podName,
			Namespace: testNS,
			Labels:    labels,
			Annotations: map[string]string{
				k8smeta.AnnotationVolumeID: volumeID,
			},
		},
		Status: corev1.PodStatus{Phase: corev1.PodFailed},
	}
	_, _ = client.CoreV1().Pods(testNS).Create(context.Background(), pod, metav1.CreateOptions{})

	mgr.Reconcile(context.Background())

	if mgr.ViewerExists(context.Background(), volumeID) {
		t.Fatal("failed pod should have been deleted by Reconcile")
	}
}

func TestReconcile_DeletesOrphanService(t *testing.T) {
	client := fake.NewSimpleClientset()
	mgr := newTestManager(client)

	volumeID := "vol-orphan"
	svcName := viewerPodName(volumeID)
	labels := mgr.viewerLabels(volumeID)

	// Create service without corresponding pod.
	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      svcName,
			Namespace: testNS,
			Labels:    labels,
		},
	}
	_, _ = client.CoreV1().Services(testNS).Create(context.Background(), svc, metav1.CreateOptions{})

	mgr.Reconcile(context.Background())

	_, err := client.CoreV1().Services(testNS).Get(context.Background(), svcName, metav1.GetOptions{})
	if err == nil {
		t.Fatal("orphan service should have been deleted")
	}
}

func TestReconcile_KeepsActivePod(t *testing.T) {
	client := fake.NewSimpleClientset()
	mgr := newTestManager(client)

	volumeID := "vol-active"
	podName := viewerPodName(volumeID)

	recent := time.Now().UTC().Format(time.RFC3339)
	labels := mgr.viewerLabels(volumeID)
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      podName,
			Namespace: testNS,
			Labels:    labels,
			Annotations: map[string]string{
				k8smeta.AnnotationVolumeID:   volumeID,
				viewerAnnotationLastAccessed: recent,
			},
		},
		Status: corev1.PodStatus{Phase: corev1.PodRunning},
	}
	_, _ = client.CoreV1().Pods(testNS).Create(context.Background(), pod, metav1.CreateOptions{})

	mgr.Reconcile(context.Background())

	if !mgr.ViewerExists(context.Background(), volumeID) {
		t.Fatal("active pod should not have been deleted")
	}
}

func TestViewerPodName_Stable(t *testing.T) {
	name1 := viewerPodName("abc-def-123")
	name2 := viewerPodName("abc-def-123")
	if name1 != name2 {
		t.Fatalf("viewerPodName is not deterministic: %q vs %q", name1, name2)
	}
	if !hasPrefix(name1, "piper-nb-browser-") {
		t.Fatalf("unexpected prefix: %q", name1)
	}
}

func hasPrefix(s, prefix string) bool {
	return len(s) >= len(prefix) && s[:len(prefix)] == prefix
}
