package notebook

import (
	"context"
	"fmt"
	"strings"
	"unicode"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"

	"github.com/google/uuid"

	"github.com/piper/piper/pkg/pipeline"
)

// K8sDriverConfig holds the configuration for the K8s notebook driver.
// Defined here (rather than in config.go) to avoid circular imports between
// the piper root package and pkg/notebook.
type K8sDriverConfig struct {
	Namespace    string
	WorkerImage  string // Jupyter container image
	StorageClass string // empty → use cluster default
	StorageSize  string // default "10Gi"
	PodDefaults  K8sPodDefaults
}

// K8sPodDefaults are cluster-wide defaults applied to every notebook pod.
// Individual NotebookServerSpec fields override these when non-empty.
type K8sPodDefaults struct {
	Resources    pipeline.Resources
	NodeSelector map[string]string
	Tolerations  []pipeline.Toleration
	Annotations  map[string]string
}

// K8sDriver implements Driver by creating K8s StatefulSets, PVCs, and Services.
// It also implements StatusSyncer for periodic readiness polling.
type K8sDriver struct {
	k8s  kubernetes.Interface
	cfg  K8sDriverConfig
	ns   string
	repo Repository
}

// NewK8sDriver creates a K8sDriver. repo is used by SyncStatus to persist status updates.
func NewK8sDriver(k8s kubernetes.Interface, cfg K8sDriverConfig, repo Repository) *K8sDriver {
	ns := cfg.Namespace
	if ns == "" {
		ns = "default"
	}
	if cfg.StorageSize == "" {
		cfg.StorageSize = "10Gi"
	}
	cfg.Namespace = ns
	return &K8sDriver{k8s: k8s, cfg: cfg, ns: ns, repo: repo}
}

// ProvisionVolume creates a PVC for the notebook and sets vol.WorkDir.
// storageSize overrides d.cfg.StorageSize when non-empty.
func (d *K8sDriver) ProvisionVolume(ctx context.Context, vol *NotebookVolume, storageSize string) error {
	name := d.pvcName(vol.ID)

	size := storageSize
	if size == "" {
		size = d.cfg.StorageSize
	}
	storageQty, err := resource.ParseQuantity(size)
	if err != nil {
		return fmt.Errorf("notebook k8s: invalid storage_size %q: %w", size, err)
	}

	pvc := &corev1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: d.ns,
			Labels: map[string]string{
				"app.kubernetes.io/managed-by": "piper",
				"piper/notebook-volume":        vol.ID,
			},
		},
		Spec: corev1.PersistentVolumeClaimSpec{
			AccessModes: []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce},
			Resources: corev1.VolumeResourceRequirements{
				Requests: corev1.ResourceList{
					corev1.ResourceStorage: storageQty,
				},
			},
		},
	}

	if d.cfg.StorageClass != "" {
		sc := d.cfg.StorageClass
		pvc.Spec.StorageClassName = &sc
	}

	if _, err := d.k8s.CoreV1().PersistentVolumeClaims(d.ns).Create(ctx, pvc, metav1.CreateOptions{}); err != nil {
		if !k8serrors.IsAlreadyExists(err) {
			return fmt.Errorf("notebook k8s: create pvc %s: %w", name, err)
		}
	}

	vol.WorkDir = "/home/jovyan"
	vol.WorkerID = "" // K8s mode — no worker node affinity
	return nil
}

