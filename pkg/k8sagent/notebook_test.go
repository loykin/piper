package k8sagent

import (
	"context"
	"encoding/json"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"

	iagent "github.com/piper/piper/internal/agent"
	"github.com/piper/piper/internal/tunnel"
)

func TestNotebookProvisionVolumeCreatesPVC(t *testing.T) {
	client := fake.NewSimpleClientset()
	a := New(Config{
		ID:                "agent-1",
		ClusterName:       "gpu-a",
		K8sClient:         client,
		NotebookNamespace: "notebooks",
		StorageSize:       "5Gi",
	})

	resp, err := a.provisionNotebookVolume(context.Background(), notebookProvisionVolumeRequest{VolumeID: "vol-1234567890abcdef"})
	if err != nil {
		t.Fatalf("provisionNotebookVolume returned error: %v", err)
	}
	if resp.WorkDir != "/home/jovyan" {
		t.Fatalf("work dir = %q", resp.WorkDir)
	}
	pvc, err := client.CoreV1().PersistentVolumeClaims("notebooks").Get(context.Background(), "piper-nb-vol-vol123456789", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get pvc: %v", err)
	}
	if pvc.Labels["piper.io/cluster"] != "gpu-a" {
		t.Fatalf("cluster label = %q", pvc.Labels["piper.io/cluster"])
	}
	if got := pvc.Spec.Resources.Requests[corev1.ResourceStorage]; got.String() != "5Gi" {
		t.Fatalf("storage request = %s", got.String())
	}
}

func TestNotebookStartCreatesStatefulSetAndService(t *testing.T) {
	client := fake.NewSimpleClientset()
	a := New(Config{
		ID:                "agent-1",
		ClusterName:       "gpu-a",
		K8sClient:         client,
		NotebookNamespace: "notebooks",
		NotebookImage:     "jupyter/minimal-notebook:latest",
	})

	resp, err := a.startNotebook(context.Background(), notebookStartRequest{
		YAML: `
metadata:
  name: My Notebook
spec: {}
`,
		VolumeID: "vol-abc",
	})
	if err != nil {
		t.Fatalf("startNotebook returned error: %v", err)
	}
	if resp.Endpoint != "tunnel://agent-1/nb/My Notebook" {
		t.Fatalf("endpoint = %q", resp.Endpoint)
	}
	sts, err := client.AppsV1().StatefulSets("notebooks").Get(context.Background(), "piper-nb-my-notebook", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get statefulset: %v", err)
	}
	if sts.Spec.Template.Spec.Containers[0].Image != "jupyter/minimal-notebook:latest" {
		t.Fatalf("image = %q", sts.Spec.Template.Spec.Containers[0].Image)
	}
	if sts.Spec.Template.Spec.Volumes[0].PersistentVolumeClaim.ClaimName != "piper-nb-vol-volabc" {
		t.Fatalf("claim name = %q", sts.Spec.Template.Spec.Volumes[0].PersistentVolumeClaim.ClaimName)
	}
	svc, err := client.CoreV1().Services("notebooks").Get(context.Background(), "piper-nb-my-notebook", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get service: %v", err)
	}
	if svc.Spec.ClusterIP != "None" {
		t.Fatalf("service clusterIP = %q", svc.Spec.ClusterIP)
	}
}

func TestNotebookRPCHandlersAreRegisteredWhenK8sClientExists(t *testing.T) {
	a := New(Config{ID: "agent-1", ClusterName: "gpu-a", K8sClient: fake.NewSimpleClientset()})
	payload, _ := json.Marshal(notebookProvisionVolumeRequest{VolumeID: "vol-123"})

	resp := a.dispatcher.Handle(context.Background(), tunnel.Frame{
		Type:    tunnel.FrameRPCRequest,
		ID:      "1",
		Method:  iagent.MethodNotebookProvisionVolume,
		Payload: payload,
	})
	if resp.Status != "ok" {
		t.Fatalf("status = %q error=%q", resp.Status, resp.Error)
	}
}

func TestNotebookRPCHandlersAreNotRegisteredWithoutK8sClient(t *testing.T) {
	a := New(Config{ID: "agent-1", ClusterName: "gpu-a"})
	resp := a.dispatcher.Handle(context.Background(), tunnel.Frame{
		Type:   tunnel.FrameRPCRequest,
		ID:     "1",
		Method: iagent.MethodNotebookProvisionVolume,
	})
	if resp.Status != "error" {
		t.Fatalf("status = %q, want error", resp.Status)
	}
}

