package k8sdriver

import (
	"context"
	"testing"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/fake"

	"github.com/loykin/piper/internal/artifact"
	"github.com/loykin/piper/pkg/manifest"
	"github.com/loykin/piper/pkg/serving"
)

type statusReport struct {
	projectID, name, status, endpoint string
}

func newTestDriver(t *testing.T, client kubernetes.Interface, observeInterval time.Duration) (*Driver, chan statusReport) {
	t.Helper()
	reports := make(chan statusReport, 16)
	d, err := New(Config{
		RuntimeID:            "test-k8s-worker",
		Namespaces:           []string{"svc-ns"},
		Client:               client,
		ArtifactFetcherImage: "piper:test",
		ObserveInterval:      observeInterval,
		ReportStatus: func(projectID, name, status, endpoint string) error {
			reports <- statusReport{projectID, name, status, endpoint}
			return nil
		},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return d, reports
}

func testSpec(projectID, name string) serving.ModelService {
	return serving.ModelService{
		Metadata: manifest.ObjectMeta{ProjectID: projectID, Name: name},
		Spec: serving.ModelServiceSpec{
			Run: serving.ModelServiceRun{Command: []string{"serve"}, Port: 8080},
			Driver: manifest.DriverSpec{
				K8s: &manifest.DriverK8sSpec{Image: "model-server:latest", Namespace: "svc-ns"},
			},
		},
	}
}

func awaitReport(t *testing.T, reports chan statusReport) statusReport {
	t.Helper()
	select {
	case r := <-reports:
		return r
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for status report")
		return statusReport{}
	}
}

func TestNewRejectsMissingConfig(t *testing.T) {
	base := Config{
		RuntimeID:    "w",
		Namespaces:   []string{"ns"},
		Client:       fake.NewSimpleClientset(),
		ReportStatus: func(string, string, string, string) error { return nil },
	}
	cases := []struct {
		name string
		mut  func(*Config)
	}{
		{"worker id", func(c *Config) { c.RuntimeID = "" }},
		{"client", func(c *Config) { c.Client = nil }},
		{"namespaces", func(c *Config) { c.Namespaces = nil }},
		{"report status", func(c *Config) { c.ReportStatus = nil }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := base
			tc.mut(&cfg)
			if _, err := New(cfg); err == nil {
				t.Fatalf("expected error when %s missing", tc.name)
			}
		})
	}
}

func TestDeployWithRemoteURIArtifactCreatesDeploymentAndService(t *testing.T) {
	client := fake.NewSimpleClientset()
	d, _ := newTestDriver(t, client, time.Second)

	art := artifact.Resolved{RemoteURI: "http://models.example.com/model.tar"}
	svc, err := d.Deploy(context.Background(), testSpec("proj", "svc"), art, "yaml-body")
	if err != nil {
		t.Fatalf("Deploy: %v", err)
	}
	if svc.Status != serving.StatusStarting || svc.RuntimeID != "test-k8s-worker" || svc.YAML != "yaml-body" {
		t.Fatalf("unexpected service: %+v", svc)
	}
	wantEndpoint := "http://" + servingResourceName("proj", "svc") + ".svc-ns.svc.cluster.local:8080"
	if svc.Endpoint != wantEndpoint {
		t.Fatalf("Endpoint = %q, want %q", svc.Endpoint, wantEndpoint)
	}

	name := servingResourceName("proj", "svc")
	dep, err := client.AppsV1().Deployments("svc-ns").Get(context.Background(), name, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get deployment: %v", err)
	}
	if dep.Annotations["piper.io/project-id"] != "proj" {
		t.Fatalf("deployment annotations = %v", dep.Annotations)
	}
	if len(dep.Spec.Template.Spec.InitContainers) != 0 {
		t.Fatalf("expected no init container for a RemoteURI (non-key) artifact")
	}
	if _, err := client.CoreV1().Services("svc-ns").Get(context.Background(), name, metav1.GetOptions{}); err != nil {
		t.Fatalf("get service: %v", err)
	}
}

func TestDeployWithStoredArtifactCreatesSecretAndInitContainer(t *testing.T) {
	client := fake.NewSimpleClientset()
	d, _ := newTestDriver(t, client, time.Second)
	d.WithStorage("s3://bucket", "storage-token-value")

	art := artifact.Resolved{ArtifactKey: "run-1/step/model", S3URI: "s3://bucket/run-1/step/model"}
	if _, err := d.Deploy(context.Background(), testSpec("proj", "svc"), art, ""); err != nil {
		t.Fatalf("Deploy: %v", err)
	}

	name := servingResourceName("proj", "svc")
	dep, err := client.AppsV1().Deployments("svc-ns").Get(context.Background(), name, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get deployment: %v", err)
	}
	if len(dep.Spec.Template.Spec.InitContainers) != 1 {
		t.Fatalf("expected an artifact-download init container, got %d", len(dep.Spec.Template.Spec.InitContainers))
	}
	if dep.Spec.Template.Spec.InitContainers[0].Image != "piper:test" {
		t.Fatalf("init container image = %q, want piper:test", dep.Spec.Template.Spec.InitContainers[0].Image)
	}

	secret, err := client.CoreV1().Secrets("svc-ns").Get(context.Background(), name+"-artifact", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get artifact secret: %v", err)
	}
	if secret.StringData["storage-url"] != "s3://bucket" || secret.StringData["storage-token"] != "storage-token-value" {
		t.Fatalf("unexpected secret data: %+v", secret.StringData)
	}
	if secret.StringData["artifact-key"] != "run-1/step/model" {
		t.Fatalf("artifact-key = %q", secret.StringData["artifact-key"])
	}
}

func TestDeployRequiresArtifactFetcherImageForStoredArtifact(t *testing.T) {
	client := fake.NewSimpleClientset()
	d, err := New(Config{
		RuntimeID:    "w",
		Namespaces:   []string{"svc-ns"},
		Client:       client,
		ReportStatus: func(string, string, string, string) error { return nil },
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	d.WithStorage("s3://bucket", "tok")

	art := artifact.Resolved{ArtifactKey: "k"}
	if _, err := d.Deploy(context.Background(), testSpec("proj", "svc"), art, ""); err == nil {
		t.Fatal("expected error when ArtifactFetcherImage is unset for a stored artifact")
	}
}

func TestDeployRejectsMissingArtifactLocation(t *testing.T) {
	d, _ := newTestDriver(t, fake.NewSimpleClientset(), time.Second)
	if _, err := d.Deploy(context.Background(), testSpec("proj", "svc"), artifact.Resolved{}, ""); err == nil {
		t.Fatal("expected error for missing artifact location")
	}
}

func TestDeployRejectsDisallowedNamespace(t *testing.T) {
	d, _ := newTestDriver(t, fake.NewSimpleClientset(), time.Second)
	spec := testSpec("proj", "svc")
	spec.Spec.Driver.K8s.Namespace = "other-ns"
	art := artifact.Resolved{RemoteURI: "http://x"}
	if _, err := d.Deploy(context.Background(), spec, art, ""); err == nil {
		t.Fatal("expected error for disallowed namespace")
	}
}

func TestDeployRejectsMismatchedPlacementRuntime(t *testing.T) {
	d, _ := newTestDriver(t, fake.NewSimpleClientset(), time.Second)
	spec := testSpec("proj", "svc")
	spec.Spec.Driver.Placement.Runtime = "docker"
	art := artifact.Resolved{RemoteURI: "http://x"}
	if _, err := d.Deploy(context.Background(), spec, art, ""); err == nil {
		t.Fatal("expected placement.runtime mismatch rejection")
	}
}

func TestStopDeletesDeploymentServiceAndSecret(t *testing.T) {
	client := fake.NewSimpleClientset()
	d, _ := newTestDriver(t, client, time.Second)
	d.WithStorage("s3://bucket", "tok")

	art := artifact.Resolved{ArtifactKey: "k", S3URI: "s3://bucket/k"}
	if _, err := d.Deploy(context.Background(), testSpec("proj", "svc"), art, ""); err != nil {
		t.Fatalf("Deploy: %v", err)
	}

	if err := d.Stop(context.Background(), &serving.Service{ProjectID: "proj", Name: "svc", Namespace: "svc-ns"}); err != nil {
		t.Fatalf("Stop: %v", err)
	}

	name := servingResourceName("proj", "svc")
	if _, err := client.AppsV1().Deployments("svc-ns").Get(context.Background(), name, metav1.GetOptions{}); err == nil {
		t.Fatal("expected deployment to be deleted")
	}
	if _, err := client.CoreV1().Services("svc-ns").Get(context.Background(), name, metav1.GetOptions{}); err == nil {
		t.Fatal("expected service to be deleted")
	}
	if _, err := client.CoreV1().Secrets("svc-ns").Get(context.Background(), name+"-artifact", metav1.GetOptions{}); err == nil {
		t.Fatal("expected secret to be deleted")
	}
}

func TestStopUnknownServiceIsNotError(t *testing.T) {
	d, _ := newTestDriver(t, fake.NewSimpleClientset(), time.Second)
	if err := d.Stop(context.Background(), &serving.Service{ProjectID: "proj", Name: "missing", Namespace: "svc-ns"}); err != nil {
		t.Fatalf("Stop on unknown service: %v", err)
	}
}

func TestObserveReportsRunningOnceReady(t *testing.T) {
	client := fake.NewSimpleClientset()
	d, reports := newTestDriver(t, client, 30*time.Millisecond)

	art := artifact.Resolved{RemoteURI: "http://x"}
	if _, err := d.Deploy(context.Background(), testSpec("proj", "svc"), art, ""); err != nil {
		t.Fatalf("Deploy: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		d.Observe(ctx)
	}()
	t.Cleanup(func() { cancel(); <-done })

	r := awaitReport(t, reports)
	if r.status != serving.StatusStarting {
		t.Fatalf("status = %q, want starting", r.status)
	}

	name := servingResourceName("proj", "svc")
	dep, err := client.AppsV1().Deployments("svc-ns").Get(context.Background(), name, metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	dep.Status.ReadyReplicas = 1
	if _, err := client.AppsV1().Deployments("svc-ns").UpdateStatus(context.Background(), dep, metav1.UpdateOptions{}); err != nil {
		t.Fatal(err)
	}

	r = awaitReport(t, reports)
	if r.status != serving.StatusRunning {
		t.Fatalf("status = %q, want running", r.status)
	}
	if r.projectID != "proj" || r.name != "svc" {
		t.Fatalf("unexpected report target: %+v", r)
	}
}

func TestObservedDeploymentStatus(t *testing.T) {
	one := int32(1)
	zero := int32(0)
	cases := []struct {
		name string
		dep  *appsv1.Deployment
		want string
	}{
		{"stopped", &appsv1.Deployment{Spec: appsv1.DeploymentSpec{Replicas: &zero}}, serving.StatusStopped},
		{"starting", &appsv1.Deployment{Spec: appsv1.DeploymentSpec{Replicas: &one}}, serving.StatusStarting},
		{"running", &appsv1.Deployment{
			Spec:   appsv1.DeploymentSpec{Replicas: &one},
			Status: appsv1.DeploymentStatus{ReadyReplicas: 1},
		}, serving.StatusRunning},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := observedDeploymentStatus(tc.dep); got != tc.want {
				t.Fatalf("observedDeploymentStatus = %q, want %q", got, tc.want)
			}
		})
	}
}
