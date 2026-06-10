package notebookworker

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"gopkg.in/yaml.v3"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/util/retry"

	iagent "github.com/piper/piper/internal/agent"
	"github.com/piper/piper/internal/grpcagent"
	"github.com/piper/piper/pkg/internal/k8smeta"
	"github.com/piper/piper/pkg/notebook"
	"github.com/piper/piper/pkg/workload"
	"k8s.io/client-go/kubernetes"
)

type Dispatcher = *grpcagent.Dispatcher

type Config struct {
	WorkerID     string
	ClusterName  string
	Namespaces   []string
	Client       kubernetes.Interface
	Namespace    string
	Image        string
	StorageClass string
	StorageSize  string
	// PodDefaults are cluster-wide defaults applied to every notebook pod.
	// Merged before the per-notebook pod_template (defaults lose on conflict).
	PodDefaults  corev1.PodTemplateSpec
	ReportStatus func(notebook.WorkerStatusUpdate) error
}

type Worker struct {
	cfg        Config
	statusMu   sync.Mutex
	lastStatus map[string]string
}

func New(cfg Config) *Worker {
	return &Worker{cfg: cfg, lastStatus: make(map[string]string)}
}

func Register(dispatcher Dispatcher, cfg Config) *Worker {
	w := New(cfg)
	w.register(dispatcher)
	return w
}

func (a *Worker) register(dispatcher Dispatcher) {
	_ = grpcagent.RegisterJSON(dispatcher, iagent.MethodNotebookProvisionVolume, a.provisionNotebookVolume)
	_ = grpcagent.RegisterJSON(dispatcher, iagent.MethodNotebookStart, a.startNotebook)
	_ = grpcagent.RegisterJSON(dispatcher, iagent.MethodNotebookStop, func(ctx context.Context, req notebook.WorkerStopRequest) (any, error) {
		return nil, a.stopNotebook(ctx, req)
	})
	_ = grpcagent.RegisterJSON(dispatcher, iagent.MethodNotebookDeprovision, func(ctx context.Context, req notebook.WorkerDeprovisionVolumeRequest) (any, error) {
		return nil, a.deprovisionNotebookVolume(ctx, req)
	})
	_ = grpcagent.RegisterJSON(dispatcher, iagent.MethodNotebookSyncStatus, a.syncNotebookStatus)
	_ = grpcagent.RegisterJSON(dispatcher, iagent.MethodFSListFiles, a.listFiles)
}

// listFiles returns an empty list for K8s volumes.
// TODO: exec into the running notebook pod to list files when available.
func (a *Worker) listFiles(_ context.Context, _ notebook.FSListFilesRequest) (*notebook.FSListFilesResponse, error) {
	return &notebook.FSListFilesResponse{Files: []string{}}, nil
}

func (a *Worker) provisionNotebookVolume(ctx context.Context, req notebook.WorkerProvisionVolumeRequest) (*notebook.WorkerProvisionVolumeResponse, error) {
	if req.VolumeID == "" {
		return nil, fmt.Errorf("volume_id is required")
	}
	ns := a.notebookNamespace()
	size := req.StorageSize
	if size == "" {
		size = a.cfg.StorageSize
	}
	if size == "" {
		size = "10Gi"
	}
	qty, err := resource.ParseQuantity(size)
	if err != nil {
		return nil, fmt.Errorf("invalid storage_size %q: %w", size, err)
	}

	pvc := &corev1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{
			Name:      notebookPVCName(req.VolumeID),
			Namespace: ns,
			Labels:    a.k8sLabels("notebook-volume", req.VolumeID),
			Annotations: map[string]string{
				k8smeta.AnnotationVolumeID: req.VolumeID,
			},
		},
		Spec: corev1.PersistentVolumeClaimSpec{
			AccessModes: []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce},
			Resources: corev1.VolumeResourceRequirements{
				Requests: corev1.ResourceList{corev1.ResourceStorage: qty},
			},
		},
	}
	if a.cfg.StorageClass != "" {
		storageClass := a.cfg.StorageClass
		pvc.Spec.StorageClassName = &storageClass
	}
	if _, err := a.cfg.Client.CoreV1().PersistentVolumeClaims(ns).Create(ctx, pvc, metav1.CreateOptions{}); err != nil && !k8serrors.IsAlreadyExists(err) {
		return nil, err
	}
	return &notebook.WorkerProvisionVolumeResponse{WorkDir: notebook.ContainerWorkDir}, nil
}

