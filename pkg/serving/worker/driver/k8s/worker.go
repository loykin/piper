package servingworker

import (
	"context"
	"fmt"
	"sync"
	"time"

	"gopkg.in/yaml.v3"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"

	iagent "github.com/piper/piper/internal/agent"
	"github.com/piper/piper/internal/grpcagent"
	"github.com/piper/piper/pkg/internal/k8smeta"
	"github.com/piper/piper/pkg/serving"
	"github.com/piper/piper/pkg/workload"
	"k8s.io/client-go/kubernetes"
)

type Dispatcher = *grpcagent.Dispatcher

type Config struct {
	ClusterName  string
	Namespaces   []string
	Client       kubernetes.Interface
	Namespace    string
	ReportStatus func(serving.WorkerStatusUpdate) error
}

type Worker struct {
	cfg        Config
	statusMu   sync.Mutex
	lastStatus map[string]string
	namespaces map[string]struct{}
}

func New(cfg Config) *Worker {
	return &Worker{
		cfg:        cfg,
		lastStatus: make(map[string]string),
		namespaces: make(map[string]struct{}),
	}
}

func Register(dispatcher Dispatcher, cfg Config) *Worker {
	w := New(cfg)
	w.register(dispatcher)
	return w
}

type servingDeployRequest struct {
	YAML  string `json:"yaml"`
	S3URI string `json:"s3_uri"`
}

type servingDeployResponse struct {
	Endpoint  string `json:"endpoint"`
	Namespace string `json:"namespace"`
}

type servingStopRequest struct {
	Name      string `json:"name"`
	Namespace string `json:"namespace,omitempty"`
}

func (a *Worker) register(dispatcher Dispatcher) {
	_ = grpcagent.RegisterJSON(dispatcher, iagent.MethodServingDeploy, a.deployServing)
	_ = grpcagent.RegisterJSON(dispatcher, iagent.MethodServingStop, func(ctx context.Context, req servingStopRequest) (any, error) {
		return nil, a.stopServing(ctx, req)
	})
	_ = grpcagent.RegisterJSON(dispatcher, iagent.MethodServingSyncStatus, a.syncStatus)
}

