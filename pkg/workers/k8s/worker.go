// Package k8sworker implements the cluster-local Kubernetes worker.
package k8sworker

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
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
	S3Endpoint           string
	S3AccessKey          string
	S3SecretKey          string
	S3Bucket             string
	S3UseSSL             bool
}

type Worker struct {
	cfg        Config
	client     *http.Client
	dispatcher *RPCDispatcher
}

func New(cfg Config) *Worker {
	a := &Worker{
		cfg:        cfg,
		client:     &http.Client{Timeout: 10 * time.Second},
		dispatcher: NewRPCDispatcher(),
	}
	if cfg.K8sClient != nil {
		k8snotebook.Register(a.dispatcher, k8snotebook.Config{
			AgentID:      cfg.ID,
			ClusterName:  cfg.ClusterName,
			Namespaces:   cfg.Namespaces,
			Client:       cfg.K8sClient,
			Namespace:    cfg.NotebookNamespace,
			Image:        cfg.NotebookImage,
			StorageClass: cfg.StorageClass,
			StorageSize:  cfg.StorageSize,
		})
		k8sserving.Register(a.dispatcher, k8sserving.Config{
			ClusterName: cfg.ClusterName,
			Namespaces:  cfg.Namespaces,
			Client:      cfg.K8sClient,
			Namespace:   cfg.ServingNamespace,
		})
		k8spipeline.Register(a.dispatcher, k8spipeline.Config{
			MasterURL:            cfg.MasterURL,
			Token:                cfg.Token,
			Namespaces:           cfg.Namespaces,
			Client:               cfg.K8sClient,
			Namespace:            cfg.PipelineNamespace,
			WorkerImage:          cfg.PipelineWorkerImage,
			AgentImagePullPolicy: cfg.AgentImagePullPolicy,
			DefaultImage:         cfg.DefaultImage,
			TTLAfterFinished:     cfg.TTLAfterFinished,
			S3Endpoint:           cfg.S3Endpoint,
			S3AccessKey:          cfg.S3AccessKey,
			S3SecretKey:          cfg.S3SecretKey,
			S3Bucket:             cfg.S3Bucket,
			S3UseSSL:             cfg.S3UseSSL,
		})
	}
	return a
}

func (a *Worker) Run(ctx context.Context) error {
	if a.cfg.MasterURL == "" {
		return fmt.Errorf("k8s worker: master url is required")
	}
	if a.cfg.ID == "" {
		return fmt.Errorf("k8s worker: id is required")
	}
	if a.cfg.ClusterName == "" {
		return fmt.Errorf("k8s worker: cluster name is required")
	}

	for {
		if err := a.register(ctx); err != nil {
			return err
		}
		if err := a.connectTunnel(ctx); err != nil && ctx.Err() == nil {
			slog.Warn("k8s worker tunnel disconnected", "err", err)
		}
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(5 * time.Second):
		}
	}
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
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(a.cfg.MasterURL, "/")+"/api/agents", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if a.cfg.Token != "" {
		req.Header.Set("Authorization", "Bearer "+a.cfg.Token)
	}
	resp, err := a.client.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("register k8s worker: master returned %d", resp.StatusCode)
	}
	return nil
}

func (a *Worker) connectTunnel(ctx context.Context) error {
	tunnelURL, err := BuildTunnelURL(a.cfg.MasterURL, a.cfg.ID)
	if err != nil {
		return err
	}
	opts := &websocket.DialOptions{}
	if a.cfg.Token != "" {
		opts.HTTPHeader = http.Header{"Authorization": []string{"Bearer " + a.cfg.Token}}
	}
	conn, _, err := websocket.Dial(ctx, tunnelURL, opts)
	if err != nil {
		return err
	}
	defer func() { _ = conn.Close(websocket.StatusNormalClosure, "") }()

	for {
		var frame tunnel.Frame
		if err := wsjson.Read(ctx, conn, &frame); err != nil {
			return err
		}
		if frame.Type != tunnel.FrameRPCRequest {
			continue
		}
		if err := wsjson.Write(ctx, conn, a.dispatcher.Handle(ctx, frame)); err != nil {
			return err
		}
	}
}

func BuildTunnelURL(masterURL, agentID string) (string, error) {
	u, err := url.Parse(masterURL)
	if err != nil {
		return "", err
	}
	switch u.Scheme {
	case "http":
		u.Scheme = "ws"
	case "https":
		u.Scheme = "wss"
	case "ws", "wss":
	default:
		return "", fmt.Errorf("unsupported master url scheme %q", u.Scheme)
	}
	u.Path = strings.TrimRight(u.Path, "/") + "/api/agents/" + agentID + "/tunnel"
	u.RawQuery = ""
	return u.String(), nil
}
