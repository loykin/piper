// Package k8sdriver implements notebook.Driver directly in-process against
// a Kubernetes cluster — no remote worker/tunnel involved, mirroring
// fed.md §13.2's Pipeline direct-runtime treatment and the docker/baremetal
// pkg/notebook/dispatch/localdriver package's shape. It is a separate
// package (not a third case in that package's driver) because the K8s
// worker never implemented the shared low-level notebookdriver.Driver
// interface docker/process share — it talks to kubernetes.Interface
// directly, same split as pkg/notebook/worker/driver/{docker,process,k8s}.
//
// Workspace file access (reading a running notebook's files for pipeline
// template snapshotting) is handled by the separate WorkspaceReader in
// workspace.go, using pod exec rather than the deleted remote worker's
// tunnel-based volume_browser.go.
//
// Endpoint resolution assumes Piper itself runs with network reachability
// to the target namespaces' cluster-internal service DNS (e.g. deployed as
// a pod in the same cluster it manages via runtime.k8s.in_cluster) —
// already an existing, supported deployment mode for runtime.type: k8s, not
// a new constraint introduced here.
package k8sdriver

import (
	"bufio"
	"context"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/util/retry"

	"github.com/loykin/piper/internal/logsink"
	"github.com/loykin/piper/pkg/manifest"
	k8smanifest "github.com/loykin/piper/pkg/manifest/k8s"
	"github.com/loykin/piper/pkg/notebook"
)

// piperDataVolume is the fixed name for the piper-managed PVC volume and
// mount, matching pkg/notebook/worker/driver/k8s/worker.go.
const piperDataVolume = "piper-data"

// Config configures a direct, in-process K8s notebook driver.
type Config struct {
	// WorkerID is a fixed local identity used to populate
	// NotebookServer.WorkerID and as the ReportStatus agentID.
	WorkerID string
	// ClusterName is an informational label only (piper.io/cluster) — not
	// used in any selector, so it's safe to leave empty.
	ClusterName string
	Namespaces  []string
	Client      kubernetes.Interface
	// LogClient enables pod log streaming for running notebooks. Optional.
	LogClient logsink.PushClient
	// ObserveInterval controls how often Observe polls StatefulSet status.
	// Zero means 2s, matching pkg/notebook/worker/driver/k8s/worker.go's Observe.
	ObserveInterval time.Duration
	// ReportStatus mirors pkg/notebook/dispatch/localdriver.Config.ReportStatus's
	// signature exactly so piper.go can wire the same nbMgr.UpdateStatus
	// closure shape for every runtime.type. K8s has no PID concept (always
	// 0) and only sets workDir/token once, at Start — later Observe-driven
	// status changes pass empty strings, which Manager.UpdateStatus already
	// treats as "leave unchanged".
	ReportStatus func(projectID, name, status, endpoint, workDir, token string, pid int, env string) error
}

// Driver implements notebook.Driver directly against a Kubernetes cluster.
// Call Observe once at startup (as a long-running background goroutine) to
// detect StatefulSet readiness transitions and stream pod logs — there is
// no Recover: unlike docker/process's in-memory generation-counter state,
// StatefulSets/PVCs are external K8s objects Observe rediscovers on its own
// on every tick, so a restart needs no special reattachment step (the
// remote K8s worker doesn't implement Recoverable either).
type Driver struct {
	cfg Config

	statusMu   sync.Mutex
	lastStatus map[string]string // "projectID:name" -> last reported status, for change dedup

	logMu      sync.Mutex
	logGens    map[string]uint64
	logCancels map[string]context.CancelFunc
}

// New constructs a Driver. cfg.Client, cfg.Namespaces, and cfg.ReportStatus are required.
func New(cfg Config) (*Driver, error) {
	if cfg.WorkerID == "" {
		return nil, fmt.Errorf("k8sdriver: WorkerID is required")
	}
	if cfg.Client == nil {
		return nil, fmt.Errorf("k8sdriver: Client is required")
	}
	if len(cfg.Namespaces) == 0 {
		return nil, fmt.Errorf("k8sdriver: at least one namespace is required")
	}
	if cfg.ReportStatus == nil {
		return nil, fmt.Errorf("k8sdriver: ReportStatus is required")
	}
	if cfg.ObserveInterval <= 0 {
		cfg.ObserveInterval = 2 * time.Second
	}
	return &Driver{
		cfg:        cfg,
		lastStatus: make(map[string]string),
		logGens:    make(map[string]uint64),
		logCancels: make(map[string]context.CancelFunc),
	}, nil
}

