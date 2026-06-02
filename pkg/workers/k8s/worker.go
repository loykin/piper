// Package k8sworker implements the cluster-local Kubernetes worker.
package k8sworker

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/client-go/kubernetes"

	iagent "github.com/piper/piper/internal/agent"
	"github.com/piper/piper/internal/tunnel"
	k8snotebook "github.com/piper/piper/pkg/workers/k8s/notebook"
	k8spipeline "github.com/piper/piper/pkg/workers/k8s/pipeline"
	k8sserving "github.com/piper/piper/pkg/workers/k8s/serving"
)

type Config struct {
	MasterURL   string
	Token       string
	ID          string
	ClusterName string
	Namespaces  []string
	K8sClient   kubernetes.Interface

	NotebookNamespace    string
	ServingNamespace     string
	PipelineNamespace    string
	NotebookImage        string
	PipelineWorkerImage  string
	StorageClass         string
	StorageSize          string
	AgentImagePullPolicy string
	DefaultImage         string
	TTLAfterFinished     *int32
	// StorageURL selects the artifact store backend for pipeline jobs.
	StorageURL string
	// PodDefaults are cluster-wide defaults applied to every notebook pod.
	PodDefaults corev1.PodTemplateSpec
}

type Worker struct {
	cfg    Config
	client *tunnel.Client
}

func New(cfg Config) *Worker {
	a := &Worker{
		cfg: cfg,
		client: tunnel.NewClient(tunnel.ClientConfig{
			MasterURL: cfg.MasterURL,
			AgentID:   cfg.ID,
			Token:     cfg.Token,
		}),
	}
	if cfg.K8sClient != nil {
		k8snotebook.Register(a.client.Dispatcher(), k8snotebook.Config{
			AgentID:      cfg.ID,
			ClusterName:  cfg.ClusterName,
			Namespaces:   cfg.Namespaces,
			Client:       cfg.K8sClient,
			Namespace:    cfg.NotebookNamespace,
			Image:        cfg.NotebookImage,
			StorageClass: cfg.StorageClass,
			StorageSize:  cfg.StorageSize,
			PodDefaults:  cfg.PodDefaults,
		})
		k8sserving.Register(a.client.Dispatcher(), k8sserving.Config{
			ClusterName: cfg.ClusterName,
			Namespaces:  cfg.Namespaces,
			Client:      cfg.K8sClient,
			Namespace:   cfg.ServingNamespace,
		})
		k8spipeline.Register(a.client.Dispatcher(), k8spipeline.Config{
			MasterURL:            cfg.MasterURL,
			Token:                cfg.Token,
			Namespaces:           cfg.Namespaces,
			Client:               cfg.K8sClient,
			Namespace:            cfg.PipelineNamespace,
			WorkerImage:          cfg.PipelineWorkerImage,
			AgentImagePullPolicy: cfg.AgentImagePullPolicy,
			DefaultImage:         cfg.DefaultImage,
			TTLAfterFinished:     cfg.TTLAfterFinished,
			StorageURL:           cfg.StorageURL,
		})
	}
	return a
}

func (a *Worker) Run(ctx context.Context) error {
	if a.cfg.ClusterName == "" {
		return fmt.Errorf("k8s worker: cluster name is required")
	}
	return a.client.Run(ctx, a.register)
}

func (a *Worker) register(ctx context.Context) error {
	capabilities := []string{iagent.CapabilityK8s, iagent.CapabilityTunnel}
	if a.cfg.K8sClient != nil {
		capabilities = append(capabilities, iagent.CapabilityNotebook, iagent.CapabilityServing, iagent.CapabilityPipeline)
	}
	body, err := json.Marshal(iagent.Info{
		ID:           a.cfg.ID,
		Kind:         iagent.KindK8s,
		Capabilities: capabilities,
		ClusterName:  a.cfg.ClusterName,
		Namespaces:   a.cfg.Namespaces,
	})
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		strings.TrimRight(a.cfg.MasterURL, "/")+"/api/agents",
		bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if a.cfg.Token != "" {
		req.Header.Set("Authorization", "Bearer "+a.cfg.Token)
	}
	c := &http.Client{Timeout: 10 * time.Second}
	resp, err := c.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("register k8s worker: master returned %d", resp.StatusCode)
	}
	return nil
}

// BuildTunnelURL is kept for backward compatibility with tests.
// Prefer tunnel.TunnelURL directly.
func BuildTunnelURL(masterURL, agentID string) (string, error) {
	return tunnel.TunnelURL(masterURL, agentID)
}
