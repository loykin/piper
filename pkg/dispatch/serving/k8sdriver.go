package servingdispatch

import (
	"context"
	"fmt"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/client-go/kubernetes"

	"github.com/piper/piper/pkg/artifact"
	"github.com/piper/piper/pkg/serving"
	"github.com/piper/piper/pkg/workload"
)

// K8sDriver implements Driver by managing Kubernetes Deployments and Services.
type K8sDriver struct {
	k8s  kubernetes.Interface
	repo serving.Repository
}

// NewK8sDriver creates a K8sDriver backed by the given clientset.
func NewK8sDriver(k8s kubernetes.Interface, repo serving.Repository) *K8sDriver {
	return &K8sDriver{k8s: k8s, repo: repo}
}

// Deploy creates a Kubernetes Deployment and serving.Service for the given serving.ModelService spec.
func (d *K8sDriver) Deploy(ctx context.Context, spec serving.ModelService, art artifact.Resolved, yamlStr string) (*serving.Service, error) {
	if art.S3URI == "" {
		return nil, fmt.Errorf("k8s serving requires S3 artifact storage (configure S3.Bucket)")
	}
	return d.deployK8s(ctx, spec, art.S3URI, yamlStr)
}

// Stop deletes the Kubernetes Deployment and serving.Service for the given service record.
func (d *K8sDriver) Stop(ctx context.Context, svc *serving.Service) error {
	ns := svc.K8sNamespace()
	name := workload.SafeName(svc.Name)
	_ = d.k8s.AppsV1().Deployments(ns).Delete(ctx, name, metav1.DeleteOptions{})
	_ = d.k8s.CoreV1().Services(ns).Delete(ctx, name, metav1.DeleteOptions{})
	return nil
}

// Restart stops and re-deploys the service.
func (d *K8sDriver) Restart(ctx context.Context, spec serving.ModelService, art artifact.Resolved, yamlStr string) error {
	existing, err := d.repo.Get(ctx, spec.Metadata.Name)
	if err == nil && existing != nil {
		_ = d.Stop(ctx, existing)
	}
	_, err = d.Deploy(ctx, spec, art, yamlStr)
	return err
}

func (d *K8sDriver) deployK8s(ctx context.Context, svc serving.ModelService, s3URI string, _ string) (*serving.Service, error) {
	rt := svc.Spec.Runtime
	k := svc.Spec.K8s
	ns := k.Namespace
	if ns == "" {
		ns = "default"
	}
	replicas := int32(k.Replicas)
	if replicas == 0 {
		replicas = 1
	}

	envMap := map[string]string{
		"PIPER_MODEL_DIR":    s3URI,
		"PIPER_SERVICE_NAME": svc.Metadata.Name,
	}
	command := workload.ExpandArgs(rt.Command, envMap)

	resReqs := corev1.ResourceRequirements{
		Requests: corev1.ResourceList{},
		Limits:   corev1.ResourceList{},
	}
	if cpu, ok := k.Resources["cpu"]; ok {
		q := resource.MustParse(cpu)
		resReqs.Requests[corev1.ResourceCPU] = q
		resReqs.Limits[corev1.ResourceCPU] = q
	}
	if mem, ok := k.Resources["memory"]; ok {
		q := resource.MustParse(mem)
		resReqs.Requests[corev1.ResourceMemory] = q
		resReqs.Limits[corev1.ResourceMemory] = q
	}
	if gpu, ok := k.Resources["gpu"]; ok {
		q := resource.MustParse(gpu)
		resReqs.Limits["nvidia.com/gpu"] = q
	}

	name := workload.SafeName(svc.Metadata.Name)
	labels := map[string]string{
		"app.kubernetes.io/managed-by": "piper",
		"piper/service":                svc.Metadata.Name,
	}

	deployment := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns, Labels: labels},
		Spec: appsv1.DeploymentSpec{
			Replicas: &replicas,
			Selector: &metav1.LabelSelector{MatchLabels: labels},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: labels},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Name:            "serving",
							Image:           rt.Image,
							ImagePullPolicy: corev1.PullPolicy(k.ImagePullPolicy),
							Command:         []string{command[0]},
							Args:            command[1:],
							Resources:       resReqs,
							Env: []corev1.EnvVar{
								{Name: "PIPER_MODEL_DIR", Value: s3URI},
								{Name: "PIPER_SERVICE_NAME", Value: svc.Metadata.Name},
							},
							Ports: []corev1.ContainerPort{
								{ContainerPort: int32(rt.Port)},
							},
						},
					},
				},
			},
		},
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

	if _, err := d.k8s.AppsV1().Deployments(ns).Create(ctx, deployment, metav1.CreateOptions{}); err != nil {
		return nil, fmt.Errorf("create deployment: %w", err)
	}
	if _, err := d.k8s.CoreV1().Services(ns).Create(ctx, k8sSvc, metav1.CreateOptions{}); err != nil {
		return nil, fmt.Errorf("create service: %w", err)
	}

	artifactLabel := ""
	if svc.Spec.Model.FromArtifact != nil {
		artifactLabel = svc.Spec.Model.FromArtifact.Step + "/" + svc.Spec.Model.FromArtifact.Artifact
	} else if svc.Spec.Model.FromURI != "" {
		artifactLabel = svc.Spec.Model.FromURI
	}

	return &serving.Service{
		Name:      svc.Metadata.Name,
		Artifact:  artifactLabel,
		Endpoint:  fmt.Sprintf("http://%s.%s.svc.cluster.local:%d", name, ns, rt.Port),
		Namespace: ns,
		PID:       0,
		Status:    serving.StatusRunning,
	}, nil
}