func (a *Worker) startNotebook(ctx context.Context, req notebook.WorkerStartRequest) (*notebook.WorkerStartResponse, error) {
	if req.VolumeID == "" {
		return nil, fmt.Errorf("volume_id is required")
	}
	var spec notebook.Notebook
	if err := yaml.Unmarshal([]byte(req.YAML), &spec); err != nil {
		return nil, fmt.Errorf("invalid notebook yaml: %w", err)
	}
	if spec.Metadata.Name == "" {
		return nil, fmt.Errorf("metadata.name is required")
	}

	ns := a.notebookNamespace()
	name := notebookWorkloadName(spec.Metadata.Name)
	labels := a.k8sLabels("notebook", spec.Metadata.Name)
	baseURL := fmt.Sprintf("/notebooks/%s/proxy/", spec.Metadata.Name)
	token := uuid.NewString()
	workDir := req.WorkDir
	if workDir == "" {
		workDir = notebook.ContainerWorkDir
	}
	replicas := int32(1)

	// ── Stage 1: deep copy server-level PodDefaults ───────────────────────────
	podTemplate := *a.cfg.PodDefaults.DeepCopy()

	// ── Stage 2: merge per-notebook pod_template on top ──────────────────────
	if spec.Spec.Driver.K8s != nil {
		mergePodTemplate(&podTemplate, &spec.Spec.Driver.K8s.PodTemplate)
	}

	// ── Stage 3: apply piper-required fields (always last) ───────────────────

	// Resolve image: spec.driver.k8s.image > notebook container image in merged template
	// > cfg.Image > hardcoded default.
	var driverImage string
	if spec.Spec.Driver.K8s != nil {
		driverImage = spec.Spec.Driver.K8s.Image
	}
	image := resolveImage(a.cfg.Image, driverImage, podTemplate)

	// Piper selector labels must be present; merged template labels are preserved.
	if podTemplate.Labels == nil {
		podTemplate.Labels = make(map[string]string)
	}
	for k, v := range labels {
		podTemplate.Labels[k] = v
	}

	// Find or create the notebook container.
	nbIdx := containerIndex(podTemplate.Spec.Containers, "notebook")
	if nbIdx < 0 {
		podTemplate.Spec.Containers = append(podTemplate.Spec.Containers, corev1.Container{Name: "notebook"})
		nbIdx = len(podTemplate.Spec.Containers) - 1
	}
	c := &podTemplate.Spec.Containers[nbIdx]
	c.Image = image
	prepSteps, err := spec.Spec.Prepare.StepsForBackend(notebook.PrepareBackendK8s)
	if err != nil {
		return nil, err
	}
	baseCommand := notebook.JupyterStartArgs(baseURL, token, notebook.ContainerWorkDir, 8888)
	if len(prepSteps) > 0 {
		script, err := notebook.BuildLaunchScript(nil, prepSteps, baseCommand, notebook.ContainerWorkDir)
		if err != nil {
			return nil, err
		}
		c.Command = []string{"/bin/sh"}
		c.Args = []string{"-lc", script}
	} else {
		// piper required args appended last so they win on duplicate flags.
		c.Args = append(c.Args, baseCommand...)
	}
	c.Ports = []corev1.ContainerPort{{Name: "notebook", ContainerPort: 8888}}

	// Ensure the piper-data volume mount is present (fixed name avoids user collisions).
	if !hasMountName(c.VolumeMounts, piperDataVolume) {
		c.VolumeMounts = append(c.VolumeMounts, corev1.VolumeMount{
			Name:      piperDataVolume,
			MountPath: notebook.ContainerWorkDir,
		})
	}

	// Ensure the PVC volume is present.
	if !hasVolumeName(podTemplate.Spec.Volumes, piperDataVolume) {
		podTemplate.Spec.Volumes = append(podTemplate.Spec.Volumes, corev1.Volume{
			Name: piperDataVolume,
			VolumeSource: corev1.VolumeSource{
				PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{
					ClaimName: notebookPVCName(req.VolumeID),
				},
			},
		})
	}

	sts := &appsv1.StatefulSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:        name,
			Namespace:   ns,
			Labels:      labels,
			Annotations: k8smeta.WorkloadAnnotations(spec.Metadata.Name),
		},
		Spec: appsv1.StatefulSetSpec{
			Replicas: &replicas,
			Selector: &metav1.LabelSelector{MatchLabels: labels},
			Template: podTemplate,
		},
	}
	if _, err := a.cfg.Client.AppsV1().StatefulSets(ns).Create(ctx, sts, metav1.CreateOptions{}); err != nil {
		if !k8serrors.IsAlreadyExists(err) {
			return nil, err
		}
		if err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
			existing, err := a.cfg.Client.AppsV1().StatefulSets(ns).Get(ctx, name, metav1.GetOptions{})
			if err != nil {
				return err
			}
			existing.Spec.Replicas = sts.Spec.Replicas
			existing.Spec.Template = sts.Spec.Template
			_, err = a.cfg.Client.AppsV1().StatefulSets(ns).Update(ctx, existing, metav1.UpdateOptions{})
			return err
		}); err != nil {
			return nil, err
		}
	}

	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns, Labels: labels},
		Spec: corev1.ServiceSpec{
			Selector:  labels,
			ClusterIP: "None",
			Ports:     []corev1.ServicePort{{Name: "notebook", Port: 8888}},
		},
	}
	if _, err := a.cfg.Client.CoreV1().Services(ns).Create(ctx, svc, metav1.CreateOptions{}); err != nil && !k8serrors.IsAlreadyExists(err) {
		return nil, err
	}

	// target = headless service DNS + port inside the cluster, reachable by the agent.
	svcTarget := fmt.Sprintf("%s.%s.svc.cluster.local:8888", name, ns)
	return &notebook.WorkerStartResponse{
		Token:    token,
		WorkDir:  workDir,
		Endpoint: fmt.Sprintf("tunnel://%s?target=%s", a.cfg.WorkerID, svcTarget),
	}, nil
}

