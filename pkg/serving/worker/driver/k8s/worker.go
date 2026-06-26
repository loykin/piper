package servingworker

import (
	"bufio"
	"context"
	"fmt"
	"slices"
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
	"github.com/piper/piper/internal/logsink"
	"github.com/piper/piper/internal/process"
	"github.com/piper/piper/pkg/manifest"
	k8smanifest "github.com/piper/piper/pkg/manifest/k8s"
	"github.com/piper/piper/pkg/serving"
	"k8s.io/client-go/kubernetes"
)

type Dispatcher = *grpcagent.Dispatcher

type Config struct {
	ClusterName  string
	Namespaces   []string
	Client       kubernetes.Interface
	ReportStatus func(serving.WorkerStatusUpdate) error
	// LogClient enables pod log streaming for serving deployments.
	LogClient logsink.PushClient
}

type Worker struct {
	cfg        Config
	statusMu   sync.Mutex
	lastStatus map[string]string
	namespaces map[string]struct{}
	logMu      sync.Mutex
	logGens    map[string]uint64
	logCancels map[string]context.CancelFunc
}

func New(cfg Config) *Worker {
	return &Worker{
		cfg:        cfg,
		lastStatus: make(map[string]string),
		namespaces: make(map[string]struct{}),
		logGens:    make(map[string]uint64),
		logCancels: make(map[string]context.CancelFunc),
	}
}

func Register(dispatcher Dispatcher, cfg Config) *Worker {
	w := New(cfg)
	w.register(dispatcher)
	return w
}

type servingDeployRequest struct {
	ProjectID string `json:"project_id"`
	YAML      string `json:"yaml"`
	S3URI     string `json:"s3_uri"`
}

type servingDeployResponse struct {
	Endpoint  string `json:"endpoint"`
	Namespace string `json:"namespace"`
}

