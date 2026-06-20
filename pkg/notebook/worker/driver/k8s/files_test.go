package notebookworker

import (
	"context"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"

	"github.com/piper/piper/pkg/internal/k8smeta"
	"github.com/piper/piper/pkg/notebook"
)

func newK8sWorker(client *fake.Clientset) *Worker {
	return New(Config{
		WorkerID:            "worker-1",
		ClusterName:         "test",
		Client:              client,
		Namespaces:          []string{"notebooks"},
		InfrastructureImage: "piper:latest",
	})
}

func boundPVC(name, ns string) *corev1.PersistentVolumeClaim {
	return &corev1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns},
		Status:     corev1.PersistentVolumeClaimStatus{Phase: corev1.ClaimBound},
	}
}

func pendingPVC(name, ns string) *corev1.PersistentVolumeClaim {
	return &corev1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns},
		Status:     corev1.PersistentVolumeClaimStatus{Phase: corev1.ClaimPending},
	}
}

func lostPVC(name, ns string) *corev1.PersistentVolumeClaim {
	return &corev1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns},
		Status:     corev1.PersistentVolumeClaimStatus{Phase: corev1.ClaimLost},
	}
}

func notebookPod(name, ns, pvcName string, ready bool) *corev1.Pod {
	phase := corev1.PodPending
	var conditions []corev1.PodCondition
	if ready {
		phase = corev1.PodRunning
		conditions = []corev1.PodCondition{{Type: corev1.PodReady, Status: corev1.ConditionTrue}}
	}
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: ns,
			Labels:    map[string]string{k8smeta.LabelWorkloadKind: "notebook"},
		},
		Spec: corev1.PodSpec{
			Volumes: []corev1.Volume{{
				Name: "data",
				VolumeSource: corev1.VolumeSource{
					PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{ClaimName: pvcName},
				},
			}},
		},
		Status: corev1.PodStatus{Phase: phase, Conditions: conditions},
	}
}

func terminatingNotebookPod(name, ns, pvcName string) *corev1.Pod {
	pod := notebookPod(name, ns, pvcName, false)
	now := metav1.Now()
	pod.DeletionTimestamp = &now
	return pod
}

func viewerPod(volumeID, ns, pvcName string, ready bool) *corev1.Pod {
	name := viewerPodName(volumeID)
	phase := corev1.PodPending
	var conditions []corev1.PodCondition
	if ready {
		phase = corev1.PodRunning
		conditions = []corev1.PodCondition{{Type: corev1.PodReady, Status: corev1.ConditionTrue}}
	}
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: ns,
			Labels: map[string]string{
				k8smeta.LabelWorkloadKind: viewerLabelKind,
				k8smeta.LabelWorkloadID:   k8smeta.LabelValue(volumeID),
			},
			Annotations: map[string]string{k8smeta.AnnotationVolumeID: volumeID},
		},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{{
				Name:  "browser",
				Image: "piper:latest",
				Env:   []corev1.EnvVar{{Name: "PIPER_BROWSER_TOKEN", Value: "tok"}},
			}},
			Volumes: []corev1.Volume{{
				Name: "data",
				VolumeSource: corev1.VolumeSource{
					PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{
						ClaimName: pvcName,
						ReadOnly:  true,
					},
				},
			}},
		},
		Status: corev1.PodStatus{
			Phase:      phase,
			Conditions: conditions,
		},
	}
}

func desiredReplicasStatefulSet(name, ns, volumeID string, replicas int32) *appsv1.StatefulSet {
	return &appsv1.StatefulSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: ns,
			Labels: map[string]string{
				k8smeta.LabelManagedBy:    k8smeta.ManagedByPiper,
				k8smeta.LabelWorkloadKind: "notebook",
			},
			Annotations: map[string]string{k8smeta.AnnotationVolumeID: volumeID},
		},
		Spec: appsv1.StatefulSetSpec{Replicas: &replicas},
	}
}

const testVolumeID = "vol-abc123456789"

func req() notebook.FSListFilesRequest {
	return notebook.FSListFilesRequest{VolumeID: testVolumeID, Notebook: "nb1", Token: "tok"}
}

func pvcName() string { return notebookPVCName(testVolumeID) }