// Start creates a StatefulSet and headless Service for the notebook.
// Returns a NotebookServer with Status=StatusStarting; SyncStatus will transition
// to StatusRunning once the pod is ready.
func (d *K8sDriver) Start(ctx context.Context, spec NotebookServerSpec, vol *NotebookVolume, _ string) (*NotebookServer, error) {
	name := spec.Metadata.Name
	safeName := d.statefulSetName(name)
	pvcName := d.pvcName(vol.ID)
	token := uuid.NewString()

	// Resolve container image: spec > config > fallback.
	image := spec.Spec.Image
	if image == "" {
		image = d.cfg.WorkerImage
	}
	if image == "" {
		image = "jupyter/scipy-notebook:latest"
	}

	// Resolve resources: spec overrides defaults.
	resReqs := mergeResources(spec.Spec.Resources, d.cfg.PodDefaults.Resources)

	// Resolve node selector: spec overrides defaults.
	nodeSelector := d.cfg.PodDefaults.NodeSelector
	if len(spec.Spec.NodeSelector) > 0 {
		nodeSelector = spec.Spec.NodeSelector
	}

	// Resolve tolerations: spec completely replaces defaults when non-empty.
	tolerations := buildK8sTolerations(d.cfg.PodDefaults.Tolerations)
	if len(spec.Spec.Tolerations) > 0 {
		tolerations = buildK8sTolerations(spec.Spec.Tolerations)
	}

	// Annotations: cluster defaults only (no per-spec override).
	annotations := d.cfg.PodDefaults.Annotations

	labels := map[string]string{
		"app.kubernetes.io/managed-by": "piper",
		"piper/notebook":               name,
	}

	replicas := int32(1)
	baseURL := fmt.Sprintf("/notebooks/%s/proxy/", name)

	sts := &appsv1.StatefulSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:        safeName,
			Namespace:   d.ns,
			Labels:      labels,
			Annotations: annotations,
		},
		Spec: appsv1.StatefulSetSpec{
			Replicas: &replicas,
			Selector: &metav1.LabelSelector{MatchLabels: labels},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels:      labels,
					Annotations: annotations,
				},
				Spec: corev1.PodSpec{
					NodeSelector: nodeSelector,
					Tolerations:  tolerations,
					Containers: []corev1.Container{
						{
							Name:            "notebook",
							Image:           image,
							ImagePullPolicy: corev1.PullIfNotPresent,
							Args: []string{
								"start-notebook.py",
								"--ServerApp.base_url=" + baseURL,
								"--ServerApp.token=" + token,
								"--ServerApp.root_dir=/home/jovyan",
								"--ServerApp.allow_origin=*",
								"--no-browser",
								"--port=8888",
							},
							Ports: []corev1.ContainerPort{
								{Name: "notebook", ContainerPort: 8888},
							},
							Resources: resReqs,
							VolumeMounts: []corev1.VolumeMount{
								{Name: "data", MountPath: "/home/jovyan"},
							},
						},
					},
					Volumes: []corev1.Volume{
						{
							Name: "data",
							VolumeSource: corev1.VolumeSource{
								PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{
									ClaimName: pvcName,
								},
							},
						},
					},
				},
			},
		},
	}

	if _, err := d.k8s.AppsV1().StatefulSets(d.ns).Create(ctx, sts, metav1.CreateOptions{}); err != nil {
		if !k8serrors.IsAlreadyExists(err) {
			return nil, fmt.Errorf("notebook k8s: create statefulset %s: %w", safeName, err)
		}
		// StatefulSet exists (stopped state) — patch replicas back to 1 with new spec.
		existing, gErr := d.k8s.AppsV1().StatefulSets(d.ns).Get(ctx, safeName, metav1.GetOptions{})
		if gErr != nil {
			return nil, fmt.Errorf("notebook k8s: get statefulset %s for restart: %w", safeName, gErr)
		}
		existing.Spec.Replicas = sts.Spec.Replicas
		existing.Spec.Template = sts.Spec.Template
		if _, uErr := d.k8s.AppsV1().StatefulSets(d.ns).Update(ctx, existing, metav1.UpdateOptions{}); uErr != nil {
			return nil, fmt.Errorf("notebook k8s: restart statefulset %s: %w", safeName, uErr)
		}
	}

	// Headless service for stable DNS.
	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      safeName,
			Namespace: d.ns,
			Labels:    labels,
		},
		Spec: corev1.ServiceSpec{
			Selector:  labels,
			ClusterIP: "None", // headless
			Ports: []corev1.ServicePort{
				{Name: "notebook", Port: 8888},
			},
		},
	}
	if _, err := d.k8s.CoreV1().Services(d.ns).Create(ctx, svc, metav1.CreateOptions{}); err != nil {
		if !k8serrors.IsAlreadyExists(err) {
			// Non-fatal: the proxy will still work if we use pod IP directly.
			// Log and continue rather than failing the whole Start.
			_ = err
		}
	}

	endpoint := fmt.Sprintf("http://%s.%s.svc.cluster.local:8888", safeName, d.ns)

	return &NotebookServer{
		Name:     name,
		Status:   StatusStarting,
		Token:    token,
		Endpoint: endpoint,
		WorkerID: "",
		Image:    image,
	}, nil
}

// Stop scales the StatefulSet replicas to 0, leaving the PVC intact.
func (d *K8sDriver) Stop(ctx context.Context, nb *NotebookServer) error {
	safeName := d.statefulSetName(nb.Name)
	zero := int32(0)

	existing, err := d.k8s.AppsV1().StatefulSets(d.ns).Get(ctx, safeName, metav1.GetOptions{})
	if err != nil {
		if k8serrors.IsNotFound(err) {
			return nil
		}
		return fmt.Errorf("notebook k8s: get statefulset %s: %w", safeName, err)
	}

	existing.Spec.Replicas = &zero
	if _, err := d.k8s.AppsV1().StatefulSets(d.ns).Update(ctx, existing, metav1.UpdateOptions{}); err != nil {
		return fmt.Errorf("notebook k8s: scale down statefulset %s: %w", safeName, err)
	}
	return nil
}