// ProvisionVolume creates the PVC backing vol.
func (d *Driver) ProvisionVolume(ctx context.Context, vol *notebook.NotebookVolume, spec notebook.Notebook) error {
	if vol.ID == "" {
		return fmt.Errorf("k8sdriver: volume id is required")
	}
	ns := spec.K8sNamespace()
	if ns == "" {
		return fmt.Errorf("k8sdriver: driver.k8s.namespace is required")
	}
	if !slices.Contains(d.cfg.Namespaces, ns) {
		return fmt.Errorf("k8sdriver: namespace %q is not allowed", ns)
	}
	size := spec.StorageSize()
	if size == "" {
		return fmt.Errorf("k8sdriver: volume.size is required")
	}
	qty, err := resource.ParseQuantity(size)
	if err != nil {
		return fmt.Errorf("k8sdriver: invalid volume.size %q: %w", size, err)
	}

	pvc := &corev1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{
			Name:        notebookPVCName(vol.ID),
			Namespace:   ns,
			Labels:      d.k8sLabels("notebook-volume", vol.ID),
			Annotations: map[string]string{k8smanifest.AnnotationVolumeID: vol.ID},
		},
		Spec: corev1.PersistentVolumeClaimSpec{
			AccessModes: []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce},
			Resources: corev1.VolumeResourceRequirements{
				Requests: corev1.ResourceList{corev1.ResourceStorage: qty},
			},
		},
	}
	if sc := spec.StorageClass(); sc != "" {
		pvc.Spec.StorageClassName = &sc
	}
	if _, err := d.cfg.Client.CoreV1().PersistentVolumeClaims(ns).Create(ctx, pvc, metav1.CreateOptions{}); err != nil && !k8serrors.IsAlreadyExists(err) {
		return err
	}
	vol.WorkDir = notebook.ContainerWorkDir
	slog.Info("k8sdriver: notebook volume provisioned", "volume_id", vol.ID, "namespace", ns)
	return nil
}

// DeprovisionVolume deletes the PVC backing vol.
func (d *Driver) DeprovisionVolume(ctx context.Context, vol *notebook.NotebookVolume) error {
	if vol.ID == "" {
		return nil
	}
	ns, err := d.findVolumeNamespace(ctx, vol.ID)
	if err != nil {
		if k8serrors.IsNotFound(err) {
			return nil
		}
		return err
	}
	if err := d.cfg.Client.CoreV1().PersistentVolumeClaims(ns).Delete(ctx, notebookPVCName(vol.ID), metav1.DeleteOptions{}); err != nil && !k8serrors.IsNotFound(err) {
		return err
	}
	return nil
}