func (a *Worker) stopNotebook(ctx context.Context, req notebook.WorkerStopRequest) error {
	if req.Name == "" {
		return fmt.Errorf("name is required")
	}
	ns := a.notebookNamespace()
	name := notebookWorkloadName(req.Name)
	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		current, err := a.cfg.Client.AppsV1().StatefulSets(ns).Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			if k8serrors.IsNotFound(err) {
				return nil
			}
			return err
		}
		zero := int32(0)
		current.Spec.Replicas = &zero
		_, err = a.cfg.Client.AppsV1().StatefulSets(ns).Update(ctx, current, metav1.UpdateOptions{})
		return err
	})
}

func (a *Worker) deprovisionNotebookVolume(ctx context.Context, req notebook.WorkerDeprovisionVolumeRequest) error {
	if req.VolumeID == "" {
		return fmt.Errorf("volume_id is required")
	}
	err := a.cfg.Client.CoreV1().PersistentVolumeClaims(a.notebookNamespace()).Delete(ctx, notebookPVCName(req.VolumeID), metav1.DeleteOptions{})
	if err != nil && !k8serrors.IsNotFound(err) {
		return err
	}
	return nil
}

func (a *Worker) syncNotebookStatus(ctx context.Context, req notebook.WorkerSyncStatusRequest) (notebook.WorkerSyncStatusResponse, error) {
	statuses := make(map[string]string, len(req.Targets))
	for _, target := range req.Targets {
		name := target.Name
		sts, err := a.cfg.Client.AppsV1().StatefulSets(a.notebookNamespace()).Get(ctx, notebookWorkloadName(name), metav1.GetOptions{})
		if err != nil {
			if k8serrors.IsNotFound(err) {
				statuses[name] = notebook.StatusFailed
			}
			continue
		}
		desired := int32(1)
		if sts.Spec.Replicas != nil {
			desired = *sts.Spec.Replicas
		}
		switch {
		case sts.Status.ReadyReplicas >= 1:
			statuses[name] = notebook.StatusRunning
		case sts.Status.ReadyReplicas == 0 && desired == 0:
			statuses[name] = notebook.StatusStopped
		}
	}
	return notebook.WorkerSyncStatusResponse{Statuses: statuses}, nil
}