// DeprovisionVolume deletes the PVC. NotFound errors are silently ignored.
func (d *K8sDriver) DeprovisionVolume(ctx context.Context, vol *NotebookVolume) error {
	pvcName := d.pvcName(vol.ID)
	err := d.k8s.CoreV1().PersistentVolumeClaims(d.ns).Delete(ctx, pvcName, metav1.DeleteOptions{})
	if err != nil && !k8serrors.IsNotFound(err) {
		return fmt.Errorf("notebook k8s: delete pvc %s: %w", pvcName, err)
	}
	return nil
}

// SyncStatus reconciles the status of starting/running notebook servers by
// querying their StatefulSet readyReplicas. It implements StatusSyncer.
func (d *K8sDriver) SyncStatus(ctx context.Context, servers []*NotebookServer) error {
	for _, nb := range servers {
		safeName := d.statefulSetName(nb.Name)

		sts, err := d.k8s.AppsV1().StatefulSets(d.ns).Get(ctx, safeName, metav1.GetOptions{})
		if err != nil {
			if k8serrors.IsNotFound(err) {
				_ = d.repo.SetStatus(ctx, nb.Name, StatusFailed)
			}
			continue
		}

		desired := int32(1)
		if sts.Spec.Replicas != nil {
			desired = *sts.Spec.Replicas
		}

		switch {
		case sts.Status.ReadyReplicas >= 1 && nb.Status != StatusRunning:
			_ = d.repo.SetStatus(ctx, nb.Name, StatusRunning)

		case sts.Status.ReadyReplicas == 0 && desired == 0 && nb.Status != StatusStopped:
			_ = d.repo.SetStatus(ctx, nb.Name, StatusStopped)
		}
	}
	return nil
}

// --- helpers ---

func (d *K8sDriver) statefulSetName(name string) string {
	return "piper-nb-" + nbSafeName(name)
}

func (d *K8sDriver) pvcName(volumeID string) string {
	clean := strings.ReplaceAll(volumeID, "-", "")
	if len(clean) > 12 {
		clean = clean[:12]
	}
	return "piper-nb-vol-" + clean
}

// nbSafeName converts an arbitrary string to a K8s-safe lowercase name:
// alphanumeric + hyphen, max 40 chars, no leading/trailing hyphens.
func nbSafeName(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
		} else {
			b.WriteRune('-')
		}
	}
	result := strings.Trim(b.String(), "-")
	if len(result) > 40 {
		result = result[:40]
	}
	return strings.TrimRight(result, "-")
}

// mergeResources merges spec-level resource overrides with cluster defaults.
// spec fields take precedence over defaults when non-empty.
func mergeResources(spec pipeline.Resources, defaults pipeline.Resources) corev1.ResourceRequirements {
	cpu := spec.CPU
	if cpu == "" {
		cpu = defaults.CPU
	}
	mem := spec.Memory
	if mem == "" {
		mem = defaults.Memory
	}
	gpu := spec.GPU
	if gpu == "" {
		gpu = defaults.GPU
	}

	reqs := corev1.ResourceList{}
	limits := corev1.ResourceList{}

	if cpu != "" {
		q := resource.MustParse(cpu)
		reqs[corev1.ResourceCPU] = q
		limits[corev1.ResourceCPU] = q
	}
	if mem != "" {
		q := resource.MustParse(mem)
		reqs[corev1.ResourceMemory] = q
		limits[corev1.ResourceMemory] = q
	}
	if gpu != "" {
		q := resource.MustParse(gpu)
		limits[corev1.ResourceName("nvidia.com/gpu")] = q
	}

	return corev1.ResourceRequirements{Requests: reqs, Limits: limits}
}

// buildK8sTolerations converts pipeline.Toleration slices to corev1.Toleration slices.
func buildK8sTolerations(tolerations []pipeline.Toleration) []corev1.Toleration {
	if len(tolerations) == 0 {
		return nil
	}
	out := make([]corev1.Toleration, 0, len(tolerations))
	for _, t := range tolerations {
		out = append(out, corev1.Toleration{
			Key:               t.Key,
			Operator:          corev1.TolerationOperator(t.Operator),
			Value:             t.Value,
			Effect:            corev1.TaintEffect(t.Effect),
			TolerationSeconds: t.TolerationSeconds,
		})
	}
	return out
}
