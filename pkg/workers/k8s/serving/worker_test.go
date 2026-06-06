package servingworker

import (
	"context"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"

	"github.com/piper/piper/pkg/serving"
)

func TestServingDeployCreatesDeploymentAndService(t *testing.T) {
	client := fake.NewSimpleClientset()
	a := New(Config{
		ClusterName: "gpu-a",
		Client:      client,
		Namespace:   "serving",
	})

	resp, err := a.deployServing(context.Background(), servingDeployRequest{
		S3URI: "s3://models/demo",
		YAML: `
metadata:
  name: demo
spec:
  model:
    from_uri: s3://models/demo
  runtime:
    image: model:latest
    command: ["python", "serve.py"]
    port: 8000
  k8s:
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
	if resp.Endpoint != "http://demo.serving.svc.cluster.local:8000" {
		t.Fatalf("endpoint = %q", resp.Endpoint)
	}
	deployment, err := client.AppsV1().Deployments("serving").Get(context.Background(), "demo", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get deployment: %v", err)
	}
	if deployment.Spec.Template.Spec.Containers[0].Image != "model:latest" {
		t.Fatalf("image = %q", deployment.Spec.Template.Spec.Containers[0].Image)
	}
	if deployment.Labels["piper.io/cluster"] != "gpu-a" {
		t.Fatalf("cluster label = %q", deployment.Labels["piper.io/cluster"])
	}
	if _, err := client.CoreV1().Services("serving").Get(context.Background(), "demo", metav1.GetOptions{}); err != nil {
		t.Fatalf("get service: %v", err)
	}
}

func TestServingDeployUpdatesExistingDeployment(t *testing.T) {
	replicas := int32(1)
	client := fake.NewSimpleClientset(&appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: "demo", Namespace: "serving"},
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
		S3URI: "s3://models/demo",
		YAML: `
metadata:
  name: demo
spec:
  runtime:
    image: new
    command: ["python", "serve.py"]
    port: 8000
`,
	}); err != nil {
		t.Fatalf("deployServing returned error: %v", err)
	}
	deployment, err := client.AppsV1().Deployments("serving").Get(context.Background(), "demo", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get deployment: %v", err)
	}
	if deployment.Spec.Template.Spec.Containers[0].Image != "new" {
		t.Fatalf("image = %q, want new", deployment.Spec.Template.Spec.Containers[0].Image)
	}
}

func TestServingStopDeletesDeploymentAndService(t *testing.T) {
	client := fake.NewSimpleClientset(
		&appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: "demo", Namespace: "serving"}},
		&corev1.Service{ObjectMeta: metav1.ObjectMeta{Name: "demo", Namespace: "serving"}},
	)
	a := New(Config{ClusterName: "gpu-a", Client: client})

	if err := a.stopServing(context.Background(), servingStopRequest{Name: "demo", Namespace: "serving"}); err != nil {
		t.Fatalf("stopServing returned error: %v", err)
	}
	if _, err := client.AppsV1().Deployments("serving").Get(context.Background(), "demo", metav1.GetOptions{}); err == nil {
		t.Fatal("expected deployment to be deleted")
	}
	if _, err := client.CoreV1().Services("serving").Get(context.Background(), "demo", metav1.GetOptions{}); err == nil {
		t.Fatal("expected service to be deleted")
	}
}

func TestObserveOnceReportsDeploymentTransitions(t *testing.T) {
	replicas := int32(1)
	deployment := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "demo",
			Namespace: "serving",
			Labels: map[string]string{
				"app.kubernetes.io/managed-by": "piper",
				"piper.io/workload-kind":       "serving",
				"piper.io/workload-id":         "demo",
			},
			Annotations: map[string]string{"piper.io/workload-id": "demo"},
		},
		Spec: appsv1.DeploymentSpec{Replicas: &replicas},
	}
	client := fake.NewSimpleClientset(deployment)
	var updates []serving.WorkerStatusUpdate
	a := New(Config{
		Client:    client,
		Namespace: "serving",
		ReportStatus: func(update serving.WorkerStatusUpdate) error {
			updates = append(updates, update)
			return nil
		},
	})

	a.observeOnce(context.Background())
	if len(updates) != 1 || updates[0].Status != serving.StatusStarting {
		t.Fatalf("updates = %#v, want starting", updates)
	}
	current, err := client.AppsV1().Deployments("serving").Get(context.Background(), "demo", metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	current.Status.ReadyReplicas = 1
	if _, err := client.AppsV1().Deployments("serving").Update(context.Background(), current, metav1.UpdateOptions{}); err != nil {
		t.Fatal(err)
	}
	a.observeOnce(context.Background())
	if len(updates) != 2 || updates[1].Status != serving.StatusRunning {
		t.Fatalf("updates = %#v, want running transition", updates)
	}
}
