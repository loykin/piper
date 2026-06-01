// Package k8sagent implements the cluster-local outbound agent.
package k8sagent

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
	PipelineAgentImage   string
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

type Agent struct {
	cfg        Config
	client     *http.Client
	dispatcher *RPCDispatcher
}

func New(cfg Config) *Agent {
	a := &Agent{
		cfg:        cfg,
		client:     &http.Client{Timeout: 10 * time.Second},
		dispatcher: NewRPCDispatcher(),
	}
	if cfg.K8sClient != nil {
		registerNotebookHandlers(a)
		registerServingHandlers(a)
		registerPipelineHandlers(a)
	}
	return a
}

func (a *Agent) Run(ctx context.Context) error {
	if a.cfg.MasterURL == "" {
		return fmt.Errorf("k8s agent: master url is required")
	}
	if a.cfg.ID == "" {
		return fmt.Errorf("k8s agent: id is required")
	}
	if a.cfg.ClusterName == "" {
		return fmt.Errorf("k8s agent: cluster name is required")
	}

	for {
		if err := a.register(ctx); err != nil {
			return err
		}
		if err := a.connectTunnel(ctx); err != nil && ctx.Err() == nil {
			slog.Warn("k8s agent tunnel disconnected", "err", err)
		}
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(5 * time.Second):
		}
	}
}

func (a *Agent) register(ctx context.Context) error {
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
		return fmt.Errorf("register k8s agent: master returned %d", resp.StatusCode)
	}
	return nil
}

func (a *Agent) connectTunnel(ctx context.Context) error {
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