func TestNotebookStopScalesStatefulSetToZero(t *testing.T) {
	replicas := int32(1)
	client := fake.NewSimpleClientset(&appsv1.StatefulSet{
		ObjectMeta: metav1.ObjectMeta{Name: "piper-nb-demo", Namespace: "notebooks"},
		Spec: appsv1.StatefulSetSpec{
			Replicas: &replicas,
		},
	})
	a := New(Config{ID: "agent-1", ClusterName: "gpu-a", K8sClient: client, NotebookNamespace: "notebooks"})

	if err := a.stopNotebook(context.Background(), notebookStopRequest{Name: "demo"}); err != nil {
		t.Fatalf("stopNotebook returned error: %v", err)
	}
	sts, err := client.AppsV1().StatefulSets("notebooks").Get(context.Background(), "piper-nb-demo", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get statefulset: %v", err)
	}
	if sts.Spec.Replicas == nil || *sts.Spec.Replicas != 0 {
		t.Fatalf("replicas = %v, want 0", sts.Spec.Replicas)
	}
}

func TestNotebookDeprovisionDeletesPVC(t *testing.T) {
	client := fake.NewSimpleClientset(&corev1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{Name: "piper-nb-vol-volabc", Namespace: "notebooks"},
	})
	a := New(Config{ID: "agent-1", ClusterName: "gpu-a", K8sClient: client, NotebookNamespace: "notebooks"})

	if err := a.deprovisionNotebookVolume(context.Background(), notebookDeprovisionVolumeRequest{VolumeID: "vol-abc"}); err != nil {
		t.Fatalf("deprovisionNotebookVolume returned error: %v", err)
	}
	if _, err := client.CoreV1().PersistentVolumeClaims("notebooks").Get(context.Background(), "piper-nb-vol-volabc", metav1.GetOptions{}); err == nil {
		t.Fatal("expected PVC to be deleted")
	}
}

func TestNotebookStartUpdatesExistingStatefulSet(t *testing.T) {
	client := fake.NewSimpleClientset(&appsv1.StatefulSet{
		ObjectMeta: metav1.ObjectMeta{Name: "piper-nb-demo", Namespace: "notebooks"},
		Spec: appsv1.StatefulSetSpec{
			Template: corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{{Name: "notebook", Image: "old"}},
				},
			},
		},
	})
	a := New(Config{
		ID:                "agent-1",
		ClusterName:       "gpu-a",
		K8sClient:         client,
		NotebookNamespace: "notebooks",
		NotebookImage:     "new-image",
	})

	if _, err := a.startNotebook(context.Background(), notebookStartRequest{
		YAML:     "metadata:\n  name: demo\nspec: {}\n",
		VolumeID: "vol-demo",
	}); err != nil {
		t.Fatalf("startNotebook returned error: %v", err)
	}
	sts, err := client.AppsV1().StatefulSets("notebooks").Get(context.Background(), "piper-nb-demo", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get statefulset: %v", err)
	}
	if sts.Spec.Template.Spec.Containers[0].Image != "new-image" {
		t.Fatalf("image = %q, want new-image", sts.Spec.Template.Spec.Containers[0].Image)
	}
}

func TestNotebookSyncStatusReportsReadyStatefulSet(t *testing.T) {
	replicas := int32(1)
	client := fake.NewSimpleClientset(&appsv1.StatefulSet{
		ObjectMeta: metav1.ObjectMeta{Name: "piper-nb-demo", Namespace: "notebooks"},
		Spec: appsv1.StatefulSetSpec{
			Replicas: &replicas,
		},
		Status: appsv1.StatefulSetStatus{
			ReadyReplicas: 1,
		},
	})
	a := New(Config{ID: "agent-1", ClusterName: "gpu-a", K8sClient: client, NotebookNamespace: "notebooks"})

	resp, err := a.syncNotebookStatus(context.Background(), notebookSyncStatusRequest{Names: []string{"demo"}})
	if err != nil {
		t.Fatalf("syncNotebookStatus returned error: %v", err)
	}
	if resp.Statuses["demo"] != "running" {
		t.Fatalf("status = %q, want running", resp.Statuses["demo"])
	}
}
