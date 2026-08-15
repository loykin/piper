// Package k8sdriver implements serving.Driver directly in-process against
// a Kubernetes cluster — no remote runtime/tunnel involved, mirroring
// fed.md §13.2's Pipeline direct-runtime treatment and the notebook
// domain's pkg/notebook/dispatch/localdriver/k8s package's shape. Separate
// package from the docker/process serving localdriver for the same reason
// notebook's is: the remote K8s runtime never implemented the shared
// low-level servingdriver.Driver interface docker/process share.
//
// Unlike docker/baremetal direct-runtime (ArtifactTarget=TargetLocal, Piper
// resolves the model to a local path before Deploy runs), a K8s pod cannot
// reach Piper's local filesystem at all — this driver returns
// artifact.TargetRemote and, when the model comes from a stored artifact,
// replicates the same Secret + init-container ("/piper internal
// artifact-download") pattern so the pod fetches its own model bytes.
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

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/client-go/kubernetes"

	"github.com/loykin/piper/internal/artifact"
	"github.com/loykin/piper/internal/logsink"
	iprocess "github.com/loykin/piper/internal/process"
	"github.com/loykin/piper/pkg/manifest"
	k8smanifest "github.com/loykin/piper/pkg/manifest/k8s"
	"github.com/loykin/piper/pkg/serving"
)

// Config configures a direct, in-process K8s serving driver.
type Config struct {
	RuntimeID   string
	ClusterName string
	Namespaces  []string
	Client      kubernetes.Interface
	// ArtifactFetcherImage runs `/piper internal artifact-download` as an
	// init container when the model comes from a stored artifact. Reuse
	// the same image Pipeline's K8s runtime already uses for runner pods
	// (cfg.Runtime.K8s.PipelineRunnerImage) — it's the same piper binary.
	ArtifactFetcherImage string
	ArtifactPullPolicy   corev1.PullPolicy
	// WorkloadURL is the URL serving pods use to reach Piper's built-in
	// artifact endpoint when storage resolves to file:// — pods cannot
	// reach the host's local filesystem directly. Matches
	// cfg.Runtime.K8s.WorkloadURL, the same field Pipeline's K8s runtime uses.
	WorkloadURL string
	// WorkloadToken is used as the storage token fallback when rewriting a
	// file:// storage URL to WorkloadURL/store and no token was already
	// set — mirrors internal/k8sruntime/pipeline's taskStorageForK8sRuntime.
	WorkloadToken string
	LogClient     logsink.PushClient
	// ObserveInterval controls how often Observe polls Deployment status.
	// Zero means 2s.
	ObserveInterval time.Duration
	// ReportStatus mirrors pkg/serving/dispatch/localdriver.Config.ReportStatus's
	// signature exactly so piper.go can wire the same servingMgr.UpdateStatus
	// closure shape for every runtime.type.
	ReportStatus func(projectID, name, status, endpoint string) error
}

// Driver implements serving.Driver directly against a Kubernetes cluster.
// Call Observe once at startup (as a long-running background goroutine) to
// detect Deployment readiness transitions and stream pod logs. There is no
// Recover: Deployments/Services/Secrets are external K8s objects Observe
// rediscovers on its own on every tick (same reasoning as the notebook K8s
// driver; the remote K8s runtime doesn't implement Recoverable either).
type Driver struct {
	cfg Config

	storageMu    sync.RWMutex
	storageURL   string
	storageToken string

	statusMu   sync.Mutex
	lastStatus map[string]string

	logMu      sync.Mutex
	logGens    map[string]uint64
	logCancels map[string]context.CancelFunc
}

