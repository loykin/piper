package notebook

import (
	"context"
	"testing"

	"github.com/piper/piper/pkg/pipeline"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

// ─── helpers ──────────────────────────────────────────────────────────────────

func newTestK8sDriver(cfg K8sDriverConfig) (*K8sDriver, *fakeRepo) {
	repo := newFakeRepo()
	client := fake.NewSimpleClientset()
	return NewK8sDriver(client, cfg, repo), repo
}

func defaultCfg() K8sDriverConfig {
	return K8sDriverConfig{
		Namespace:   "test-ns",
		WorkerImage: "jupyter/scipy-notebook:latest",
		StorageSize: "5Gi",
	}
}

func newVol() *NotebookVolume {
	return &NotebookVolume{ID: "aaaabbbbccccdddd"}
}

// ─── ProvisionVolume ──────────────────────────────────────────────────────────

func TestK8sDriver_ProvisionVolume_CreatesPVC(t *testing.T) {
	d, _ := newTestK8sDriver(defaultCfg())
	vol := newVol()

	if err := d.ProvisionVolume(context.Background(), vol, ""); err != nil {
		t.Fatalf("ProvisionVolume: %v", err)
	}

	pvcs, _ := d.k8s.CoreV1().PersistentVolumeClaims("test-ns").List(context.Background(), metav1.ListOptions{})
	if len(pvcs.Items) != 1 {
		t.Fatalf("expected 1 PVC, got %d", len(pvcs.Items))
	}
	pvc := pvcs.Items[0]
	qty := pvc.Spec.Resources.Requests[corev1.ResourceStorage]
	if qty.Cmp(resource.MustParse("5Gi")) != 0 {
		t.Errorf("PVC size = %s, want 5Gi", qty.String())
	}
}

func TestK8sDriver_ProvisionVolume_StorageSizeOverride(t *testing.T) {
	d, _ := newTestK8sDriver(defaultCfg())
	vol := newVol()

	if err := d.ProvisionVolume(context.Background(), vol, "20Gi"); err != nil {
		t.Fatalf("ProvisionVolume: %v", err)
	}

	pvcs, _ := d.k8s.CoreV1().PersistentVolumeClaims("test-ns").List(context.Background(), metav1.ListOptions{})
	qty := pvcs.Items[0].Spec.Resources.Requests[corev1.ResourceStorage]
	if qty.Cmp(resource.MustParse("20Gi")) != 0 {
		t.Errorf("PVC size = %s, want 20Gi (override)", qty.String())
	}
}

func TestK8sDriver_ProvisionVolume_SetsWorkDir(t *testing.T) {
	d, _ := newTestK8sDriver(defaultCfg())
	vol := newVol()

	_ = d.ProvisionVolume(context.Background(), vol, "")

	if vol.WorkDir != "/home/jovyan" {
		t.Errorf("WorkDir = %q, want /home/jovyan", vol.WorkDir)
	}
	if vol.WorkerID != "" {
		t.Errorf("WorkerID should be empty in K8s mode, got %q", vol.WorkerID)
	}
}

func TestK8sDriver_ProvisionVolume_InvalidSize(t *testing.T) {
	d, _ := newTestK8sDriver(defaultCfg())
	vol := newVol()

	err := d.ProvisionVolume(context.Background(), vol, "not-a-size")
	if err == nil {
		t.Fatal("expected error for invalid storage size")
	}
}

func TestK8sDriver_ProvisionVolume_Idempotent(t *testing.T) {
	d, _ := newTestK8sDriver(defaultCfg())
	vol := newVol()

	_ = d.ProvisionVolume(context.Background(), vol, "")
	// Second call should not error (PVC already exists).
	if err := d.ProvisionVolume(context.Background(), vol, ""); err != nil {
		t.Fatalf("second ProvisionVolume: %v", err)
	}
}

// ─── Start ────────────────────────────────────────────────────────────────────

func TestK8sDriver_Start_CreatesStatefulSetAndService(t *testing.T) {
	d, _ := newTestK8sDriver(defaultCfg())
	vol := &NotebookVolume{ID: "aaaabbbbccccdddd", WorkDir: "/home/jovyan"}

	spec := NotebookServerSpec{}
	spec.Metadata.Name = "my-nb"

	nb, err := d.Start(context.Background(), spec, vol, "")
	if err != nil {
		t.Fatalf("Start: %v", err)
	}

	if nb.Status != StatusStarting {
		t.Errorf("status = %s, want starting", nb.Status)
	}
	if nb.Token == "" {
		t.Error("Token should be set")
	}

	stsList, _ := d.k8s.AppsV1().StatefulSets("test-ns").List(context.Background(), metav1.ListOptions{})
	if len(stsList.Items) != 1 {
		t.Fatalf("expected 1 StatefulSet, got %d", len(stsList.Items))
	}

	svcList, _ := d.k8s.CoreV1().Services("test-ns").List(context.Background(), metav1.ListOptions{})
	if len(svcList.Items) != 1 {
		t.Fatalf("expected 1 Service, got %d", len(svcList.Items))
	}
}

func TestK8sDriver_Start_UsesSpecImage(t *testing.T) {
	d, _ := newTestK8sDriver(defaultCfg())
	vol := &NotebookVolume{ID: "aaaabbbbccccdddd", WorkDir: "/home/jovyan"}

	spec := NotebookServerSpec{}
	spec.Metadata.Name = "my-nb"
	spec.Spec.Image = "custom/image:v1"

	_, err := d.Start(context.Background(), spec, vol, "")
	if err != nil {
		t.Fatalf("Start: %v", err)
	}

	sts, _ := d.k8s.AppsV1().StatefulSets("test-ns").Get(context.Background(), d.statefulSetName("my-nb"), metav1.GetOptions{})
	got := sts.Spec.Template.Spec.Containers[0].Image
	if got != "custom/image:v1" {
		t.Errorf("container image = %q, want custom/image:v1", got)
	}
}

func TestK8sDriver_Start_FallsBackToConfigImage(t *testing.T) {
	d, _ := newTestK8sDriver(defaultCfg())
	vol := &NotebookVolume{ID: "aaaabbbbccccdddd", WorkDir: "/home/jovyan"}

	spec := NotebookServerSpec{}
	spec.Metadata.Name = "my-nb"
	// spec.Spec.Image is empty → should use cfg.WorkerImage

	_, _ = d.Start(context.Background(), spec, vol, "")

	sts, _ := d.k8s.AppsV1().StatefulSets("test-ns").Get(context.Background(), d.statefulSetName("my-nb"), metav1.GetOptions{})
	got := sts.Spec.Template.Spec.Containers[0].Image
	if got != defaultCfg().WorkerImage {
		t.Errorf("container image = %q, want %s", got, defaultCfg().WorkerImage)
	}
}

func TestK8sDriver_Start_AppliesResources(t *testing.T) {
	d, _ := newTestK8sDriver(defaultCfg())
	vol := &NotebookVolume{ID: "aaaabbbbccccdddd", WorkDir: "/home/jovyan"}

	spec := NotebookServerSpec{}
	spec.Metadata.Name = "my-nb"
	spec.Spec.Resources = pipeline.Resources{CPU: "2", Memory: "4Gi"}

	_, _ = d.Start(context.Background(), spec, vol, "")

	sts, _ := d.k8s.AppsV1().StatefulSets("test-ns").Get(context.Background(), d.statefulSetName("my-nb"), metav1.GetOptions{})
	limits := sts.Spec.Template.Spec.Containers[0].Resources.Limits
	if limits.Cpu().Cmp(resource.MustParse("2")) != 0 {
		t.Errorf("CPU limit = %s, want 2", limits.Cpu())
	}
	if limits.Memory().Cmp(resource.MustParse("4Gi")) != 0 {
		t.Errorf("memory limit = %s, want 4Gi", limits.Memory())
	}
}

func TestK8sDriver_Start_PodDefaultsApplied(t *testing.T) {
	cfg := defaultCfg()
	cfg.PodDefaults.NodeSelector = map[string]string{"gpu": "true"}
	d, _ := newTestK8sDriver(cfg)
	vol := &NotebookVolume{ID: "aaaabbbbccccdddd", WorkDir: "/home/jovyan"}

	spec := NotebookServerSpec{}
	spec.Metadata.Name = "my-nb"

	_, _ = d.Start(context.Background(), spec, vol, "")

	sts, _ := d.k8s.AppsV1().StatefulSets("test-ns").Get(context.Background(), d.statefulSetName("my-nb"), metav1.GetOptions{})
	if sts.Spec.Template.Spec.NodeSelector["gpu"] != "true" {
		t.Errorf("NodeSelector not applied from PodDefaults")
	}
}

func TestK8sDriver_Start_Restart_PatchesReplicas(t *testing.T) {
	d, _ := newTestK8sDriver(defaultCfg())
	vol := &NotebookVolume{ID: "aaaabbbbccccdddd", WorkDir: "/home/jovyan"}

	spec := NotebookServerSpec{}
	spec.Metadata.Name = "my-nb"

	// First start.
	_, _ = d.Start(context.Background(), spec, vol, "")

	// Simulate stop (replicas = 0).
	safeName := d.statefulSetName("my-nb")
	sts, _ := d.k8s.AppsV1().StatefulSets("test-ns").Get(context.Background(), safeName, metav1.GetOptions{})
	zero := int32(0)
	sts.Spec.Replicas = &zero
	_, _ = d.k8s.AppsV1().StatefulSets("test-ns").Update(context.Background(), sts, metav1.UpdateOptions{})

	// Restart (Start on existing StatefulSet).
	_, err := d.Start(context.Background(), spec, vol, "")
	if err != nil {
		t.Fatalf("restart Start: %v", err)
	}

	updated, _ := d.k8s.AppsV1().StatefulSets("test-ns").Get(context.Background(), safeName, metav1.GetOptions{})
	if *updated.Spec.Replicas != 1 {
		t.Errorf("replicas after restart = %d, want 1", *updated.Spec.Replicas)
	}
}

// ─── Stop ─────────────────────────────────────────────────────────────────────

func TestK8sDriver_Stop_ScalesToZero(t *testing.T) {
	d, _ := newTestK8sDriver(defaultCfg())
	vol := &NotebookVolume{ID: "aaaabbbbccccdddd", WorkDir: "/home/jovyan"}

	spec := NotebookServerSpec{}
	spec.Metadata.Name = "my-nb"
	nb, _ := d.Start(context.Background(), spec, vol, "")

	if err := d.Stop(context.Background(), nb); err != nil {
		t.Fatalf("Stop: %v", err)
	}

	sts, _ := d.k8s.AppsV1().StatefulSets("test-ns").Get(context.Background(), d.statefulSetName("my-nb"), metav1.GetOptions{})
	if *sts.Spec.Replicas != 0 {
		t.Errorf("replicas after Stop = %d, want 0", *sts.Spec.Replicas)
	}
}

func TestK8sDriver_Stop_NotFoundIsNoop(t *testing.T) {
	d, _ := newTestK8sDriver(defaultCfg())
	nb := &NotebookServer{Name: "ghost"}
	if err := d.Stop(context.Background(), nb); err != nil {
		t.Fatalf("Stop on missing StatefulSet should be noop: %v", err)
	}
}

// ─── DeprovisionVolume ────────────────────────────────────────────────────────

func TestK8sDriver_DeprovisionVolume_DeletesPVC(t *testing.T) {
	d, _ := newTestK8sDriver(defaultCfg())
	vol := newVol()

	_ = d.ProvisionVolume(context.Background(), vol, "")

	if err := d.DeprovisionVolume(context.Background(), vol); err != nil {
		t.Fatalf("DeprovisionVolume: %v", err)
	}

	pvcs, _ := d.k8s.CoreV1().PersistentVolumeClaims("test-ns").List(context.Background(), metav1.ListOptions{})
	if len(pvcs.Items) != 0 {
		t.Errorf("expected 0 PVCs after deprovision, got %d", len(pvcs.Items))
	}
}

func TestK8sDriver_DeprovisionVolume_NotFoundIsNoop(t *testing.T) {
	d, _ := newTestK8sDriver(defaultCfg())
	vol := newVol()
	if err := d.DeprovisionVolume(context.Background(), vol); err != nil {
		t.Fatalf("DeprovisionVolume on missing PVC should be noop: %v", err)
	}
}

// ─── SyncStatus ───────────────────────────────────────────────────────────────

func TestK8sDriver_SyncStatus_StartingToRunning(t *testing.T) {
	d, repo := newTestK8sDriver(defaultCfg())
	vol := &NotebookVolume{ID: "aaaabbbbccccdddd", WorkDir: "/home/jovyan"}

	spec := NotebookServerSpec{}
	spec.Metadata.Name = "my-nb"
	_, _ = d.Start(context.Background(), spec, vol, "")

	// Seed repo with a starting server.
	nb := &NotebookServer{Name: "my-nb", Status: StatusStarting}
	_ = repo.Create(context.Background(), nb)

	// Simulate pod becoming ready: patch readyReplicas.
	safeName := d.statefulSetName("my-nb")
	sts, _ := d.k8s.AppsV1().StatefulSets("test-ns").Get(context.Background(), safeName, metav1.GetOptions{})
	sts.Status.ReadyReplicas = 1
	_, _ = d.k8s.AppsV1().StatefulSets("test-ns").UpdateStatus(context.Background(), sts, metav1.UpdateOptions{})

	if err := d.SyncStatus(context.Background(), []*NotebookServer{nb}); err != nil {
		t.Fatalf("SyncStatus: %v", err)
	}

	updated, _ := repo.Get(context.Background(), "my-nb")
	if updated.Status != StatusRunning {
		t.Errorf("status = %s, want running", updated.Status)
	}
}

func TestK8sDriver_SyncStatus_RunningToStopped(t *testing.T) {
	d, repo := newTestK8sDriver(defaultCfg())
	vol := &NotebookVolume{ID: "aaaabbbbccccdddd", WorkDir: "/home/jovyan"}

	spec := NotebookServerSpec{}
	spec.Metadata.Name = "my-nb"
	_, _ = d.Start(context.Background(), spec, vol, "")

	nb := &NotebookServer{Name: "my-nb", Status: StatusRunning}
	_ = repo.Create(context.Background(), nb)

	// Scale to zero.
	safeName := d.statefulSetName("my-nb")
	sts, _ := d.k8s.AppsV1().StatefulSets("test-ns").Get(context.Background(), safeName, metav1.GetOptions{})
	zero := int32(0)
	sts.Spec.Replicas = &zero
	sts.Status.ReadyReplicas = 0
	_, _ = d.k8s.AppsV1().StatefulSets("test-ns").Update(context.Background(), sts, metav1.UpdateOptions{})
	_, _ = d.k8s.AppsV1().StatefulSets("test-ns").UpdateStatus(context.Background(), sts, metav1.UpdateOptions{})

	_ = d.SyncStatus(context.Background(), []*NotebookServer{nb})

	updated, _ := repo.Get(context.Background(), "my-nb")
	if updated.Status != StatusStopped {
		t.Errorf("status = %s, want stopped", updated.Status)
	}
}

func TestK8sDriver_SyncStatus_MissingStatefulSetSetsFailed(t *testing.T) {
	d, repo := newTestK8sDriver(defaultCfg())

	nb := &NotebookServer{Name: "ghost", Status: StatusStarting}
	_ = repo.Create(context.Background(), nb)

	_ = d.SyncStatus(context.Background(), []*NotebookServer{nb})

	updated, _ := repo.Get(context.Background(), "ghost")
	if updated.Status != StatusFailed {
		t.Errorf("status = %s, want failed", updated.Status)
	}
}

// ─── nbSafeName ───────────────────────────────────────────────────────────────

func TestNbSafeName(t *testing.T) {
	cases := []struct{ in, want string }{
		{"my-notebook", "my-notebook"},
		{"My Notebook!", "my-notebook"},
		{"123abc", "123abc"},
		{"--leading", "leading"},
		{"trailing--", "trailing"},
		{"", ""},
	}
	for _, c := range cases {
		got := nbSafeName(c.in)
		if got != c.want {
			t.Errorf("nbSafeName(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// ─── mergeResources ───────────────────────────────────────────────────────────

func TestMergeResources_SpecOverridesDefaults(t *testing.T) {
	spec := pipeline.Resources{CPU: "4", Memory: "8Gi"}
	defaults := pipeline.Resources{CPU: "1", Memory: "2Gi", GPU: "1"}

	reqs := mergeResources(spec, defaults)

	if reqs.Limits.Cpu().Cmp(resource.MustParse("4")) != 0 {
		t.Errorf("CPU = %s, want 4", reqs.Limits.Cpu())
	}
	if reqs.Limits.Memory().Cmp(resource.MustParse("8Gi")) != 0 {
		t.Errorf("memory = %s, want 8Gi", reqs.Limits.Memory())
	}
	// GPU falls through from defaults since spec has no GPU.
	if _, ok := reqs.Limits[corev1.ResourceName("nvidia.com/gpu")]; !ok {
		t.Error("GPU should be inherited from defaults")
	}
}

func TestMergeResources_EmptySpecUsesDefaults(t *testing.T) {
	spec := pipeline.Resources{}
	defaults := pipeline.Resources{CPU: "2", Memory: "4Gi"}

	reqs := mergeResources(spec, defaults)

	if reqs.Limits.Cpu().Cmp(resource.MustParse("2")) != 0 {
		t.Errorf("CPU = %s, want 2 (from defaults)", reqs.Limits.Cpu())
	}
}

// ─── pvcName / statefulSetName ────────────────────────────────────────────────

func TestK8sDriver_NamingConsistency(t *testing.T) {
	d, _ := newTestK8sDriver(defaultCfg())

	pvc := d.pvcName("aaaabbbbccccdddd")
	if pvc != "piper-nb-vol-aaaabbbbcccc" {
		t.Errorf("pvcName = %q", pvc)
	}

	sts := d.statefulSetName("my-notebook")
	if sts != "piper-nb-my-notebook" {
		t.Errorf("statefulSetName = %q", sts)
	}
}

// ─── defaultNamespace ─────────────────────────────────────────────────────────

func TestNewK8sDriver_DefaultNamespace(t *testing.T) {
	client := fake.NewSimpleClientset()
	d := NewK8sDriver(client, K8sDriverConfig{WorkerImage: "img"}, newFakeRepo())
	if d.ns != "default" {
		t.Errorf("ns = %q, want default", d.ns)
	}
}

func TestNewK8sDriver_DefaultStorageSize(t *testing.T) {
	client := fake.NewSimpleClientset()
	d := NewK8sDriver(client, K8sDriverConfig{WorkerImage: "img"}, newFakeRepo())
	if d.cfg.StorageSize != "10Gi" {
		t.Errorf("StorageSize = %q, want 10Gi", d.cfg.StorageSize)
	}
}
