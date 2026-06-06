package notebookworker

import (
	"context"
	"strings"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"

	"github.com/piper/piper/pkg/notebook"
)

func TestNotebookProvisionVolumeCreatesPVC(t *testing.T) {
	client := fake.NewSimpleClientset()
	a := New(Config{
		WorkerID:    "worker-1",
		ClusterName: "gpu-a",
		Client:      client,
		Namespace:   "notebooks",
		StorageSize: "5Gi",
	})

	resp, err := a.provisionNotebookVolume(context.Background(), notebook.WorkerProvisionVolumeRequest{VolumeID: "vol-1234567890abcdef"})
	if err != nil {
		t.Fatalf("provisionNotebookVolume returned error: %v", err)
	}
	if resp.WorkDir != notebook.ContainerWorkDir {
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
		WorkerID:    "worker-1",
		ClusterName: "gpu-a",
		Client:      client,
		Namespace:   "notebooks",
		Image:       "jupyter/minimal-notebook:latest",
	})

	resp, err := a.startNotebook(context.Background(), notebook.WorkerStartRequest{
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
	wantEndpoint := "tunnel://worker-1?target=piper-nb-my-notebook.notebooks.svc.cluster.local:8888"
	if resp.Endpoint != wantEndpoint {
		t.Fatalf("endpoint = %q, want %q", resp.Endpoint, wantEndpoint)
	}
	if resp.Token == "" {
		t.Fatal("token is empty")
	}
	if resp.WorkDir != notebook.ContainerWorkDir {
		t.Fatalf("work dir = %q", resp.WorkDir)
	}
	sts, err := client.AppsV1().StatefulSets("notebooks").Get(context.Background(), "piper-nb-my-notebook", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get statefulset: %v", err)
	}
	if sts.Spec.Template.Spec.Containers[0].Image != "jupyter/minimal-notebook:latest" {
		t.Fatalf("image = %q", sts.Spec.Template.Spec.Containers[0].Image)
	}
	wantArgs := notebook.JupyterStartArgs("/notebooks/My Notebook/proxy/", resp.Token, notebook.ContainerWorkDir, 8888)
	gotArgs := sts.Spec.Template.Spec.Containers[0].Args
	for i, want := range wantArgs {
		if gotArgs[i] != want {
			t.Fatalf("args[%d] = %q, want %q", i, gotArgs[i], want)
		}
	}
	if sts.Spec.Template.Spec.Containers[0].VolumeMounts[0].MountPath != notebook.ContainerWorkDir {
		t.Fatalf("mount path = %q", sts.Spec.Template.Spec.Containers[0].VolumeMounts[0].MountPath)
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

func TestNotebookStartPrepWrapsContainerCommand(t *testing.T) {
	client := fake.NewSimpleClientset()
	a := New(Config{
		WorkerID:    "worker-1",
		ClusterName: "gpu-a",
		Client:      client,
		Namespace:   "notebooks",
		Image:       "jupyter/minimal-notebook:latest",
	})

	_, err := a.startNotebook(context.Background(), notebook.WorkerStartRequest{
		YAML: `
metadata:
  name: prep notebook
spec:
  prepare:
    steps:
      - type: command
        backend: k8s
        command: ["sh", "-lc", "echo k8s prep"]
`,
		VolumeID: "vol-prep",
	})
	if err != nil {
		t.Fatalf("startNotebook returned error: %v", err)
	}
	sts, err := client.AppsV1().StatefulSets("notebooks").Get(context.Background(), "piper-nb-prep-notebook", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get statefulset: %v", err)
	}
	got := sts.Spec.Template.Spec.Containers[0]
	if len(got.Command) != 1 || got.Command[0] != "/bin/sh" {
		t.Fatalf("command = %#v", got.Command)
	}
	if len(got.Args) != 2 || got.Args[0] != "-lc" {
		t.Fatalf("args = %#v", got.Args)
	}
	if !strings.Contains(got.Args[1], "echo k8s prep") {
		t.Fatalf("prep script = %q", got.Args[1])
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
	a := New(Config{WorkerID: "worker-1", ClusterName: "gpu-a", Client: client, Namespace: "notebooks"})

	if err := a.stopNotebook(context.Background(), notebook.WorkerStopRequest{Name: "demo"}); err != nil {
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
	a := New(Config{WorkerID: "worker-1", ClusterName: "gpu-a", Client: client, Namespace: "notebooks"})

	if err := a.deprovisionNotebookVolume(context.Background(), notebook.WorkerDeprovisionVolumeRequest{VolumeID: "vol-abc"}); err != nil {
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
		WorkerID:    "worker-1",
		ClusterName: "gpu-a",
		Client:      client,
		Namespace:   "notebooks",
		Image:       "new-image",
	})

	if _, err := a.startNotebook(context.Background(), notebook.WorkerStartRequest{
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
	a := New(Config{WorkerID: "worker-1", ClusterName: "gpu-a", Client: client, Namespace: "notebooks"})

	resp, err := a.syncNotebookStatus(context.Background(), notebook.WorkerSyncStatusRequest{
		Targets: []notebook.WorkerSyncStatusTarget{{Name: "demo"}},
	})
	if err != nil {
		t.Fatalf("syncNotebookStatus returned error: %v", err)
	}
	if resp.Statuses["demo"] != "running" {
		t.Fatalf("status = %q, want running", resp.Statuses["demo"])
	}
}

func TestObserveOnceReportsStateChanges(t *testing.T) {
	replicas := int32(1)
	sts := &appsv1.StatefulSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "piper-nb-demo",
			Namespace: "notebooks",
			Labels: map[string]string{
				"app.kubernetes.io/managed-by": "piper",
				"piper.io/workload-kind":       "notebook",
				"piper.io/workload-id":         "demo",
			},
			Annotations: map[string]string{"piper.io/workload-id": "demo"},
		},
		Spec: appsv1.StatefulSetSpec{Replicas: &replicas},
		Status: appsv1.StatefulSetStatus{
			ReadyReplicas: 1,
		},
	}
	client := fake.NewSimpleClientset(sts)
	var updates []notebook.WorkerStatusUpdate
	a := New(Config{
		Client:    client,
		Namespace: "notebooks",
		ReportStatus: func(update notebook.WorkerStatusUpdate) error {
			updates = append(updates, update)
			return nil
		},
	})

	a.observeOnce(context.Background())
	a.observeOnce(context.Background())
	if len(updates) != 1 || updates[0].Name != "demo" || updates[0].Status != notebook.StatusRunning {
		t.Fatalf("updates = %#v, want one running update", updates)
	}

	current, err := client.AppsV1().StatefulSets("notebooks").Get(context.Background(), sts.Name, metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	zero := int32(0)
	current.Spec.Replicas = &zero
	current.Status.ReadyReplicas = 0
	if _, err := client.AppsV1().StatefulSets("notebooks").Update(context.Background(), current, metav1.UpdateOptions{}); err != nil {
		t.Fatal(err)
	}
	a.observeOnce(context.Background())
	if len(updates) != 2 || updates[1].Status != notebook.StatusStopped {
		t.Fatalf("updates = %#v, want stopped transition", updates)
	}
}
