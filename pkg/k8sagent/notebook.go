package k8sagent

import (
	"context"
	"encoding/json"
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
	"github.com/piper/piper/pkg/notebook"
	"github.com/piper/piper/pkg/workload"
)

type notebookProvisionVolumeRequest struct {
	VolumeID    string `json:"volume_id"`
	StorageSize string `json:"storage_size,omitempty"`
}

type notebookProvisionVolumeResponse struct {
	WorkDir string `json:"work_dir"`
}

type notebookStartRequest struct {
	YAML      string `json:"yaml"`
	MasterURL string `json:"master_url"`
	WorkDir   string `json:"work_dir"`
	VolumeID  string `json:"volume_id"`
}

type notebookStartResponse struct {
	Token    string `json:"token"`
	WorkDir  string `json:"work_dir"`
	Endpoint string `json:"endpoint"`
}

type notebookStopRequest struct {
	Name string `json:"name"`
}

type notebookDeprovisionVolumeRequest struct {
	VolumeID string `json:"volume_id"`
}

type notebookSyncStatusRequest struct {
	Names []string `json:"names"`
}

type notebookSyncStatusResponse struct {
	Statuses map[string]string `json:"statuses"`
}

func registerNotebookHandlers(a *Agent) {
	_ = a.dispatcher.Register(iagent.MethodNotebookProvisionVolume, func(ctx context.Context, payload json.RawMessage) (any, error) {
		var req notebookProvisionVolumeRequest
		if err := json.Unmarshal(payload, &req); err != nil {
			return nil, err
		}
		return a.provisionNotebookVolume(ctx, req)
	})
	_ = a.dispatcher.Register(iagent.MethodNotebookStart, func(ctx context.Context, payload json.RawMessage) (any, error) {
		var req notebookStartRequest
		if err := json.Unmarshal(payload, &req); err != nil {
			return nil, err
		}
		return a.startNotebook(ctx, req)
	})
	_ = a.dispatcher.Register(iagent.MethodNotebookStop, func(ctx context.Context, payload json.RawMessage) (any, error) {
		var req notebookStopRequest
		if err := json.Unmarshal(payload, &req); err != nil {
			return nil, err
		}
		return nil, a.stopNotebook(ctx, req)
	})
	_ = a.dispatcher.Register(iagent.MethodNotebookDeprovision, func(ctx context.Context, payload json.RawMessage) (any, error) {
		var req notebookDeprovisionVolumeRequest
		if err := json.Unmarshal(payload, &req); err != nil {
			return nil, err
		}
		return nil, a.deprovisionNotebookVolume(ctx, req)
	})
	_ = a.dispatcher.Register(iagent.MethodNotebookSyncStatus, func(ctx context.Context, payload json.RawMessage) (any, error) {
		var req notebookSyncStatusRequest
		if err := json.Unmarshal(payload, &req); err != nil {
			return nil, err
		}
		return a.syncNotebookStatus(ctx, req)
	})
}

func (a *Agent) provisionNotebookVolume(ctx context.Context, req notebookProvisionVolumeRequest) (*notebookProvisionVolumeResponse, error) {
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
	if _, err := a.cfg.K8sClient.CoreV1().PersistentVolumeClaims(ns).Create(ctx, pvc, metav1.CreateOptions{}); err != nil && !k8serrors.IsAlreadyExists(err) {
		return nil, err
	}
	return &notebookProvisionVolumeResponse{WorkDir: "/home/jovyan"}, nil
}

func (a *Agent) startNotebook(ctx context.Context, req notebookStartRequest) (*notebookStartResponse, error) {
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
		image = a.cfg.NotebookImage
	}
	if image == "" {
		image = "jupyter/scipy-notebook:latest"
	}

	token := uuid.NewString()
	workDir := req.WorkDir
	if workDir == "" {
		workDir = "/home/jovyan"
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
						Args: []string{
							"start-notebook.py",
							"--ServerApp.base_url=" + baseURL,
							"--ServerApp.token=" + token,
							"--ServerApp.root_dir=/home/jovyan",
							"--ServerApp.allow_origin=*",
							"--no-browser",
							"--port=8888",
						},
						Ports: []corev1.ContainerPort{{Name: "notebook", ContainerPort: 8888}},
						VolumeMounts: []corev1.VolumeMount{{
							Name:      "data",
							MountPath: "/home/jovyan",
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
	if _, err := a.cfg.K8sClient.AppsV1().StatefulSets(ns).Create(ctx, sts, metav1.CreateOptions{}); err != nil {
		if !k8serrors.IsAlreadyExists(err) {
			return nil, err
		}
		existing, err := a.cfg.K8sClient.AppsV1().StatefulSets(ns).Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			return nil, err
		}
		existing.Spec.Replicas = sts.Spec.Replicas
		existing.Spec.Template = sts.Spec.Template
		if _, err := a.cfg.K8sClient.AppsV1().StatefulSets(ns).Update(ctx, existing, metav1.UpdateOptions{}); err != nil {
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
	if _, err := a.cfg.K8sClient.CoreV1().Services(ns).Create(ctx, svc, metav1.CreateOptions{}); err != nil && !k8serrors.IsAlreadyExists(err) {
		return nil, err
	}

	return &notebookStartResponse{
		Token:    token,
		WorkDir:  workDir,
		Endpoint: fmt.Sprintf("tunnel://%s/nb/%s", a.cfg.ID, spec.Metadata.Name),
	}, nil
}

func (a *Agent) stopNotebook(ctx context.Context, req notebookStopRequest) error {
	if req.Name == "" {
		return fmt.Errorf("name is required")
	}
	ns := a.notebookNamespace()
	name := notebookWorkloadName(req.Name)
	sts, err := a.cfg.K8sClient.AppsV1().StatefulSets(ns).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		if k8serrors.IsNotFound(err) {
			return nil
		}
		return err
	}
	zero := int32(0)
	sts.Spec.Replicas = &zero
	_, err = a.cfg.K8sClient.AppsV1().StatefulSets(ns).Update(ctx, sts, metav1.UpdateOptions{})
	return err
}

func (a *Agent) deprovisionNotebookVolume(ctx context.Context, req notebookDeprovisionVolumeRequest) error {
	if req.VolumeID == "" {
		return fmt.Errorf("volume_id is required")
	}
	err := a.cfg.K8sClient.CoreV1().PersistentVolumeClaims(a.notebookNamespace()).Delete(ctx, notebookPVCName(req.VolumeID), metav1.DeleteOptions{})
	if err != nil && !k8serrors.IsNotFound(err) {
		return err
	}
	return nil
}

func (a *Agent) syncNotebookStatus(ctx context.Context, req notebookSyncStatusRequest) (notebookSyncStatusResponse, error) {
	statuses := make(map[string]string, len(req.Names))
	for _, name := range req.Names {
		sts, err := a.cfg.K8sClient.AppsV1().StatefulSets(a.notebookNamespace()).Get(ctx, notebookWorkloadName(name), metav1.GetOptions{})
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
	return notebookSyncStatusResponse{Statuses: statuses}, nil
}

func (a *Agent) notebookNamespace() string {
	if a.cfg.NotebookNamespace != "" {
		return a.cfg.NotebookNamespace
	}
	if len(a.cfg.Namespaces) > 0 {
		return a.cfg.Namespaces[0]
	}
	return "default"
}

func (a *Agent) k8sLabels(kind, id string) map[string]string {
	return map[string]string{
		"app.kubernetes.io/managed-by": "piper",
		"piper.io/cluster":             a.cfg.ClusterName,
		"piper.io/workload-kind":       kind,
		"piper.io/workload-id":         id,
	}
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