// Observe reports Kubernetes-observed notebook state. It is independent of the
// master connection: failed pushes are recovered by the generic reconnect sync.
func (a *Worker) Observe(ctx context.Context) {
	if a.cfg.Client == nil || a.cfg.ReportStatus == nil {
		return
	}
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	a.observeOnce(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			a.observeOnce(ctx)
		}
	}
}

func (a *Worker) observeOnce(ctx context.Context) {
	selector := k8smeta.ManagedSelector() + "," + k8smeta.LabelWorkloadKind + "=notebook"
	items, err := a.cfg.Client.AppsV1().StatefulSets(a.notebookNamespace()).List(ctx, metav1.ListOptions{
		LabelSelector: selector,
	})
	if err != nil {
		return
	}
	for i := range items.Items {
		sts := &items.Items[i]
		name := sts.Annotations[k8smeta.AnnotationWorkloadID]
		if name == "" {
			continue
		}
		status := observedStatefulSetStatus(sts)
		if status == "" || !a.statusChanged(name, status) {
			continue
		}
		_ = a.cfg.ReportStatus(notebook.WorkerStatusUpdate{Name: name, Status: status})
	}
}

func (a *Worker) statusChanged(name, status string) bool {
	a.statusMu.Lock()
	defer a.statusMu.Unlock()
	if a.lastStatus[name] == status {
		return false
	}
	a.lastStatus[name] = status
	return true
}

func observedStatefulSetStatus(sts *appsv1.StatefulSet) string {
	desired := int32(1)
	if sts.Spec.Replicas != nil {
		desired = *sts.Spec.Replicas
	}
	if desired == 0 && sts.Status.ReadyReplicas == 0 {
		return notebook.StatusStopped
	}
	for _, condition := range sts.Status.Conditions {
		if condition.Type == appsv1.StatefulSetConditionType("ReplicaFailure") && condition.Status == corev1.ConditionTrue {
			return notebook.StatusFailed
		}
	}
	if desired > 0 && sts.Status.ReadyReplicas >= desired {
		return notebook.StatusRunning
	}
	if desired > 0 {
		return notebook.StatusStarting
	}
	return ""
}

func (a *Worker) notebookNamespace() string {
	if a.cfg.Namespace != "" {
		return a.cfg.Namespace
	}
	if len(a.cfg.Namespaces) > 0 {
		return a.cfg.Namespaces[0]
	}
	return "default"
}

func (a *Worker) k8sLabels(kind, id string) map[string]string {
	return k8smeta.WorkloadLabels(a.cfg.ClusterName, kind, id)
}

func notebookWorkloadName(name string) string {
	return "piper-nb-" + workload.SafeName(name)
}

func notebookPVCName(volumeID string) string {
	clean := strings.ReplaceAll(volumeID, "-", "")
	if len(clean) > 12 {
		clean = clean[:12]
	}
	return "piper-nb-vol-" + clean
}

// piperDataVolume is the fixed name for the piper-managed PVC volume and mount.
// Using a piper-prefixed name avoids collisions with user-defined volumes.
const piperDataVolume = "piper-data"

