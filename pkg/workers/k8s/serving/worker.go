package servingworker

import (
	"context"
	"fmt"

	"gopkg.in/yaml.v3"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"

	iagent "github.com/piper/piper/internal/agent"
	"github.com/piper/piper/internal/tunnel"
	"github.com/piper/piper/pkg/serving"
	"github.com/piper/piper/pkg/workers/k8s/internal/meta"
	"github.com/piper/piper/pkg/workload"
	"k8s.io/client-go/kubernetes"
)

type Dispatcher = *tunnel.Dispatcher

type Config struct {
	ClusterName string
	Namespaces  []string
	Client      kubernetes.Interface
	Namespace   string
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
	_ = tunnel.RegisterJSON(dispatcher, iagent.MethodServingDeploy, a.deployServing)
	_ = tunnel.RegisterJSON(dispatcher, iagent.MethodServingStop, func(ctx context.Context, req servingStopRequest) (any, error) {
		return nil, a.stopServing(ctx, req)
	})
	_ = tunnel.RegisterJSON(dispatcher, iagent.MethodServingRestart, func(ctx context.Context, req servingDeployRequest) (any, error) {
		var spec serving.ModelService
		if err := yaml.Unmarshal([]byte(req.YAML), &spec); err != nil {
			return nil, err
		}
		_ = a.stopServing(ctx, servingStopRequest{Name: spec.Metadata.Name})
		return a.deployServing(ctx, req)
	})
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
	rt := svc.Spec.Runtime
	if rt.Image == "" {
		return servingDeployResponse{}, fmt.Errorf("spec.runtime.image is required")
	}
	command := workload.ExpandArgs(rt.Command, map[string]string{
		"PIPER_MODEL_DIR":    req.S3URI,
		"PIPER_SERVICE_NAME": svc.Metadata.Name,
	})
	if len(command) == 0 {
		return servingDeployResponse{}, fmt.Errorf("spec.runtime.command is required")
	}
	if rt.Port == 0 {
		return servingDeployResponse{}, fmt.Errorf("spec.runtime.port is required")
	}

	ns := svc.Spec.K8s.Namespace
	if ns == "" {
		ns = a.servingNamespace()
	}
	replicas := int32(svc.Spec.K8s.Replicas)
	if replicas == 0 {
		replicas = 1
	}
	name := workload.SafeName(svc.Metadata.Name)
	labels := a.k8sLabels("serving", svc.Metadata.Name)

	resReqs := corev1.ResourceRequirements{
		Requests: corev1.ResourceList{},
		Limits:   corev1.ResourceList{},
	}
	if cpu, ok := svc.Spec.K8s.Resources["cpu"]; ok {
		q := resource.MustParse(cpu)
		resReqs.Requests[corev1.ResourceCPU] = q
		resReqs.Limits[corev1.ResourceCPU] = q
	}
	if mem, ok := svc.Spec.K8s.Resources["memory"]; ok {
		q := resource.MustParse(mem)
		resReqs.Requests[corev1.ResourceMemory] = q
		resReqs.Limits[corev1.ResourceMemory] = q
	}
	if gpu, ok := svc.Spec.K8s.Resources["gpu"]; ok {
		resReqs.Limits["nvidia.com/gpu"] = resource.MustParse(gpu)
	}

	deployment := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns, Labels: labels},
		Spec: appsv1.DeploymentSpec{
			Replicas: &replicas,
			Selector: &metav1.LabelSelector{MatchLabels: labels},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: labels},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{{
						Name:            "serving",
						Image:           rt.Image,
						ImagePullPolicy: corev1.PullPolicy(svc.Spec.K8s.ImagePullPolicy),
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
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns, Labels: labels},
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
	return nil
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
	return meta.Labels(a.cfg.ClusterName, kind, id)
}