// New constructs a Driver. cfg.Client, cfg.Namespaces, and cfg.ReportStatus are required.
func New(cfg Config) (*Driver, error) {
	if cfg.RuntimeID == "" {
		return nil, fmt.Errorf("k8sdriver: RuntimeID is required")
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

// WithStorage sets the storage URL/token used to build the artifact-fetcher
// Secret. Piper's storage URL is only resolved partway through piper.New,
// after the driver itself is constructed — call this once that resolution
// completes, mirroring servingdispatch.AgentDriver.WithStorage's timing.
func (d *Driver) WithStorage(url, token string) *Driver {
	d.storageMu.Lock()
	d.storageURL, d.storageToken = url, token
	d.storageMu.Unlock()
	return d
}

func (d *Driver) storage() (url, token string) {
	d.storageMu.RLock()
	defer d.storageMu.RUnlock()
	return d.storageURL, d.storageToken
}

// ArtifactTarget is TargetRemote: a K8s pod cannot access Piper's local
// filesystem, so the model must be resolved to a fetchable
// key/URI (see internal/artifact.TargetRemote) and downloaded by the pod
// itself via an init container, not read from a local path Deploy is
// handed.
func (d *Driver) ArtifactTarget() artifact.Target { return artifact.TargetRemote }

// Deploy creates (or updates) the service's Deployment and Service, and —
// when the model comes from a stored artifact — an artifact-fetcher Secret
// and init container. Returns immediately with status=starting;
// serving.Manager.Deploy is fully synchronous and trusts this returned
// Status as-is (unlike notebook.Manager), so the real "running" transition
// is only ever reported later, asynchronously, by Observe.
func (d *Driver) Deploy(ctx context.Context, spec serving.ModelService, art artifact.Resolved, yamlStr string) (*serving.Service, error) {
	projectID := spec.Metadata.ProjectID
	name := spec.Metadata.Name
	rt := spec.Spec.Run
	if spec.Spec.Driver.K8s == nil || spec.Spec.Driver.K8s.Image == "" {
		return nil, fmt.Errorf("k8sdriver: driver.k8s.image is required")
	}
	if err := serving.ValidateDirectPlacement(spec, "k8s"); err != nil {
		return nil, fmt.Errorf("k8sdriver: %w", err)
	}

	modelDir := art.S3URI
	if modelDir == "" {
		modelDir = art.RemoteURI
	}
	downloadArtifact := art.ArtifactKey != ""
	if modelDir == "" && !downloadArtifact {
		return nil, fmt.Errorf("k8sdriver: artifact location is required")
	}

	command := iprocess.ExpandArgs(rt.Command, map[string]string{
		"PIPER_MODEL_DIR":    modelDir,
		"PIPER_SERVICE_NAME": name,
	})
	if len(command) == 0 {
		return nil, fmt.Errorf("k8sdriver: run.command is required")
	}
	if rt.Port == 0 {
		return nil, fmt.Errorf("k8sdriver: run.port is required")
	}

	ns := spec.Spec.Driver.K8s.Namespace
	if ns == "" {
		return nil, fmt.Errorf("k8sdriver: driver.k8s.namespace is required")
	}
	if !slices.Contains(d.cfg.Namespaces, ns) {
		return nil, fmt.Errorf("k8sdriver: namespace %q is not allowed", ns)
	}

	replicas := int32(spec.Spec.Driver.K8s.Replicas)
	if replicas == 0 {
		replicas = 1
	}
	resourceName := servingResourceName(projectID, name)
	artifactSecretName := resourceName + "-artifact"
	labels := d.k8sLabels("serving", servingKey(projectID, name))
	annotations := k8smanifest.WorkloadAnnotations(name)
	annotations[k8smanifest.AnnotationProjectID] = projectID

	resReqs, err := servingResourceRequirements(spec.Spec.Driver.K8s.Resources)
	if err != nil {
		return nil, err
	}

	podSpec := corev1.PodSpec{
		Containers: []corev1.Container{{
			Name:            "serving",
			Image:           spec.Spec.Driver.K8s.Image,
			ImagePullPolicy: corev1.PullPolicy(spec.Spec.Driver.K8s.ImagePullPolicy),
			Command:         []string{command[0]},
			Args:            command[1:],
			Resources:       resReqs,
			Env: []corev1.EnvVar{
				{Name: "PIPER_MODEL_DIR", Value: modelDir},
				{Name: "PIPER_SERVICE_NAME", Value: name},
			},
			Ports: []corev1.ContainerPort{{ContainerPort: int32(rt.Port)}},
		}},
	}
	if downloadArtifact {
		if d.cfg.ArtifactFetcherImage == "" {
			return nil, fmt.Errorf("k8sdriver: artifact fetcher image is required for stored artifacts")
		}
		storageURL, storageToken := d.storage()
		if strings.HasPrefix(storageURL, "file://") && d.cfg.WorkloadURL != "" {
			storageURL = strings.TrimRight(d.cfg.WorkloadURL, "/") + "/store"
			if storageToken == "" {
				storageToken = d.cfg.WorkloadToken
			}
		}
		if storageURL == "" {
			return nil, fmt.Errorf("k8sdriver: storage_url is required for stored artifacts")
		}
		if err := d.upsertArtifactSecret(ctx, ns, artifactSecretName, labels, storageURL, storageToken, art.ArtifactKey); err != nil {
			return nil, err
		}
		modelDir = "/piper-model"
		command = iprocess.ExpandArgs(rt.Command, map[string]string{
			"PIPER_MODEL_DIR":    modelDir,
			"PIPER_SERVICE_NAME": name,
		})
		podSpec.Containers[0].Command = []string{command[0]}
		podSpec.Containers[0].Args = command[1:]
		podSpec.Containers[0].Env[0].Value = modelDir
		podSpec.Containers[0].VolumeMounts = []corev1.VolumeMount{{Name: "model", MountPath: modelDir, ReadOnly: true}}
		podSpec.InitContainers = []corev1.Container{{
			Name:            "artifact-download",
			Image:           d.cfg.ArtifactFetcherImage,
			ImagePullPolicy: d.cfg.ArtifactPullPolicy,
			Command:         []string{"/piper"},
			Args:            []string{"internal", "artifact-download"},
			Env: []corev1.EnvVar{
				{Name: "PIPER_STORAGE_URL", ValueFrom: secretEnv(artifactSecretName, "storage-url")},
				{Name: "PIPER_STORAGE_TOKEN", ValueFrom: secretEnv(artifactSecretName, "storage-token")},
				{Name: "PIPER_ARTIFACT_KEY", ValueFrom: secretEnv(artifactSecretName, "artifact-key")},
				{Name: "PIPER_ARTIFACT_DEST", Value: modelDir},
			},
			VolumeMounts: []corev1.VolumeMount{{Name: "model", MountPath: modelDir}},
		}}
		podSpec.Volumes = []corev1.Volume{{Name: "model", VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{}}}}
	}

	deployment := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: resourceName, Namespace: ns, Labels: labels, Annotations: annotations},
		Spec: appsv1.DeploymentSpec{
			Replicas: &replicas,
			Selector: &metav1.LabelSelector{MatchLabels: labels},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: labels},
				Spec:       podSpec,
			},
		},
	}
	apiDeployments := d.cfg.Client.AppsV1().Deployments(ns)
	if existing, err := apiDeployments.Get(ctx, resourceName, metav1.GetOptions{}); err == nil {
		deployment.ResourceVersion = existing.ResourceVersion
		if _, err := apiDeployments.Update(ctx, deployment, metav1.UpdateOptions{}); err != nil {
			return nil, fmt.Errorf("k8sdriver: update deployment: %w", err)
		}
	} else if k8serrors.IsNotFound(err) {
		if _, err := apiDeployments.Create(ctx, deployment, metav1.CreateOptions{}); err != nil {
			return nil, fmt.Errorf("k8sdriver: create deployment: %w", err)
		}
	} else {
		return nil, fmt.Errorf("k8sdriver: get deployment: %w", err)
	}

	k8sSvc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{Name: resourceName, Namespace: ns, Labels: labels, Annotations: annotations},
		Spec: corev1.ServiceSpec{
			Selector: labels,
			Ports:    []corev1.ServicePort{{Port: int32(rt.Port), TargetPort: intstr.FromInt32(int32(rt.Port))}},
		},
	}
	apiServices := d.cfg.Client.CoreV1().Services(ns)
	if existing, err := apiServices.Get(ctx, resourceName, metav1.GetOptions{}); err == nil {
		k8sSvc.ResourceVersion = existing.ResourceVersion
		k8sSvc.Spec.ClusterIP = existing.Spec.ClusterIP
		if _, err := apiServices.Update(ctx, k8sSvc, metav1.UpdateOptions{}); err != nil {
			return nil, fmt.Errorf("k8sdriver: update service: %w", err)
		}
	} else if k8serrors.IsNotFound(err) {
		if _, err := apiServices.Create(ctx, k8sSvc, metav1.CreateOptions{}); err != nil {
			return nil, fmt.Errorf("k8sdriver: create service: %w", err)
		}
	} else {
		return nil, fmt.Errorf("k8sdriver: get service: %w", err)
	}

	return &serving.Service{
		Name:      name,
		ProjectID: projectID,
		Artifact:  artifactLabel(spec),
		Status:    serving.StatusStarting,
		Endpoint:  fmt.Sprintf("http://%s.%s.svc.cluster.local:%d", resourceName, ns, rt.Port),
		RuntimeID: d.cfg.RuntimeID,
		YAML:      yamlStr,
		Namespace: ns,
	}, nil
}

