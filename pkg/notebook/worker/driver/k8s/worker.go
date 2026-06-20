package notebookworker

import (
	"bufio"
	"context"
	"fmt"
	"slices"
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
	"github.com/piper/piper/internal/logsink"
	"github.com/piper/piper/pkg/internal/k8smeta"
	"github.com/piper/piper/pkg/notebook"
	"k8s.io/client-go/kubernetes"
)

type Dispatcher = *grpcagent.Dispatcher

type Config struct {
	WorkerID            string
	ClusterName         string
	Namespaces          []string
	Client              kubernetes.Interface
	InfrastructureImage string
	ReportStatus        func(notebook.WorkerStatusUpdate) error
	// LogClient enables pod log streaming. When set, running notebook pods
	// forward stdout/stderr to the master log store.
	LogClient logsink.PushClient
}

type Worker struct {
	cfg        Config
	statusMu   sync.Mutex
	lastStatus map[string]string
	logMu      sync.Mutex
	logGens    map[string]uint64             // workload key -> generation counter for the active log stream
	logCancels map[string]context.CancelFunc // workload key -> cancel function for PodLogs stream
}

func New(cfg Config) *Worker {
	return &Worker{
		cfg:        cfg,
		lastStatus: make(map[string]string),
		logGens:    make(map[string]uint64),
		logCancels: make(map[string]context.CancelFunc),
	}
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

func (a *Worker) listFiles(ctx context.Context, req notebook.FSListFilesRequest) (*notebook.FSListFilesResponse, error) {
	return a.listFilesK8s(ctx, req)
}

func (a *Worker) provisionNotebookVolume(ctx context.Context, req notebook.WorkerProvisionVolumeRequest) (*notebook.WorkerProvisionVolumeResponse, error) {
	if req.VolumeID == "" {
		return nil, fmt.Errorf("volume_id is required")
	}
	ns := req.Namespace
	if ns == "" {
		return nil, fmt.Errorf("namespace is required")
	}
	if !slices.Contains(a.cfg.Namespaces, ns) {
		return nil, fmt.Errorf("namespace %q is not allowed", ns)
	}
	size := req.StorageSize
	if size == "" {
		return nil, fmt.Errorf("storage_size is required")
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
	if req.StorageClass != "" {
		storageClass := req.StorageClass
		pvc.Spec.StorageClassName = &storageClass
	}
	if _, err := a.cfg.Client.CoreV1().PersistentVolumeClaims(ns).Create(ctx, pvc, metav1.CreateOptions{}); err != nil && !k8serrors.IsAlreadyExists(err) {
		return nil, err
	}
	return &notebook.WorkerProvisionVolumeResponse{WorkDir: notebook.ContainerWorkDir}, nil
}

func (a *Worker) startNotebook(ctx context.Context, req notebook.WorkerStartRequest) (*notebook.WorkerStartResponse, error) {
	if req.ProjectID == "" {
		return nil, fmt.Errorf("project_id is required")
	}
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

	ns := spec.K8sNamespace()
	if ns == "" {
		return nil, fmt.Errorf("notebook driver.k8s.namespace is required")
	}
	if !slices.Contains(a.cfg.Namespaces, ns) {
		return nil, fmt.Errorf("namespace %q is not allowed", ns)
	}
	name := notebookWorkloadName(req.ProjectID, spec.Metadata.Name)
	labels := a.k8sLabels("notebook", spec.Metadata.Name)
	baseURL := fmt.Sprintf("/notebooks/%s/proxy/", spec.Metadata.Name)
	token := uuid.NewString()
	workDir := req.WorkDir
	if workDir == "" {
		workDir = notebook.ContainerWorkDir
	}
	replicas := int32(1)

	var podTemplate corev1.PodTemplateSpec
	if spec.Spec.Driver.K8s != nil {
		podTemplate = *spec.Spec.Driver.K8s.PodTemplate.DeepCopy()
	}

	// ── Stage 3: apply piper-required fields (always last) ───────────────────

	// Resolve image only from the submitted notebook manifest.
	var driverImage string
	if spec.Spec.Driver.K8s != nil {
		driverImage = spec.Spec.Driver.K8s.Image
	}
	image := resolveImage(driverImage, podTemplate)
	if image == "" {
		return nil, fmt.Errorf("notebook driver.k8s.image is required")
	}

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

	ann := k8smeta.WorkloadAnnotations(spec.Metadata.Name)
	ann[k8smeta.AnnotationProjectID] = req.ProjectID
	ann[k8smeta.AnnotationVolumeID] = req.VolumeID
	sts := &appsv1.StatefulSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:        name,
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

	// Drain viewer and scale up StatefulSet inside the same volume lock so
	// no browse request can create a viewer between drain and scale-up.
	if err := a.drainViewerAndRun(ctx, ns, req.VolumeID, func(ctx context.Context) error {
		_, err := a.cfg.Client.AppsV1().StatefulSets(ns).Create(ctx, sts, metav1.CreateOptions{})
		if err != nil {
			if !k8serrors.IsAlreadyExists(err) {
				return err
			}
			return retry.RetryOnConflict(retry.DefaultRetry, func() error {
				existing, err := a.cfg.Client.AppsV1().StatefulSets(ns).Get(ctx, name, metav1.GetOptions{})
				if err != nil {
					return err
				}
				existing.Spec.Replicas = sts.Spec.Replicas
				existing.Spec.Template = sts.Spec.Template
				if existing.Annotations == nil {
					existing.Annotations = make(map[string]string)
				}
				existing.Annotations[k8smeta.AnnotationProjectID] = req.ProjectID
				existing.Annotations[k8smeta.AnnotationVolumeID] = req.VolumeID
				_, err = a.cfg.Client.AppsV1().StatefulSets(ns).Update(ctx, existing, metav1.UpdateOptions{})
				return err
			})
		}
		return nil
	}); err != nil {
		return nil, fmt.Errorf("start notebook: %w", err)
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
	// Cancel any active log stream for this notebook.
	key := notebookStatusKey(req.ProjectID, req.Name)
	a.logMu.Lock()
	if cancel, ok := a.logCancels[key]; ok {
		cancel()
		delete(a.logCancels, key)
	}
	a.logMu.Unlock()

	name := notebookWorkloadName(req.ProjectID, req.Name)
	ns, err := a.findNotebookNamespace(ctx, name)
	if err != nil {
		if k8serrors.IsNotFound(err) {
			return nil
		}
		return err
	}
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
	ns, err := a.findVolumeNamespace(ctx, req.VolumeID)
	if err != nil {
		if k8serrors.IsNotFound(err) {
			return nil
		}
		return err
	}
	err = a.cfg.Client.CoreV1().PersistentVolumeClaims(ns).Delete(ctx, notebookPVCName(req.VolumeID), metav1.DeleteOptions{})
	if err != nil && !k8serrors.IsNotFound(err) {
		return err
	}
	return nil
}

func (a *Worker) syncNotebookStatus(ctx context.Context, req notebook.WorkerSyncStatusRequest) (notebook.WorkerSyncStatusResponse, error) {
	statuses := make(map[string]string, len(req.Targets))
	for _, target := range req.Targets {
		name := target.Name
		key := notebookStatusKey(target.ProjectID, name)
		resourceName := notebookWorkloadName(target.ProjectID, name)
		ns, findErr := a.findNotebookNamespace(ctx, resourceName)
		if findErr != nil {
			statuses[key] = notebook.StatusFailed
			continue
		}
		sts, err := a.cfg.Client.AppsV1().StatefulSets(ns).Get(ctx, resourceName, metav1.GetOptions{})
		if err != nil {
			if k8serrors.IsNotFound(err) {
				statuses[key] = notebook.StatusFailed
			}
			continue
		}
		desired := int32(1)
		if sts.Spec.Replicas != nil {
			desired = *sts.Spec.Replicas
		}
		switch {
		case sts.Status.ReadyReplicas >= 1:
			statuses[key] = notebook.StatusRunning
		case sts.Status.ReadyReplicas == 0 && desired == 0:
			statuses[key] = notebook.StatusStopped
		}
	}
	return notebook.WorkerSyncStatusResponse{Statuses: statuses}, nil
}

// Observe reports Kubernetes-observed notebook state. It is independent of the
// master connection: failed pushes are recovered by the generic reconnect sync.
// drainViewerAndRun acquires the volume lock, removes any existing viewer Pod/Service,
// then runs fn while holding the lock. StatefulSet scale-up must be performed inside fn
// to guarantee no browse request creates a viewer between drain and notebook start.
func (a *Worker) drainViewerAndRun(ctx context.Context, ns, volumeID string, fn func(ctx context.Context) error) error {
	lockCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()

	return volumeLock(lockCtx, a.cfg.Client, ns, a.cfg.WorkerID, volumeID, func(ctx context.Context) error {
		mgr := newVolumeBrowserManager(a.cfg.Client, ns, a.cfg.WorkerID, a.cfg.InfrastructureImage)
		if mgr.ViewerExists(ctx, volumeID) {
			if err := mgr.Stop(ctx, volumeID); err != nil {
				return err
			}
			if err := mgr.WaitForDeletion(ctx, volumeID); err != nil {
				return err
			}
		}
		return fn(ctx)
	})
}

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
	for _, ns := range a.cfg.Namespaces {
		a.observeNamespace(ctx, ns)
	}
}

func (a *Worker) observeNamespace(ctx context.Context, ns string) {
	selector := k8smeta.ManagedSelector() + "," + k8smeta.LabelWorkloadKind + "=notebook"
	items, err := a.cfg.Client.AppsV1().StatefulSets(ns).List(ctx, metav1.ListOptions{
		LabelSelector: selector,
	})
	if err != nil {
		return
	}

	// Collect StatefulSets with desired replicas > 0 and their volume IDs.
	activeVolumeIDs := make(map[string]bool)
	for i := range items.Items {
		sts := &items.Items[i]
		desired := int32(0)
		if sts.Spec.Replicas != nil {
			desired = *sts.Spec.Replicas
		}
		if desired > 0 {
			if vid := sts.Annotations[k8smeta.AnnotationVolumeID]; vid != "" {
				activeVolumeIDs[vid] = true
			}
		}

		name := sts.Annotations[k8smeta.AnnotationWorkloadID]
		if name == "" {
			continue
		}
		projectID := sts.Annotations[k8smeta.AnnotationProjectID]
		if projectID == "" {
			continue
		}
		status := observedStatefulSetStatus(sts)
		if status == "" || !a.statusChanged(name, status) {
			continue
		}
		_ = a.cfg.ReportStatus(notebook.WorkerStatusUpdate{ProjectID: projectID, Name: name, Status: status})
		if status == notebook.StatusRunning {
			a.ensureNotebookLogStream(ctx, projectID, name, sts)
		} else {
			key := notebookStatusKey(projectID, name)
			a.logMu.Lock()
			if cancel, ok := a.logCancels[key]; ok {
				cancel()
				delete(a.logCancels, key)
			}
			a.logMu.Unlock()
		}
	}

	// Remove viewer pods that conflict with active notebooks.
	if len(activeVolumeIDs) > 0 {
		mgr := newVolumeBrowserManager(a.cfg.Client, ns, a.cfg.WorkerID, a.cfg.InfrastructureImage)
		for volumeID := range activeVolumeIDs {
			if mgr.ViewerExists(ctx, volumeID) {
				_ = mgr.Stop(ctx, volumeID)
			}
		}
	}

	// Run viewer idle/orphan cleanup.
	newVolumeBrowserManager(a.cfg.Client, ns, a.cfg.WorkerID, a.cfg.InfrastructureImage).Reconcile(ctx)
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

// ensureNotebookLogStream starts a PodLogs stream for a running notebook if
// one is not already active. The stream forwards each line to the master log
// store via LogClient. A no-op when LogClient is not configured.
func (a *Worker) ensureNotebookLogStream(ctx context.Context, projectID, name string, sts *appsv1.StatefulSet) {
	if a.cfg.LogClient == nil {
		return
	}
	key := notebookStatusKey(projectID, name)
	a.logMu.Lock()
	if _, active := a.logCancels[key]; active {
		a.logMu.Unlock()
		return
	}
	streamCtx, cancel := context.WithCancel(ctx)
	a.logGens[key]++
	gen := a.logGens[key]
	a.logCancels[key] = cancel
	a.logMu.Unlock()

	ns := sts.Namespace
	podName := sts.Name + "-0" // StatefulSet pod naming convention
	sink := logsink.NewGRPCLogSink(projectID, a.cfg.LogClient)
	runID := "nb:" + name

	go func() {
		defer func() {
			sink.Stop()
			a.logMu.Lock()
			if a.logGens[key] == gen {
				delete(a.logCancels, key)
			}
			a.logMu.Unlock()
		}()
		req := a.cfg.Client.CoreV1().Pods(ns).GetLogs(podName, &corev1.PodLogOptions{
			Container: "notebook",
			Follow:    true,
		})
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

func (a *Worker) findNotebookNamespace(ctx context.Context, name string) (string, error) {
	for _, ns := range a.cfg.Namespaces {
		if _, err := a.cfg.Client.AppsV1().StatefulSets(ns).Get(ctx, name, metav1.GetOptions{}); err == nil {
			return ns, nil
		} else if !k8serrors.IsNotFound(err) {
			return "", err
		}
	}
	return "", k8serrors.NewNotFound(appsv1.Resource("statefulsets"), name)
}

func (a *Worker) findVolumeNamespace(ctx context.Context, volumeID string) (string, error) {
	name := notebookPVCName(volumeID)
	for _, ns := range a.cfg.Namespaces {
		if _, err := a.cfg.Client.CoreV1().PersistentVolumeClaims(ns).Get(ctx, name, metav1.GetOptions{}); err == nil {
			return ns, nil
		} else if !k8serrors.IsNotFound(err) {
			return "", err
		}
	}
	return "", k8serrors.NewNotFound(corev1.Resource("persistentvolumeclaims"), name)
}

func (a *Worker) k8sLabels(kind, id string) map[string]string {
	return k8smeta.WorkloadLabels(a.cfg.ClusterName, kind, id)
}

func notebookWorkloadName(projectID, name string) string {
	return "piper-nb-" + k8smeta.SafeName(projectID) + "-" + k8smeta.SafeName(name)
}

func notebookPVCName(volumeID string) string {
	clean := strings.ReplaceAll(volumeID, "-", "")
	if len(clean) > 12 {
		clean = clean[:12]
	}
	return "piper-nb-vol-" + clean
}

func notebookStatusKey(projectID, name string) string {
	return projectID + ":" + name
}

// piperDataVolume is the fixed name for the piper-managed PVC volume and mount.
// Using a piper-prefixed name avoids collisions with user-defined volumes.
const piperDataVolume = "piper-data"

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