// Start creates (or updates) the notebook's StatefulSet and headless
// Service and returns immediately with status=starting — Manager never
// trusts Start's returned status for any runtime, K8s included; Observe
// reports the real "running" transition once the StatefulSet actually
// becomes ready.
func (d *Driver) Start(ctx context.Context, spec notebook.Notebook, vol *notebook.NotebookVolume, _ string) (*notebook.NotebookServer, error) {
	projectID := spec.Metadata.ProjectID
	name := spec.Metadata.Name
	if projectID == "" {
		return nil, fmt.Errorf("k8sdriver: project_id is required")
	}
	if name == "" {
		return nil, fmt.Errorf("k8sdriver: metadata.name is required")
	}
	if err := notebook.ValidateDirectPlacement(spec, "k8s"); err != nil {
		return nil, fmt.Errorf("k8sdriver: %w", err)
	}
	ns := spec.K8sNamespace()
	if ns == "" {
		return nil, fmt.Errorf("k8sdriver: driver.k8s.namespace is required")
	}
	if !slices.Contains(d.cfg.Namespaces, ns) {
		return nil, fmt.Errorf("k8sdriver: namespace %q is not allowed", ns)
	}

	resourceName := notebookWorkloadName(projectID, name)
	labels := d.k8sLabels("notebook", name)
	baseURL := fmt.Sprintf("/projects/%s/notebooks/%s/proxy/", projectID, name)
	token := uuid.NewString()
	workDir := vol.WorkDir
	if workDir == "" {
		workDir = notebook.ContainerWorkDir
	}
	replicas := int32(1)

	var podTemplate corev1.PodTemplateSpec
	if spec.Spec.Driver.K8s != nil {
		podTemplate = *spec.Spec.Driver.K8s.PodTemplate.DeepCopy()
	}

	var driverImage string
	if spec.Spec.Driver.K8s != nil {
		driverImage = spec.Spec.Driver.K8s.Image
	}
	image := resolveImage(driverImage, podTemplate)
	if image == "" {
		return nil, fmt.Errorf("k8sdriver: driver.k8s.image is required")
	}

	if podTemplate.Labels == nil {
		podTemplate.Labels = make(map[string]string)
	}
	for k, v := range labels {
		podTemplate.Labels[k] = v
	}

	nbIdx := containerIndex(podTemplate.Spec.Containers, "notebook")
	if nbIdx < 0 {
		podTemplate.Spec.Containers = append(podTemplate.Spec.Containers, corev1.Container{Name: "notebook"})
		nbIdx = len(podTemplate.Spec.Containers) - 1
	}
	c := &podTemplate.Spec.Containers[nbIdx]
	c.Image = image
	var res manifest.ResourceSpec
	if spec.Spec.Driver.K8s != nil {
		res = spec.Spec.Driver.K8s.Resources
	}
	resReqs, err := notebookResourceRequirements(res)
	if err != nil {
		return nil, err
	}
	c.Resources = resReqs
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
		c.Args = append(c.Args, baseCommand...)
	}
	c.Ports = []corev1.ContainerPort{{Name: "notebook", ContainerPort: 8888}}

	if !hasMountName(c.VolumeMounts, piperDataVolume) {
		c.VolumeMounts = append(c.VolumeMounts, corev1.VolumeMount{
			Name:      piperDataVolume,
			MountPath: notebook.ContainerWorkDir,
		})
	}
	if !hasVolumeName(podTemplate.Spec.Volumes, piperDataVolume) {
		podTemplate.Spec.Volumes = append(podTemplate.Spec.Volumes, corev1.Volume{
			Name: piperDataVolume,
			VolumeSource: corev1.VolumeSource{
				PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{
					ClaimName: notebookPVCName(vol.ID),
				},
			},
		})
	}

	ann := k8smanifest.WorkloadAnnotations(name)
	ann[k8smanifest.AnnotationProjectID] = projectID
	ann[k8smanifest.AnnotationVolumeID] = vol.ID
	sts := &appsv1.StatefulSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:        resourceName,
			Namespace:   ns,
			Labels:      labels,
			Annotations: ann,
		},
		Spec: appsv1.StatefulSetSpec{
			Replicas: &replicas,
			Selector: &metav1.LabelSelector{MatchLabels: labels},
			Template: podTemplate,
		},
	}

	if _, err := d.cfg.Client.AppsV1().StatefulSets(ns).Create(ctx, sts, metav1.CreateOptions{}); err != nil {
		if !k8serrors.IsAlreadyExists(err) {
			return nil, fmt.Errorf("k8sdriver: create statefulset: %w", err)
		}
		if err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
			existing, err := d.cfg.Client.AppsV1().StatefulSets(ns).Get(ctx, resourceName, metav1.GetOptions{})
			if err != nil {
				return err
			}
			existing.Spec.Replicas = sts.Spec.Replicas
			existing.Spec.Template = sts.Spec.Template
			if existing.Annotations == nil {
				existing.Annotations = make(map[string]string)
			}
			existing.Annotations[k8smanifest.AnnotationProjectID] = projectID
			existing.Annotations[k8smanifest.AnnotationVolumeID] = vol.ID
			_, err = d.cfg.Client.AppsV1().StatefulSets(ns).Update(ctx, existing, metav1.UpdateOptions{})
			return err
		}); err != nil {
			return nil, fmt.Errorf("k8sdriver: update statefulset: %w", err)
		}
	}

	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{Name: resourceName, Namespace: ns, Labels: labels},
		Spec: corev1.ServiceSpec{
			Selector:  labels,
			ClusterIP: "None",
			Ports:     []corev1.ServicePort{{Name: "notebook", Port: 8888}},
		},
	}
	if _, err := d.cfg.Client.CoreV1().Services(ns).Create(ctx, svc, metav1.CreateOptions{}); err != nil && !k8serrors.IsAlreadyExists(err) {
		return nil, fmt.Errorf("k8sdriver: create service: %w", err)
	}

	// Plain, directly reachable URL — no tunnel:// scheme. Requires Piper to
	// have cluster-internal network reachability to ns (see package doc).
	endpoint := fmt.Sprintf("http://%s.%s.svc.cluster.local:8888", resourceName, ns)
	return &notebook.NotebookServer{
		WorkerID: d.cfg.WorkerID,
		Token:    token,
		WorkDir:  workDir,
		Endpoint: endpoint,
	}, nil
}

