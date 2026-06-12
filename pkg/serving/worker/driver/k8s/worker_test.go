package servingworker

import (
	"context"
	"testing"

	"github.com/piper/piper/pkg/serving"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

func TestServingDeployCreatesDeploymentAndService(t *testing.T) {
	client := fake.NewSimpleClientset()
	a := New(Config{
		ClusterName: "gpu-a",
		Client:      client,
		Namespace:   "serving",
	})

	resp, err := a.deployServing(context.Background(), servingDeployRequest{
		ProjectID: "project-a",
		S3URI:     "s3://models/demo",
		YAML: `
metadata:
  name: demo
spec:
  model:
    from_uri: s3://models/demo
  run:
    command: ["python", "serve.py"]
    port: 8000
  driver:
    k8s:
      image: model:latest
      resources:
        cpu: "1"
`,
	})
	if err != nil {
		t.Fatalf("deployServing returned error: %v", err)
	}
	if resp.Namespace != "serving" {
		t.Fatalf("namespace = %q", resp.Namespace)
	}
	if resp.Endpoint != "http://project-a--demo.serving.svc.cluster.local:8000" {
		t.Fatalf("endpoint = %q", resp.Endpoint)
	}
	deployment, err := client.AppsV1().Deployments("serving").Get(context.Background(), "project-a--demo", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get deployment: %v", err)
	}
	if deployment.Spec.Template.Spec.Containers[0].Image != "model:latest" {
		t.Fatalf("image = %q", deployment.Spec.Template.Spec.Containers[0].Image)
	}
	if deployment.Labels["piper.io/cluster"] != "gpu-a" {
		t.Fatalf("cluster label = %q", deployment.Labels["piper.io/cluster"])
	}
	if _, err := client.CoreV1().Services("serving").Get(context.Background(), "project-a--demo", metav1.GetOptions{}); err != nil {
		t.Fatalf("get service: %v", err)
	}
}

func TestServingDeployUpdatesExistingDeployment(t *testing.T) {
	replicas := int32(1)
	client := fake.NewSimpleClientset(&appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: "project-a--demo", Namespace: "serving"},
		Spec: appsv1.DeploymentSpec{
			Replicas: &replicas,
			Template: corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{{Name: "serving", Image: "old"}},
				},
			},
		},
	})
	a := New(Config{ClusterName: "gpu-a", Client: client, Namespace: "serving"})

	if _, err := a.deployServing(context.Background(), servingDeployRequest{
		ProjectID: "project-a",
		S3URI:     "s3://models/demo",
		YAML: `
metadata:
  name: demo
spec:
  run:
    command: ["python", "serve.py"]
    port: 8000
  driver:
    k8s:
      image: new
`,
	}); err != nil {
		t.Fatalf("deployServing returned error: %v", err)
	}
	deployment, err := client.AppsV1().Deployments("serving").Get(context.Background(), "project-a--demo", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get deployment: %v", err)
	}
	if deployment.Spec.Template.Spec.Containers[0].Image != "new" {
		t.Fatalf("image = %q, want new", deployment.Spec.Template.Spec.Containers[0].Image)
	}
}

func TestSyncStatusSeparatesProjectsWithSameServiceName(t *testing.T) {
	client := fake.NewSimpleClientset(
		&appsv1.Deployment{
			ObjectMeta: metav1.ObjectMeta{Name: "project-a--demo", Namespace: "serving"},
			Spec:       appsv1.DeploymentSpec{Replicas: int32Ptr(1)},
			Status:     appsv1.DeploymentStatus{ReadyReplicas: 1},
		},
		&appsv1.Deployment{
			ObjectMeta: metav1.ObjectMeta{Name: "project-b--demo", Namespace: "serving"},
			Spec:       appsv1.DeploymentSpec{Replicas: int32Ptr(1)},
		},
	)
	a := New(Config{Client: client, Namespace: "serving"})

	response, err := a.syncStatus(context.Background(), serving.WorkerSyncStatusRequest{
		Services: []serving.WorkerSyncStatusTarget{
			{ProjectID: "project-a", Name: "demo"},
			{ProjectID: "project-b", Name: "demo"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := response.Statuses["project-a:demo"]; got != serving.StatusRunning {
		t.Fatalf("project-a status = %q, want %q", got, serving.StatusRunning)
	}
	if got := response.Statuses["project-b:demo"]; got != serving.StatusStarting {
		t.Fatalf("project-b status = %q, want %q", got, serving.StatusStarting)
	}
}

func int32Ptr(value int32) *int32 {
	return &value
}