func (a *Worker) deployServing(ctx context.Context, req servingDeployRequest) (servingDeployResponse, error) {
	if req.S3URI == "" {
		return servingDeployResponse{}, fmt.Errorf("s3_uri is required")
	}
	var svc serving.ModelService
	if err := yaml.Unmarshal([]byte(req.YAML), &svc); err != nil {
		return servingDeployResponse{}, err
	}
	if svc.Metadata.Name == "" {
		return servingDeployResponse{}, fmt.Errorf("metadata.name is required")
	}
	rt := svc.Spec.Run
	if svc.Spec.Driver.K8s == nil || svc.Spec.Driver.K8s.Image == "" {
		return servingDeployResponse{}, fmt.Errorf("spec.driver.k8s.image is required")
	}
	command := workload.ExpandArgs(rt.Command, map[string]string{
		"PIPER_MODEL_DIR":    req.S3URI,
		"PIPER_SERVICE_NAME": svc.Metadata.Name,
	})
	if len(command) == 0 {
		return servingDeployResponse{}, fmt.Errorf("spec.run.command is required")
	}
	if rt.Port == 0 {
		return servingDeployResponse{}, fmt.Errorf("spec.run.port is required")
	}

	var ns string
	if svc.Spec.Driver.K8s != nil {
		ns = svc.Spec.Driver.K8s.Namespace
	}
	if ns == "" {
		ns = a.servingNamespace()
	}
	a.rememberNamespace(ns)
	var replicas int32
	if svc.Spec.Driver.K8s != nil {
		replicas = int32(svc.Spec.Driver.K8s.Replicas)
	}
	if replicas == 0 {
		replicas = 1
	}
	name := workload.SafeName(svc.Metadata.Name)
	labels := a.k8sLabels("serving", svc.Metadata.Name)

	res := svc.Spec.Driver.Resources
	resReqs := corev1.ResourceRequirements{
		Requests: corev1.ResourceList{},
		Limits:   corev1.ResourceList{},
	}
	if res.CPU != "" {
		q := resource.MustParse(res.CPU)
		resReqs.Requests[corev1.ResourceCPU] = q
		resReqs.Limits[corev1.ResourceCPU] = q
	}
	if res.Memory != "" {
		q := resource.MustParse(res.Memory)
		resReqs.Requests[corev1.ResourceMemory] = q
		resReqs.Limits[corev1.ResourceMemory] = q
	}
	if res.GPU != "" {
		resReqs.Limits["nvidia.com/gpu"] = resource.MustParse(res.GPU)
	}
	var pullPolicy corev1.PullPolicy
	if svc.Spec.Driver.K8s != nil {
		pullPolicy = corev1.PullPolicy(svc.Spec.Driver.K8s.ImagePullPolicy)
	}

	deployment := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:        name,
			Namespace:   ns,
			Labels:      labels,
			Annotations: k8smeta.WorkloadAnnotations(svc.Metadata.Name),
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: &replicas,
			Selector: &metav1.LabelSelector{MatchLabels: labels},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: labels},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{{
						Name:            "serving",
						Image:           svc.Spec.Driver.K8s.Image,
						ImagePullPolicy: pullPolicy,
						Command:         []string{command[0]},
						Args:            command[1:],
						Resources:       resReqs,
						Env: []corev1.EnvVar{
							{Name: "PIPER_MODEL_DIR", Value: req.S3URI},
							{Name: "PIPER_SERVICE_NAME", Value: svc.Metadata.Name},
						},
						Ports: []corev1.ContainerPort{{ContainerPort: int32(rt.Port)}},
					}},
				},
			},
		},
	}

	apiDeployments := a.cfg.Client.AppsV1().Deployments(ns)
	if existing, err := apiDeployments.Get(ctx, name, metav1.GetOptions{}); err == nil {
		deployment.ResourceVersion = existing.ResourceVersion
		if _, err := apiDeployments.Update(ctx, deployment, metav1.UpdateOptions{}); err != nil {
			return servingDeployResponse{}, fmt.Errorf("update deployment: %w", err)
		}
	} else if k8serrors.IsNotFound(err) {
		if _, err := apiDeployments.Create(ctx, deployment, metav1.CreateOptions{}); err != nil {
			return servingDeployResponse{}, fmt.Errorf("create deployment: %w", err)
		}
	} else {
		return servingDeployResponse{}, fmt.Errorf("get deployment: %w", err)
	}

	k8sSvc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:        name,
			Namespace:   ns,
			Labels:      labels,
			Annotations: k8smeta.WorkloadAnnotations(svc.Metadata.Name),
		},
		Spec: corev1.ServiceSpec{
			Selector: labels,
			Ports: []corev1.ServicePort{
				{Port: int32(rt.Port), TargetPort: intstr.FromInt32(int32(rt.Port))},
			},
		},
	}
	apiServices := a.cfg.Client.CoreV1().Services(ns)
	if existing, err := apiServices.Get(ctx, name, metav1.GetOptions{}); err == nil {
		k8sSvc.ResourceVersion = existing.ResourceVersion
		k8sSvc.Spec.ClusterIP = existing.Spec.ClusterIP
		if _, err := apiServices.Update(ctx, k8sSvc, metav1.UpdateOptions{}); err != nil {
			return servingDeployResponse{}, fmt.Errorf("update service: %w", err)
		}
	} else if k8serrors.IsNotFound(err) {
		if _, err := apiServices.Create(ctx, k8sSvc, metav1.CreateOptions{}); err != nil {
			return servingDeployResponse{}, fmt.Errorf("create service: %w", err)
		}
	} else {
		return servingDeployResponse{}, fmt.Errorf("get service: %w", err)
	}

	return servingDeployResponse{
		Endpoint:  fmt.Sprintf("http://%s.%s.svc.cluster.local:%d", name, ns, rt.Port),
		Namespace: ns,
	}, nil
}