// Stop scales the notebook's StatefulSet to zero replicas and cancels any
// active log stream. The final "stopped" status is reported asynchronously
// by Observe once ReadyReplicas reaches zero, matching Manager.Stop's
// contract (it only persists StatusStopping itself).
func (d *Driver) Stop(ctx context.Context, nb *notebook.NotebookServer) error {
	name := notebookWorkloadName(nb.ProjectID, nb.Name)
	key := notebookStatusKey(nb.ProjectID, nb.Name)

	d.logMu.Lock()
	if cancel, ok := d.logCancels[key]; ok {
		cancel()
		delete(d.logCancels, key)
	}
	d.logMu.Unlock()

	ns, err := d.findNotebookNamespace(ctx, name)
	if err != nil {
		if k8serrors.IsNotFound(err) {
			return nil
		}
		return err
	}
	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		current, err := d.cfg.Client.AppsV1().StatefulSets(ns).Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			if k8serrors.IsNotFound(err) {
				return nil
			}
			return err
		}
		zero := int32(0)
		current.Spec.Replicas = &zero
		_, err = d.cfg.Client.AppsV1().StatefulSets(ns).Update(ctx, current, metav1.UpdateOptions{})
		return err
	})
}

// Observe polls each configured namespace's notebook StatefulSets for
// status transitions and reports changes via cfg.ReportStatus. Call once at
// startup as a long-running background goroutine (see piper.go's
// runtimeObserver wiring for the established pattern) — independent of any
// connection lifecycle, matching the remote K8s worker's own Observe.
func (d *Driver) Observe(ctx context.Context) {
	ticker := time.NewTicker(d.cfg.ObserveInterval)
	defer ticker.Stop()
	d.observeOnce(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			d.observeOnce(ctx)
		}
	}
}

func (d *Driver) observeOnce(ctx context.Context) {
	for _, ns := range d.cfg.Namespaces {
		d.observeNamespace(ctx, ns)
	}
}

func (d *Driver) observeNamespace(ctx context.Context, ns string) {
	selector := k8smanifest.ManagedSelector() + "," + k8smanifest.LabelWorkloadKind + "=notebook"
	items, err := d.cfg.Client.AppsV1().StatefulSets(ns).List(ctx, metav1.ListOptions{LabelSelector: selector})
	if err != nil {
		return
	}
	for i := range items.Items {
		sts := &items.Items[i]
		name := sts.Annotations[k8smanifest.AnnotationWorkloadID]
		if name == "" {
			continue
		}
		projectID := sts.Annotations[k8smanifest.AnnotationProjectID]
		if projectID == "" {
			continue
		}
		status := observedStatefulSetStatus(sts)
		if status == "" || !d.statusChanged(notebookStatusKey(projectID, name), status) {
			continue
		}
		if err := d.cfg.ReportStatus(projectID, name, status, "", "", "", 0, ""); err != nil {
			slog.Warn("k8sdriver: report status failed", "name", name, "status", status, "err", err)
		}
		if status == notebook.StatusRunning {
			d.ensureNotebookLogStream(ctx, projectID, name, sts)
		} else {
			key := notebookStatusKey(projectID, name)
			d.logMu.Lock()
			if cancel, ok := d.logCancels[key]; ok {
				cancel()
				delete(d.logCancels, key)
			}
			d.logMu.Unlock()
		}
	}
}

func (d *Driver) statusChanged(key, status string) bool {
	d.statusMu.Lock()
	defer d.statusMu.Unlock()
	if d.lastStatus[key] == status {
		return false
	}
	d.lastStatus[key] = status
	return true
}