// Stop deletes the service's Deployment, Service, and artifact Secret (if
// any). The final "stopped" status is reported asynchronously by Observe,
// matching Manager.Stop's contract (it only persists StatusStopping itself).
func (d *Driver) Stop(ctx context.Context, svc *serving.Service) error {
	name := servingResourceName(svc.ProjectID, svc.Name)
	ns := svc.Namespace
	if ns == "" {
		return fmt.Errorf("k8sdriver: service namespace is required")
	}
	if err := d.cfg.Client.AppsV1().Deployments(ns).Delete(ctx, name, metav1.DeleteOptions{}); err != nil && !k8serrors.IsNotFound(err) {
		return err
	}
	if err := d.cfg.Client.CoreV1().Services(ns).Delete(ctx, name, metav1.DeleteOptions{}); err != nil && !k8serrors.IsNotFound(err) {
		return err
	}
	if err := d.cfg.Client.CoreV1().Secrets(ns).Delete(ctx, name+"-artifact", metav1.DeleteOptions{}); err != nil && !k8serrors.IsNotFound(err) {
		return err
	}
	return nil
}

func secretEnv(name, key string) *corev1.EnvVarSource {
	return &corev1.EnvVarSource{SecretKeyRef: &corev1.SecretKeySelector{LocalObjectReference: corev1.LocalObjectReference{Name: name}, Key: key}}
}