// --- Tests ---

func TestListFilesK8s_PVCNotFound_ReturnsUnavailable(t *testing.T) {
	client := fake.NewSimpleClientset()
	w := newK8sWorker(client)

	resp, err := w.listFilesK8s(context.Background(), req())
	if err != nil {
		t.Fatal(err)
	}
	if resp.State != notebook.FSAccessUnavailable {
		t.Fatalf("state = %s, want unavailable", resp.State)
	}
}

func TestListFilesK8s_PVCLost_ReturnsUnavailable(t *testing.T) {
	client := fake.NewSimpleClientset(lostPVC(pvcName(), "notebooks"))
	w := newK8sWorker(client)

	resp, err := w.listFilesK8s(context.Background(), req())
	if err != nil {
		t.Fatal(err)
	}
	if resp.State != notebook.FSAccessUnavailable {
		t.Fatalf("state = %s, want unavailable", resp.State)
	}
}

func TestListFilesK8s_PVCPending_ReturnsTransitioning(t *testing.T) {
	client := fake.NewSimpleClientset(pendingPVC(pvcName(), "notebooks"))
	w := newK8sWorker(client)

	resp, err := w.listFilesK8s(context.Background(), req())
	if err != nil {
		t.Fatal(err)
	}
	if resp.State != notebook.FSAccessTransitioning {
		t.Fatalf("state = %s, want transitioning", resp.State)
	}
}

func TestListFilesK8s_NotebookPodPending_ReturnsTransitioning(t *testing.T) {
	client := fake.NewSimpleClientset(
		boundPVC(pvcName(), "notebooks"),
		notebookPod("piper-nb-nb1", "notebooks", pvcName(), false),
	)
	w := newK8sWorker(client)

	resp, err := w.listFilesK8s(context.Background(), req())
	if err != nil {
		t.Fatal(err)
	}
	if resp.State != notebook.FSAccessTransitioning {
		t.Fatalf("state = %s, want transitioning", resp.State)
	}
}

func TestListFilesK8s_NotebookPodTerminating_ReturnsTransitioning(t *testing.T) {
	client := fake.NewSimpleClientset(
		boundPVC(pvcName(), "notebooks"),
		terminatingNotebookPod("piper-nb-nb1", "notebooks", pvcName()),
	)
	w := newK8sWorker(client)

	resp, err := w.listFilesK8s(context.Background(), req())
	if err != nil {
		t.Fatal(err)
	}
	if resp.State != notebook.FSAccessTransitioning {
		t.Fatalf("state = %s, want transitioning", resp.State)
	}
}

func TestListFilesK8s_StatefulSetReplicas1NoPod_ReturnsTransitioning(t *testing.T) {
	client := fake.NewSimpleClientset(
		boundPVC(pvcName(), "notebooks"),
		desiredReplicasStatefulSet("piper-nb-nb1", "notebooks", testVolumeID, 1),
	)
	w := newK8sWorker(client)

	resp, err := w.listFilesK8s(context.Background(), req())
	if err != nil {
		t.Fatal(err)
	}
	if resp.State != notebook.FSAccessTransitioning {
		t.Fatalf("state = %s, want transitioning", resp.State)
	}
}

func TestListFilesK8s_ViewerPodPending_ReturnsTransitioning(t *testing.T) {
	client := fake.NewSimpleClientset(
		boundPVC(pvcName(), "notebooks"),
		viewerPod(testVolumeID, "notebooks", pvcName(), false),
	)
	w := newK8sWorker(client)

	resp, err := w.listFilesK8s(context.Background(), req())
	if err != nil {
		t.Fatal(err)
	}
	if resp.State != notebook.FSAccessTransitioning {
		t.Fatalf("state = %s, want transitioning", resp.State)
	}
}

func TestListFilesK8s_EmptyVolumeID_ReturnsReady(t *testing.T) {
	client := fake.NewSimpleClientset()
	w := newK8sWorker(client)

	resp, err := w.listFilesK8s(context.Background(), notebook.FSListFilesRequest{VolumeID: ""})
	if err != nil {
		t.Fatal(err)
	}
	if resp.State != notebook.FSAccessReady {
		t.Fatalf("state = %s, want ready", resp.State)
	}
}