// mergePodTemplate overlays src onto dst in-place.
// For each field: src wins if set, dst keeps its value otherwise.
// Containers and Volumes are merged by name (src entry replaces dst entry with same name;
// src-only entries are appended).
func mergePodTemplate(dst, src *corev1.PodTemplateSpec) {
	// Labels and Annotations: src merged into dst (src wins on conflict).
	dst.Labels = mergeStringMaps(dst.Labels, src.Labels)
	dst.Annotations = mergeStringMaps(dst.Annotations, src.Annotations)

	// PodSpec scalar fields: src wins if non-zero.
	if src.Spec.ServiceAccountName != "" {
		dst.Spec.ServiceAccountName = src.Spec.ServiceAccountName
	}
	if src.Spec.SchedulerName != "" {
		dst.Spec.SchedulerName = src.Spec.SchedulerName
	}
	if src.Spec.NodeName != "" {
		dst.Spec.NodeName = src.Spec.NodeName
	}

	// NodeSelector: src merged into dst.
	dst.Spec.NodeSelector = mergeStringMaps(dst.Spec.NodeSelector, src.Spec.NodeSelector)

	// Tolerations: append src (duplicates allowed — k8s deduplicates).
	dst.Spec.Tolerations = append(dst.Spec.Tolerations, src.Spec.Tolerations...)

	// Containers: merge by name.
	dst.Spec.Containers = mergeContainers(dst.Spec.Containers, src.Spec.Containers)
	dst.Spec.InitContainers = mergeContainers(dst.Spec.InitContainers, src.Spec.InitContainers)

	// Volumes: src replaces dst entry with same name; src-only entries appended.
	dst.Spec.Volumes = mergeVolumes(dst.Spec.Volumes, src.Spec.Volumes)
}

// mergeContainers merges src containers into dst by name.
// src entries replace dst entries with the same name; src-only entries are appended.
func mergeContainers(dst, src []corev1.Container) []corev1.Container {
	if len(src) == 0 {
		return dst
	}
	out := make([]corev1.Container, len(dst))
	copy(out, dst)
	for _, sc := range src {
		idx := containerIndex(out, sc.Name)
		if idx >= 0 {
			out[idx] = sc
		} else {
			out = append(out, sc)
		}
	}
	return out
}

// mergeVolumes merges src volumes into dst by name.
func mergeVolumes(dst, src []corev1.Volume) []corev1.Volume {
	if len(src) == 0 {
		return dst
	}
	out := make([]corev1.Volume, len(dst))
	copy(out, dst)
	for _, sv := range src {
		idx := volumeIndex(out, sv.Name)
		if idx >= 0 {
			out[idx] = sv
		} else {
			out = append(out, sv)
		}
	}
	return out
}

func mergeStringMaps(dst, src map[string]string) map[string]string {
	if len(src) == 0 {
		return dst
	}
	out := make(map[string]string, len(dst)+len(src))
	for k, v := range dst {
		out[k] = v
	}
	for k, v := range src {
		out[k] = v
	}
	return out
}

// resolveImage returns the image to use for the notebook container.
// Priority: spec.k8s.image > pod_template notebook container image > cfg.Image > default.
func resolveImage(cfgImage string, driverImage string, tpl corev1.PodTemplateSpec) string {
	if driverImage != "" {
		return driverImage
	}
	idx := containerIndex(tpl.Spec.Containers, "notebook")
	if idx >= 0 && tpl.Spec.Containers[idx].Image != "" {
		return tpl.Spec.Containers[idx].Image
	}
	if cfgImage != "" {
		return cfgImage
	}
	return "jupyter/scipy-notebook:latest"
}

func containerIndex(containers []corev1.Container, name string) int {
	for i, c := range containers {
		if c.Name == name {
			return i
		}
	}
	return -1
}

func volumeIndex(volumes []corev1.Volume, name string) int {
	for i, v := range volumes {
		if v.Name == name {
			return i
		}
	}
	return -1
}

func hasMountName(mounts []corev1.VolumeMount, name string) bool {
	for _, m := range mounts {
		if m.Name == name {
			return true
		}
	}
	return false
}

func hasVolumeName(volumes []corev1.Volume, name string) bool {
	return volumeIndex(volumes, name) >= 0
}