func (d *Driver) upsertArtifactSecret(ctx context.Context, namespace, name string, labels map[string]string, storageURL, storageToken, artifactKey string) error {
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace, Labels: labels},
		StringData: map[string]string{"storage-url": storageURL, "storage-token": storageToken, "artifact-key": artifactKey},
	}
	secrets := d.cfg.Client.CoreV1().Secrets(namespace)
	if existing, err := secrets.Get(ctx, name, metav1.GetOptions{}); err == nil {
		secret.ResourceVersion = existing.ResourceVersion
		if _, err := secrets.Update(ctx, secret, metav1.UpdateOptions{}); err != nil {
			return fmt.Errorf("k8sdriver: update artifact secret: %w", err)
		}
		return nil
	} else if !k8serrors.IsNotFound(err) {
		return fmt.Errorf("k8sdriver: get artifact secret: %w", err)
	}
	if _, err := secrets.Create(ctx, secret, metav1.CreateOptions{}); err != nil {
		return fmt.Errorf("k8sdriver: create artifact secret: %w", err)
	}
	return nil
}

// Observe polls each configured namespace's serving Deployments for status
// transitions and reports changes via cfg.ReportStatus. Call once at
// startup as a long-running background goroutine.
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
	selector := k8smanifest.ManagedSelector() + "," + k8smanifest.LabelWorkloadKind + "=serving"
	for _, ns := range d.cfg.Namespaces {
		items, err := d.cfg.Client.AppsV1().Deployments(ns).List(ctx, metav1.ListOptions{LabelSelector: selector})
		if err != nil {
			continue
		}
		for i := range items.Items {
			deployment := &items.Items[i]
			name := deployment.Annotations[k8smanifest.AnnotationWorkloadID]
			projectID := deployment.Annotations[k8smanifest.AnnotationProjectID]
			status := observedDeploymentStatus(deployment)
			if projectID == "" || name == "" || status == "" || !d.statusChanged(servingKey(projectID, name), status) {
				continue
			}
			if err := d.cfg.ReportStatus(projectID, name, status, ""); err != nil {
				slog.Warn("k8sdriver: report status failed", "name", name, "status", status, "err", err)
			}
			if status == serving.StatusRunning {
				d.ensureServingLogStream(ctx, projectID, name, deployment)
			} else {
				key := servingKey(projectID, name)
				d.logMu.Lock()
				if cancel, ok := d.logCancels[key]; ok {
					cancel()
					delete(d.logCancels, key)
				}
				d.logMu.Unlock()
			}
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

// ensureServingLogStream starts a PodLogs stream for the first running pod
// of a serving Deployment. A no-op when LogClient is not configured or a
// stream for the same key is already active.
func (d *Driver) ensureServingLogStream(ctx context.Context, projectID, name string, deployment *appsv1.Deployment) {
	if d.cfg.LogClient == nil {
		return
	}
	key := servingKey(projectID, name)
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

	sink := logsink.NewBufferedLogSink(projectID, d.cfg.LogClient)
	ns := deployment.Namespace
	labelSelector := metav1.FormatLabelSelector(deployment.Spec.Selector)
	runID := "svc:" + name

	go func() {
		defer func() {
			sink.Stop()
			d.logMu.Lock()
			if d.logGens[key] == gen {
				delete(d.logCancels, key)
			}
			d.logMu.Unlock()
		}()
		pods, err := d.cfg.Client.CoreV1().Pods(ns).List(streamCtx, metav1.ListOptions{LabelSelector: labelSelector})
		if err != nil || len(pods.Items) == 0 {
			return
		}
		podName := pods.Items[0].Name
		req := d.cfg.Client.CoreV1().Pods(ns).GetLogs(podName, &corev1.PodLogOptions{Container: "serving", Follow: true})
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

func (d *Driver) k8sLabels(kind, id string) map[string]string {
	return k8smanifest.WorkloadLabels(d.cfg.ClusterName, kind, id)
}

func servingKey(projectID, name string) string { return projectID + ":" + name }

func servingResourceName(projectID, name string) string {
	return k8smanifest.SafeName(projectID + "--" + name)
}

func artifactLabel(spec serving.ModelService) string {
	if spec.Spec.Model.FromArtifact != nil {
		return spec.Spec.Model.FromArtifact.Step + "/" + spec.Spec.Model.FromArtifact.Artifact
	}
	return spec.Spec.Model.FromURI
}

func servingResourceRequirements(res manifest.ResourceSpec) (corev1.ResourceRequirements, error) {
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

var _ serving.Driver = (*Driver)(nil)
