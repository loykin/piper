package notebookworker

import (
	"context"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"gopkg.in/yaml.v3"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	iagent "github.com/piper/piper/internal/agent"
	"github.com/piper/piper/internal/tunnel"
	"github.com/piper/piper/pkg/notebook"
	"github.com/piper/piper/pkg/workers/k8s/internal/meta"
	"github.com/piper/piper/pkg/workload"
	"k8s.io/client-go/kubernetes"
)

type Dispatcher = *tunnel.Dispatcher

type Config struct {
	AgentID      string
	ClusterName  string
	Namespaces   []string
	Client       kubernetes.Interface
	Namespace    string
	Image        string
	StorageClass string
	StorageSize  string
}

type Worker struct {
	cfg Config
}

func New(cfg Config) *Worker {
	return &Worker{cfg: cfg}
}

func Register(dispatcher Dispatcher, cfg Config) {
	New(cfg).register(dispatcher)
}

func (a *Worker) register(dispatcher Dispatcher) {
	_ = tunnel.RegisterJSON(dispatcher, iagent.MethodNotebookProvisionVolume, a.provisionNotebookVolume)
	_ = tunnel.RegisterJSON(dispatcher, iagent.MethodNotebookStart, a.startNotebook)
	_ = tunnel.RegisterJSON(dispatcher, iagent.MethodNotebookStop, func(ctx context.Context, req notebook.WorkerStopRequest) (any, error) {
		return nil, a.stopNotebook(ctx, req)
	})
	_ = tunnel.RegisterJSON(dispatcher, iagent.MethodNotebookDeprovision, func(ctx context.Context, req notebook.WorkerDeprovisionVolumeRequest) (any, error) {
		return nil, a.deprovisionNotebookVolume(ctx, req)
	})
	_ = tunnel.RegisterJSON(dispatcher, iagent.MethodNotebookSyncStatus, a.syncNotebookStatus)
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
				"piper.io/volume-id": req.VolumeID,
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
	var spec notebook.NotebookServerSpec
	if err := yaml.Unmarshal([]byte(req.YAML), &spec); err != nil {
		return nil, fmt.Errorf("invalid notebook yaml: %w", err)
	}
	if spec.Metadata.Name == "" {
		return nil, fmt.Errorf("metadata.name is required")
	}

	ns := a.notebookNamespace()
	name := notebookWorkloadName(spec.Metadata.Name)
	image := spec.Spec.Image
	if image == "" {
		image = a.cfg.Image
	}
	if image == "" {
		image = "jupyter/scipy-notebook:latest"
	}

	token := uuid.NewString()
	workDir := req.WorkDir
	if workDir == "" {
		workDir = notebook.ContainerWorkDir
	}
	labels := a.k8sLabels("notebook", spec.Metadata.Name)
	baseURL := fmt.Sprintf("/notebooks/%s/proxy/", spec.Metadata.Name)
	replicas := int32(1)

	sts := &appsv1.StatefulSet{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns, Labels: labels},
		Spec: appsv1.StatefulSetSpec{
			Replicas: &replicas,
			Selector: &metav1.LabelSelector{MatchLabels: labels},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: labels},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{{
						Name:  "notebook",
						Image: image,
						Args:  notebook.JupyterStartArgs(baseURL, token, notebook.ContainerWorkDir, 8888),
						Ports: []corev1.ContainerPort{{Name: "notebook", ContainerPort: 8888}},
						VolumeMounts: []corev1.VolumeMount{{
							Name:      "data",
							MountPath: notebook.ContainerWorkDir,
						}},
					}},
					Volumes: []corev1.Volume{{
						Name: "data",
						VolumeSource: corev1.VolumeSource{
							PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{
								ClaimName: notebookPVCName(req.VolumeID),
							},
						},
					}},
				},
			},
		},
	}
	if _, err := a.cfg.Client.AppsV1().StatefulSets(ns).Create(ctx, sts, metav1.CreateOptions{}); err != nil {
		if !k8serrors.IsAlreadyExists(err) {
			return nil, err
		}
		existing, err := a.cfg.Client.AppsV1().StatefulSets(ns).Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			return nil, err
		}
		existing.Spec.Replicas = sts.Spec.Replicas
		existing.Spec.Template = sts.Spec.Template
		if _, err := a.cfg.Client.AppsV1().StatefulSets(ns).Update(ctx, existing, metav1.UpdateOptions{}); err != nil {
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

	return &notebook.WorkerStartResponse{
		Token:    token,
		WorkDir:  workDir,
		Endpoint: fmt.Sprintf("tunnel://%s/nb/%s", a.cfg.AgentID, spec.Metadata.Name),
	}, nil
}

func (a *Worker) stopNotebook(ctx context.Context, req notebook.WorkerStopRequest) error {
	if req.Name == "" {
		return fmt.Errorf("name is required")
	}
	ns := a.notebookNamespace()
	name := notebookWorkloadName(req.Name)
	sts, err := a.cfg.Client.AppsV1().StatefulSets(ns).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		if k8serrors.IsNotFound(err) {
			return nil
		}
		return err
	}
	zero := int32(0)
	sts.Spec.Replicas = &zero
	_, err = a.cfg.Client.AppsV1().StatefulSets(ns).Update(ctx, sts, metav1.UpdateOptions{})
	return err
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
	statuses := make(map[string]string, len(req.Names))
	for _, name := range req.Names {
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
	return meta.Labels(a.cfg.ClusterName, kind, id)
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
