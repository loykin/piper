package k8sdriver

import (
	"context"
	"sync"
	"testing"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/fake"

	"github.com/loykin/piper/pkg/manifest"
	"github.com/loykin/piper/pkg/notebook"
)

type statusReport struct {
	projectID, name, status, endpoint string
}

func newTestDriver(t *testing.T, client kubernetes.Interface, observeInterval time.Duration) (*Driver, chan statusReport) {
	t.Helper()
	reports := make(chan statusReport, 16)
	d, err := New(Config{
		RuntimeID:       "test-k8s-worker",
		Namespaces:      []string{"nb-ns"},
		Client:          client,
		ObserveInterval: observeInterval,
		ReportStatus: func(projectID, name, status, endpoint, _, _ string, _ int, _ string) error {
			reports <- statusReport{projectID, name, status, endpoint}
			return nil
		},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return d, reports
}

func testSpec(projectID, name string) notebook.Notebook {
	return notebook.Notebook{
		Metadata: manifest.ObjectMeta{ProjectID: projectID, Name: name},
		Spec: notebook.NotebookSpec{
			Driver: manifest.DriverSpec{
				K8s: &manifest.DriverK8sSpec{Image: "jupyter/base:latest", Namespace: "nb-ns"},
			},
			Volume: &notebook.VolumeSpec{Size: "1Gi"},
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

func expectNoReport(t *testing.T, reports chan statusReport, within time.Duration) {
	t.Helper()
	select {
	case r := <-reports:
		t.Fatalf("unexpected report: %+v", r)
	case <-time.After(within):
	}
}

func TestNewRejectsMissingConfig(t *testing.T) {
	base := Config{
		RuntimeID:    "w",
		Namespaces:   []string{"ns"},
		Client:       fake.NewSimpleClientset(),
		ReportStatus: func(string, string, string, string, string, string, int, string) error { return nil },
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

func TestProvisionVolumeCreatesPVC(t *testing.T) {
	client := fake.NewSimpleClientset()
	d, _ := newTestDriver(t, client, time.Second)

	vol := &notebook.NotebookVolume{ID: "vol-1"}
	if err := d.ProvisionVolume(context.Background(), vol, testSpec("proj", "nb")); err != nil {
		t.Fatalf("ProvisionVolume: %v", err)
	}
	if vol.WorkDir != notebook.ContainerWorkDir {
		t.Fatalf("WorkDir = %q, want %q", vol.WorkDir, notebook.ContainerWorkDir)
	}
	pvc, err := client.CoreV1().PersistentVolumeClaims("nb-ns").Get(context.Background(), notebookPVCName("vol-1"), metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get pvc: %v", err)
	}
	if pvc.Annotations["piper.io/volume-id"] != "vol-1" {
		t.Fatalf("pvc annotations = %v", pvc.Annotations)
	}
}

func TestProvisionVolumeRejectsDisallowedNamespace(t *testing.T) {
	client := fake.NewSimpleClientset()
	d, _ := newTestDriver(t, client, time.Second)

	spec := testSpec("proj", "nb")
	spec.Spec.Driver.K8s.Namespace = "other-ns"
	vol := &notebook.NotebookVolume{ID: "vol-1"}
	if err := d.ProvisionVolume(context.Background(), vol, spec); err == nil {
		t.Fatal("expected error for disallowed namespace")
	}
}

func TestDeprovisionVolumeDeletesPVC(t *testing.T) {
	client := fake.NewSimpleClientset()
	d, _ := newTestDriver(t, client, time.Second)

	vol := &notebook.NotebookVolume{ID: "vol-1"}
	if err := d.ProvisionVolume(context.Background(), vol, testSpec("proj", "nb")); err != nil {
		t.Fatalf("ProvisionVolume: %v", err)
	}
	if err := d.DeprovisionVolume(context.Background(), vol); err != nil {
		t.Fatalf("DeprovisionVolume: %v", err)
	}
	if _, err := client.CoreV1().PersistentVolumeClaims("nb-ns").Get(context.Background(), notebookPVCName("vol-1"), metav1.GetOptions{}); err == nil {
		t.Fatal("expected pvc to be deleted")
	}
}

func TestStartCreatesStatefulSetAndService(t *testing.T) {
	client := fake.NewSimpleClientset()
	d, _ := newTestDriver(t, client, time.Second)

	vol := &notebook.NotebookVolume{ID: "vol-1", WorkDir: notebook.ContainerWorkDir}
	srv, err := d.Start(context.Background(), testSpec("proj", "nb"), vol, "")
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if srv.RuntimeID != "test-k8s-worker" || srv.Token == "" {
		t.Fatalf("unexpected server: %+v", srv)
	}
	wantEndpoint := "http://" + notebookWorkloadName("proj", "nb") + ".nb-ns.svc.cluster.local:8888"
	if srv.Endpoint != wantEndpoint {
		t.Fatalf("Endpoint = %q, want %q", srv.Endpoint, wantEndpoint)
	}

	name := notebookWorkloadName("proj", "nb")
	sts, err := client.AppsV1().StatefulSets("nb-ns").Get(context.Background(), name, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get statefulset: %v", err)
	}
	if sts.Annotations["piper.io/project-id"] != "proj" {
		t.Fatalf("statefulset annotations = %v", sts.Annotations)
	}
	if _, err := client.CoreV1().Services("nb-ns").Get(context.Background(), name, metav1.GetOptions{}); err != nil {
		t.Fatalf("get service: %v", err)
	}
}

func TestStartRejectsMissingImage(t *testing.T) {
	client := fake.NewSimpleClientset()
	d, _ := newTestDriver(t, client, time.Second)

	spec := testSpec("proj", "nb")
	spec.Spec.Driver.K8s.Image = ""
	vol := &notebook.NotebookVolume{ID: "vol-1", WorkDir: notebook.ContainerWorkDir}
	if _, err := d.Start(context.Background(), spec, vol, ""); err == nil {
		t.Fatal("expected error for missing image")
	}
}

func TestStartRejectsMismatchedPlacementRuntime(t *testing.T) {
	d, _ := newTestDriver(t, fake.NewSimpleClientset(), time.Second)
	spec := testSpec("proj", "nb")
	spec.Spec.Driver.Placement.Runtime = "docker"
	vol := &notebook.NotebookVolume{ID: "vol-1", WorkDir: notebook.ContainerWorkDir}
	if _, err := d.Start(context.Background(), spec, vol, ""); err == nil {
		t.Fatal("expected placement.runtime mismatch rejection")
	}
}

func TestStopScalesReplicasToZero(t *testing.T) {
	client := fake.NewSimpleClientset()
	d, _ := newTestDriver(t, client, time.Second)

	vol := &notebook.NotebookVolume{ID: "vol-1", WorkDir: notebook.ContainerWorkDir}
	if _, err := d.Start(context.Background(), testSpec("proj", "nb"), vol, ""); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if err := d.Stop(context.Background(), &notebook.NotebookServer{ProjectID: "proj", Name: "nb"}); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	name := notebookWorkloadName("proj", "nb")
	sts, err := client.AppsV1().StatefulSets("nb-ns").Get(context.Background(), name, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get statefulset: %v", err)
	}
	if sts.Spec.Replicas == nil || *sts.Spec.Replicas != 0 {
		t.Fatalf("replicas = %v, want 0", sts.Spec.Replicas)
	}
}

func TestStopUnknownNotebookIsNotError(t *testing.T) {
	d, _ := newTestDriver(t, fake.NewSimpleClientset(), time.Second)
	if err := d.Stop(context.Background(), &notebook.NotebookServer{ProjectID: "proj", Name: "missing"}); err != nil {
		t.Fatalf("Stop on unknown notebook: %v", err)
	}
}

func TestObserveReportsRunningOnceReady(t *testing.T) {
	client := fake.NewSimpleClientset()
	d, reports := newTestDriver(t, client, 30*time.Millisecond)

	vol := &notebook.NotebookVolume{ID: "vol-1", WorkDir: notebook.ContainerWorkDir}
	if _, err := d.Start(context.Background(), testSpec("proj", "nb"), vol, ""); err != nil {
		t.Fatalf("Start: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		d.Observe(ctx)
	}()
	t.Cleanup(func() { cancel(); wg.Wait() })

	// First tick observes desired=1, readyReplicas=0 -> starting.
	r := awaitReport(t, reports)
	if r.status != notebook.StatusStarting {
		t.Fatalf("status = %q, want starting", r.status)
	}

	// Simulate the pod becoming ready.
	name := notebookWorkloadName("proj", "nb")
	setReadyReplicas(t, client, "nb-ns", name, 1)

	r = awaitReport(t, reports)
	if r.status != notebook.StatusRunning {
		t.Fatalf("status = %q, want running", r.status)
	}
	if r.projectID != "proj" || r.name != "nb" {
		t.Fatalf("unexpected report target: %+v", r)
	}

	// No further report until status actually changes again.
	expectNoReport(t, reports, 150*time.Millisecond)
}

func setReadyReplicas(t *testing.T, client kubernetes.Interface, ns, name string, ready int32) {
	t.Helper()
	sts, err := client.AppsV1().StatefulSets(ns).Get(context.Background(), name, metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	sts.Status.ReadyReplicas = ready
	if _, err := client.AppsV1().StatefulSets(ns).UpdateStatus(context.Background(), sts, metav1.UpdateOptions{}); err != nil {
		t.Fatal(err)
	}
}

func TestObservedStatefulSetStatus(t *testing.T) {
	one := int32(1)
	zero := int32(0)
	cases := []struct {
		name string
		sts  *appsv1.StatefulSet
		want string
	}{
		{"stopped", &appsv1.StatefulSet{Spec: appsv1.StatefulSetSpec{Replicas: &zero}}, notebook.StatusStopped},
		{"starting", &appsv1.StatefulSet{Spec: appsv1.StatefulSetSpec{Replicas: &one}}, notebook.StatusStarting},
		{"running", &appsv1.StatefulSet{
			Spec:   appsv1.StatefulSetSpec{Replicas: &one},
			Status: appsv1.StatefulSetStatus{ReadyReplicas: 1},
		}, notebook.StatusRunning},
		{"failed", &appsv1.StatefulSet{
			Spec: appsv1.StatefulSetSpec{Replicas: &one},
			Status: appsv1.StatefulSetStatus{Conditions: []appsv1.StatefulSetCondition{
				{Type: "ReplicaFailure", Status: corev1.ConditionTrue},
			}},
		}, notebook.StatusFailed},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := observedStatefulSetStatus(tc.sts); got != tc.want {
				t.Fatalf("observedStatefulSetStatus = %q, want %q", got, tc.want)
			}
		})
	}
}