// ensureNotebookLogStream starts a PodLogs stream for a running notebook if
// one is not already active. A no-op when LogClient is not configured.
func (d *Driver) ensureNotebookLogStream(ctx context.Context, projectID, name string, sts *appsv1.StatefulSet) {
	if d.cfg.LogClient == nil {
		return
	}
	key := notebookStatusKey(projectID, name)
	d.logMu.Lock()
	if _, active := d.logCancels[key]; active {
		d.logMu.Unlock()
		return
	}
	streamCtx, cancel := context.WithCancel(ctx)
	d.logGens[key]++
	gen := d.logGens[key]
	d.logCancels[key] = cancel
	d.logMu.Unlock()

	ns := sts.Namespace
	podName := sts.Name + "-0" // StatefulSet pod naming convention
	sink := logsink.NewBufferedLogSink(projectID, d.cfg.LogClient)
	runID := "nb:" + name

	go func() {
		defer func() {
			sink.Stop()
			d.logMu.Lock()
			if d.logGens[key] == gen {
				delete(d.logCancels, key)
			}
			d.logMu.Unlock()
		}()
		req := d.cfg.Client.CoreV1().Pods(ns).GetLogs(podName, &corev1.PodLogOptions{Container: "notebook", Follow: true})
		rc, err := req.Stream(streamCtx)
		if err != nil {
			return
		}
		defer func() { _ = rc.Close() }()
		sc := bufio.NewScanner(rc)
		for sc.Scan() {
			sink.Append(runID, "runtime", "combined", sc.Text(), time.Now())
		}
	}()
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

func (d *Driver) findNotebookNamespace(ctx context.Context, name string) (string, error) {
	for _, ns := range d.cfg.Namespaces {
		if _, err := d.cfg.Client.AppsV1().StatefulSets(ns).Get(ctx, name, metav1.GetOptions{}); err == nil {
			return ns, nil
		} else if !k8serrors.IsNotFound(err) {
			return "", err
		}
	}
	return "", k8serrors.NewNotFound(appsv1.Resource("statefulsets"), name)
}

func (d *Driver) findVolumeNamespace(ctx context.Context, volumeID string) (string, error) {
	name := notebookPVCName(volumeID)
	for _, ns := range d.cfg.Namespaces {
		if _, err := d.cfg.Client.CoreV1().PersistentVolumeClaims(ns).Get(ctx, name, metav1.GetOptions{}); err == nil {
			return ns, nil
		} else if !k8serrors.IsNotFound(err) {
			return "", err
		}
	}
	return "", k8serrors.NewNotFound(corev1.Resource("persistentvolumeclaims"), name)
}

func (d *Driver) k8sLabels(kind, id string) map[string]string {
	return k8smanifest.WorkloadLabels(d.cfg.ClusterName, kind, id)
}

func notebookWorkloadName(projectID, name string) string {
	return "piper-nb-" + k8smanifest.SafeName(projectID) + "-" + k8smanifest.SafeName(name)
}

func notebookPVCName(volumeID string) string {
	clean := strings.ReplaceAll(volumeID, "-", "")
	if len(clean) > 12 {
		clean = clean[:12]
	}
	return "piper-nb-vol-" + clean
}

func notebookStatusKey(projectID, name string) string { return projectID + ":" + name }

// resolveImage returns the image to use for the notebook container.
// Priority: spec.k8s.image > pod_template notebook container image.
func resolveImage(driverImage string, tpl corev1.PodTemplateSpec) string {
	if driverImage != "" {
		return driverImage
	}
	idx := containerIndex(tpl.Spec.Containers, "notebook")
	if idx >= 0 && tpl.Spec.Containers[idx].Image != "" {
		return tpl.Spec.Containers[idx].Image
	}
	return ""
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

func notebookResourceRequirements(res manifest.ResourceSpec) (corev1.ResourceRequirements, error) {
	resReqs := corev1.ResourceRequirements{
		Requests: corev1.ResourceList{},
		Limits:   corev1.ResourceList{},
	}
	if res.CPU != "" {
		q, err := resource.ParseQuantity(res.CPU)
		if err != nil {
			return corev1.ResourceRequirements{}, fmt.Errorf("invalid k8s cpu %q: %w", res.CPU, err)
		}
		resReqs.Requests[corev1.ResourceCPU] = q
		resReqs.Limits[corev1.ResourceCPU] = q
	}
	if res.Memory != "" {
		q, err := resource.ParseQuantity(res.Memory)
		if err != nil {
			return corev1.ResourceRequirements{}, fmt.Errorf("invalid k8s memory %q: %w", res.Memory, err)
		}
		resReqs.Requests[corev1.ResourceMemory] = q
		resReqs.Limits[corev1.ResourceMemory] = q
	}
	if res.GPU != "" {
		q, err := resource.ParseQuantity(res.GPU)
		if err != nil {
			return corev1.ResourceRequirements{}, fmt.Errorf("invalid k8s gpu %q: %w", res.GPU, err)
		}
		resReqs.Limits["nvidia.com/gpu"] = q
	}
	return resReqs, nil
}

var _ notebook.Driver = (*Driver)(nil)