type servingStopRequest struct {
	ProjectID string `json:"project_id"`
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
	if req.ProjectID == "" {
		return servingDeployResponse{}, fmt.Errorf("project_id is required")
	}
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
	command := process.ExpandArgs(rt.Command, map[string]string{
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
		return servingDeployResponse{}, fmt.Errorf("spec.driver.k8s.namespace is required")
	}
	if !slices.Contains(a.cfg.Namespaces, ns) {
		return servingDeployResponse{}, fmt.Errorf("namespace %q is not allowed", ns)
	}
	a.rememberNamespace(ns)
	var replicas int32
	if svc.Spec.Driver.K8s != nil {
		replicas = int32(svc.Spec.Driver.K8s.Replicas)
	}
	if replicas == 0 {
		replicas = 1
	}
	name := servingResourceName(req.ProjectID, svc.Metadata.Name)
	workloadID := servingKey(req.ProjectID, svc.Metadata.Name)
	labels := a.k8sLabels("serving", workloadID)
	annotations := k8smanifest.WorkloadAnnotations(svc.Metadata.Name)
	annotations[k8smanifest.AnnotationProjectID] = req.ProjectID

	var res manifest.ResourceSpec
	if svc.Spec.Driver.K8s != nil {
		res = svc.Spec.Driver.K8s.Resources
	}
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
			Annotations: annotations,
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
			Annotations: annotations,
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
	if req.ProjectID == "" {
		return fmt.Errorf("project_id is required")
	}
	if req.Name == "" {
		return fmt.Errorf("name is required")
	}
	name := servingResourceName(req.ProjectID, req.Name)
	ns := req.Namespace
	if ns == "" {
		return fmt.Errorf("namespace is required")
	}
	if err := a.cfg.Client.AppsV1().Deployments(ns).Delete(ctx, name, metav1.DeleteOptions{}); err != nil && !k8serrors.IsNotFound(err) {
		return err
	}
	if err := a.cfg.Client.CoreV1().Services(ns).Delete(ctx, name, metav1.DeleteOptions{}); err != nil && !k8serrors.IsNotFound(err) {
		return err
	}
	if a.cfg.ReportStatus != nil {
		a.statusChanged(servingKey(req.ProjectID, req.Name), serving.StatusStopped)
		_ = a.cfg.ReportStatus(serving.WorkerStatusUpdate{
			ProjectID: req.ProjectID,
			Name:      req.Name,
			Status:    serving.StatusStopped,
		})
	}
	return nil
}

func (a *Worker) syncStatus(ctx context.Context, req serving.WorkerSyncStatusRequest) (serving.WorkerSyncStatusResponse, error) {
	statuses := make(map[string]string, len(req.Services))
	for _, target := range req.Services {
		if target.ProjectID == "" || target.Name == "" {
			continue
		}
		key := servingKey(target.ProjectID, target.Name)
		ns := target.Namespace
		if ns == "" {
			continue
		}
		a.rememberNamespace(ns)
		deployment, err := a.cfg.Client.AppsV1().Deployments(ns).Get(ctx, servingResourceName(target.ProjectID, target.Name), metav1.GetOptions{})
		if err != nil {
			if k8serrors.IsNotFound(err) {
				statuses[key] = serving.StatusStopped
			}
			continue
		}
		statuses[key] = observedDeploymentStatus(deployment)
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
	selector := k8smanifest.ManagedSelector() + "," + k8smanifest.LabelWorkloadKind + "=serving"
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
			name := deployment.Annotations[k8smanifest.AnnotationWorkloadID]
			projectID := deployment.Annotations[k8smanifest.AnnotationProjectID]
			status := observedDeploymentStatus(deployment)
			if projectID == "" || name == "" || status == "" || !a.statusChanged(servingKey(projectID, name), status) {
				continue
			}
			_ = a.cfg.ReportStatus(serving.WorkerStatusUpdate{
				ProjectID: projectID,
				Name:      name,
				Status:    status,
			})
			if status == serving.StatusRunning {
				a.ensureServingLogStream(ctx, projectID, name, deployment)
			} else {
				key := servingKey(projectID, name)
				a.logMu.Lock()
				if cancel, ok := a.logCancels[key]; ok {
					cancel()
					delete(a.logCancels, key)
				}
				a.logMu.Unlock()
			}
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
	namespaces := make([]string, 0, len(a.namespaces)+len(a.cfg.Namespaces))
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
	if desired == 0 {
		return serving.StatusStopped
	}
	for _, condition := range deployment.Status.Conditions {
		if condition.Type == appsv1.DeploymentReplicaFailure && condition.Status == corev1.ConditionTrue {
			return serving.StatusFailed
		}
	}
	if deployment.Status.ReadyReplicas >= desired {
		return serving.StatusRunning
	}
	return serving.StatusStarting
}

// ensureServingLogStream starts a PodLogs stream for the first running pod of a
// serving Deployment. It is a no-op when LogClient is not configured or when a
// stream for the same key is already active.
func (a *Worker) ensureServingLogStream(ctx context.Context, projectID, name string, deployment *appsv1.Deployment) {
	if a.cfg.LogClient == nil {
		return
	}
	key := servingKey(projectID, name)
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

	sink := logsink.NewGRPCLogSink(projectID, a.cfg.LogClient)
	ns := deployment.Namespace
	labelSelector := metav1.FormatLabelSelector(deployment.Spec.Selector)
	runID := "svc:" + name

	go func() {
		defer func() {
			sink.Stop()
			a.logMu.Lock()
			if a.logGens[key] == gen {
				delete(a.logCancels, key)
			}
			a.logMu.Unlock()
		}()
		pods, err := a.cfg.Client.CoreV1().Pods(ns).List(streamCtx, metav1.ListOptions{
			LabelSelector: labelSelector,
		})
		if err != nil || len(pods.Items) == 0 {
			return
		}
		podName := pods.Items[0].Name
		req := a.cfg.Client.CoreV1().Pods(ns).GetLogs(podName, &corev1.PodLogOptions{
			Container: "serving",
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

func (a *Worker) k8sLabels(kind, id string) map[string]string {
	return k8smanifest.WorkloadLabels(a.cfg.ClusterName, kind, id)
}

func servingKey(projectID, name string) string {
	return projectID + ":" + name
}

func servingResourceName(projectID, name string) string {
	return k8smanifest.SafeName(projectID + "--" + name)
}
