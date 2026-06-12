package piper

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/piper/piper/internal/artifact"
	"github.com/piper/piper/pkg/project"
	"github.com/piper/piper/pkg/serving"
	"github.com/piper/piper/pkg/storage"
	"gopkg.in/yaml.v3"
)

// DeployService parses a ModelService YAML and deploys it.
func (p *Piper) DeployService(ctx context.Context, projectID string, yamlBytes []byte) (*serving.Service, error) {
	if projectID == "" {
		return nil, fmt.Errorf("project ID is required")
	}
	ctx = project.WithContext(ctx, project.Context{ID: projectID})
	return p.deployService(ctx, projectID, yamlBytes)
}

func (p *Piper) deployService(ctx context.Context, projectID string, yamlBytes []byte) (*serving.Service, error) {
	var svc serving.ModelService
	if err := yaml.Unmarshal(yamlBytes, &svc); err != nil {
		return nil, fmt.Errorf("parse ModelService YAML: %w", err)
	}
	if svc.Metadata.Name == "" {
		return nil, fmt.Errorf("ModelService metadata.name is required")
	}

	resolved, artifactLabel, err := p.resolveServiceModel(ctx, svc, p.serving.manager.ArtifactTarget())
	if err != nil {
		return nil, err
	}

	_ = p.serving.manager.Stop(ctx, projectID, svc.Metadata.Name)
	if err := p.serving.manager.Deploy(ctx, projectID, svc, resolved, string(yamlBytes)); err != nil {
		_ = p.repos.Serving.SetStatus(ctx, projectID, svc.Metadata.Name, serving.StatusFailed)
		return nil, fmt.Errorf("deploy service: %w", err)
	}

	rec, err := p.repos.Serving.Get(ctx, projectID, svc.Metadata.Name)
	if err != nil || rec == nil {
		return nil, fmt.Errorf("get service after deploy: %w", err)
	}
	rec.YAML = string(yamlBytes)
	rec.RunID = resolved.RunID
	if artifactLabel != "" {
		rec.Artifact = artifactLabel
	}
	if err := p.repos.Serving.Update(ctx, rec); err != nil {
		return nil, fmt.Errorf("update service record: %w", err)
	}
	return rec, nil
}

func (p *Piper) resolveServiceModel(ctx context.Context, svc serving.ModelService, target artifact.Target) (artifact.Resolved, string, error) {
	ref := svc.Spec.Model.FromArtifact
	if ref != nil {
		resolved, err := p.resolver.Resolve(ctx, ref.Pipeline, ref.Step, ref.Artifact, ref.Run, target)
		if err != nil {
			return artifact.Resolved{}, "", fmt.Errorf("resolve artifact: %w", err)
		}
		return resolved, ref.Step + "/" + ref.Artifact, nil
	}
	uri := strings.TrimSpace(svc.Spec.Model.FromURI)
	if uri == "" {
		return artifact.Resolved{}, "", fmt.Errorf("spec.model.from_artifact or spec.model.from_uri is required")
	}
	resolved, err := p.resolveModelURI(ctx, svc.Metadata.Name, uri, target)
	if err != nil {
		return artifact.Resolved{}, "", err
	}
	return resolved, uri, nil
}

func (p *Piper) resolveModelURI(ctx context.Context, serviceName, uri string, target artifact.Target) (artifact.Resolved, error) {
	if strings.HasPrefix(uri, "s3://") {
		if target == artifact.TargetS3 {
			return artifact.Resolved{S3URI: uri}, nil
		}
		if p.store == nil {
			return artifact.Resolved{}, fmt.Errorf("local serving from s3:// URI requires a storage backend")
		}
		dir := p.modelDir(serviceName)
		if err := os.MkdirAll(dir, 0755); err != nil {
			return artifact.Resolved{}, err
		}
		without := strings.TrimPrefix(uri, "s3://")
		slash := strings.IndexByte(without, '/')
		if slash < 0 {
			return artifact.Resolved{}, fmt.Errorf("invalid s3 URI %q: missing key", uri)
		}
		if err := storage.DownloadDir(ctx, p.store, without[slash+1:], dir); err != nil {
			return artifact.Resolved{}, fmt.Errorf("download s3 model: %w", err)
		}
		return artifact.Resolved{LocalPath: dir}, nil
	}
	if strings.HasPrefix(uri, "file://") {
		return artifact.Resolved{LocalPath: strings.TrimPrefix(uri, "file://")}, nil
	}
	if strings.HasPrefix(uri, "http://") || strings.HasPrefix(uri, "https://") {
		if target == artifact.TargetS3 {
			return artifact.Resolved{}, fmt.Errorf("k8s serving from http(s) URI requires an s3:// URI")
		}
		dir := p.modelDir(serviceName)
		if err := os.MkdirAll(dir, 0755); err != nil {
			return artifact.Resolved{}, err
		}
		dest := filepath.Join(dir, filepath.Base(strings.Split(uri, "?")[0]))
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, uri, nil)
		if err != nil {
			return artifact.Resolved{}, err
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return artifact.Resolved{}, err
		}
		defer func() { _ = resp.Body.Close() }()
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			return artifact.Resolved{}, fmt.Errorf("download model URI: status %d", resp.StatusCode)
		}
		out, err := os.Create(dest)
		if err != nil {
			return artifact.Resolved{}, err
		}
		defer func() { _ = out.Close() }()
		if _, err := io.Copy(out, resp.Body); err != nil {
			return artifact.Resolved{}, err
		}
		return artifact.Resolved{LocalPath: dir}, nil
	}
	return artifact.Resolved{LocalPath: uri}, nil
}

func (p *Piper) StopService(ctx context.Context, projectID, name string) error {
	return p.serving.manager.Stop(ctx, projectID, name)
}

func (p *Piper) RestartService(ctx context.Context, projectID, name string) error {
	rec, err := p.repos.Serving.Get(ctx, projectID, name)
	if err != nil || rec == nil {
		return fmt.Errorf("service %q not found", name)
	}
	if rec.YAML == "" {
		return fmt.Errorf("service %q has no stored YAML; cannot restart", name)
	}
	_, err = p.DeployService(ctx, projectID, []byte(rec.YAML))
	return err
}

func (p *Piper) ListServices(ctx context.Context) ([]*serving.Service, error) {
	projectContext, _ := project.FromContext(ctx)
	return p.repos.Serving.List(ctx, projectContext.ID)
}

func (p *Piper) GetService(ctx context.Context, name string) (*serving.Service, error) {
	projectContext, _ := project.FromContext(ctx)
	return p.repos.Serving.Get(ctx, projectContext.ID, name)
}