func (a *Worker) stopServing(ctx context.Context, req servingStopRequest) error {
	if req.Name == "" {
		return fmt.Errorf("name is required")
	}
	name := workload.SafeName(req.Name)
	ns := req.Namespace
	if ns == "" {
		ns = a.servingNamespace()
	}
	if err := a.cfg.Client.AppsV1().Deployments(ns).Delete(ctx, name, metav1.DeleteOptions{}); err != nil && !k8serrors.IsNotFound(err) {
		return err
	}
	if err := a.cfg.Client.CoreV1().Services(ns).Delete(ctx, name, metav1.DeleteOptions{}); err != nil && !k8serrors.IsNotFound(err) {
		return err
	}
	if a.cfg.ReportStatus != nil {
		a.statusChanged(req.Name, serving.StatusStopped)
		_ = a.cfg.ReportStatus(serving.WorkerStatusUpdate{Name: req.Name, Status: serving.StatusStopped})
	}
	return nil
}

func (a *Worker) syncStatus(ctx context.Context, req serving.WorkerSyncStatusRequest) (serving.WorkerSyncStatusResponse, error) {
	statuses := make(map[string]string, len(req.Services))
	for _, target := range req.Services {
		ns := target.Namespace
		if ns == "" {
			ns = a.servingNamespace()
		}
		a.rememberNamespace(ns)
		deployment, err := a.cfg.Client.AppsV1().Deployments(ns).Get(ctx, workload.SafeName(target.Name), metav1.GetOptions{})
		if err != nil {
			if k8serrors.IsNotFound(err) {
				statuses[target.Name] = serving.StatusStopped
			}
			continue
		}
		statuses[target.Name] = observedDeploymentStatus(deployment)
	}
	return serving.WorkerSyncStatusResponse{Statuses: statuses}, nil
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
	selector := k8smeta.ManagedSelector() + "," + k8smeta.LabelWorkloadKind + "=serving"
	namespaces := a.observedNamespaces()
	seenNamespaces := make(map[string]struct{}, len(namespaces))
	for _, namespace := range namespaces {
		if namespace == "" {
			continue
		}
		if _, seen := seenNamespaces[namespace]; seen {
			continue
		}
		seenNamespaces[namespace] = struct{}{}
		items, err := a.cfg.Client.AppsV1().Deployments(namespace).List(ctx, metav1.ListOptions{LabelSelector: selector})
		if err != nil {
			continue
		}
		for i := range items.Items {
			deployment := &items.Items[i]
			name := deployment.Annotations[k8smeta.AnnotationWorkloadID]
			status := observedDeploymentStatus(deployment)
			if name == "" || status == "" || !a.statusChanged(name, status) {
				continue
			}
			_ = a.cfg.ReportStatus(serving.WorkerStatusUpdate{Name: name, Status: status})
		}
	}
}

func (a *Worker) rememberNamespace(namespace string) {
	if namespace == "" {
		return
	}
	a.statusMu.Lock()
	a.namespaces[namespace] = struct{}{}
	a.statusMu.Unlock()
}

func (a *Worker) observedNamespaces() []string {
	a.statusMu.Lock()
	defer a.statusMu.Unlock()
	namespaces := make([]string, 0, len(a.namespaces)+len(a.cfg.Namespaces)+1)
	namespaces = append(namespaces, a.servingNamespace())
	namespaces = append(namespaces, a.cfg.Namespaces...)
	for namespace := range a.namespaces {
		namespaces = append(namespaces, namespace)
	}
	return namespaces
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

func observedDeploymentStatus(deployment *appsv1.Deployment) string {
	desired := int32(1)
	if deployment.Spec.Replicas != nil {
		desired = *deployment.Spec.Replicas
	}
	for _, condition := range deployment.Status.Conditions {
		if condition.Type == appsv1.DeploymentReplicaFailure && condition.Status == corev1.ConditionTrue {
			return serving.StatusFailed
		}
	}
	if desired > 0 && deployment.Status.ReadyReplicas >= desired {
		return serving.StatusRunning
	}
	return serving.StatusStarting
}

func (a *Worker) servingNamespace() string {
	if a.cfg.Namespace != "" {
		return a.cfg.Namespace
	}
	if len(a.cfg.Namespaces) > 0 && a.cfg.Namespaces[0] != "" {
		return a.cfg.Namespaces[0]
	}
	return "default"
}

func (a *Worker) k8sLabels(kind, id string) map[string]string {
	return k8smeta.WorkloadLabels(a.cfg.ClusterName, kind, id)
}
