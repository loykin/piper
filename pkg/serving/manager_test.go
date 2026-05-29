package serving

import (
	"context"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

func TestStopK8sUsesPersistedNamespace(t *testing.T) {
	ctx := context.Background()
	name := "fraud-detector"
	kname := k8sName(name)
	clientset := fake.NewSimpleClientset(
		&appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: kname, Namespace: "ml-prod"}},
		&corev1.Service{ObjectMeta: metav1.ObjectMeta{Name: kname, Namespace: "ml-prod"}},
		&appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: kname, Namespace: "wrong-ns"}},
		&corev1.Service{ObjectMeta: metav1.ObjectMeta{Name: kname, Namespace: "wrong-ns"}},
	)
	repo := newStubServingRepo(&Service{
		Name:      name,
		Status:    StatusRunning,
		Endpoint:  "http://fraud-detector.wrong-ns.svc.cluster.local:8000",
		Namespace: "ml-prod",
	})
	manager := New(repo, "", clientset)

	if err := manager.Stop(ctx, name); err != nil {
		t.Fatal(err)
	}
	if _, err := clientset.AppsV1().Deployments("ml-prod").Get(ctx, kname, metav1.GetOptions{}); err == nil {
		t.Fatal("deployment in persisted namespace still exists")
	}
	if _, err := clientset.CoreV1().Services("ml-prod").Get(ctx, kname, metav1.GetOptions{}); err == nil {
		t.Fatal("service in persisted namespace still exists")
	}
	if _, err := clientset.AppsV1().Deployments("wrong-ns").Get(ctx, kname, metav1.GetOptions{}); err != nil {
		t.Fatalf("deployment in endpoint-derived namespace was deleted: %v", err)
	}
	if repo.services[name].Status != StatusStopped {
		t.Fatalf("status = %q, want %q", repo.services[name].Status, StatusStopped)
	}
}
