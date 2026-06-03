package notebook

import (
	"context"
	"fmt"
	"io"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

type PromotionTarget string

const (
	PromotionTargetDraft       PromotionTarget = "draft"
	PromotionTargetDownload    PromotionTarget = "download"
	PromotionTargetRepo        PromotionTarget = "repo"
	PromotionTargetObjectStore PromotionTarget = "object_store"
)

func ParsePromotionTarget(raw string) PromotionTarget {
	target := PromotionTarget(strings.TrimSpace(raw))
	if target == "" {
		return PromotionTargetDraft
	}
	switch target {
	case PromotionTargetDownload, PromotionTargetRepo, PromotionTargetObjectStore:
		return target
	default:
		return target
	}
}

func (t PromotionTarget) Valid() bool {
	switch t {
	case PromotionTargetDraft, PromotionTargetDownload, PromotionTargetRepo, PromotionTargetObjectStore:
		return true
	default:
		return false
	}
}

type PromotionValidation struct {
	Status   string   `json:"status"`
	Messages []string `json:"messages,omitempty"`
}

type PromotionSource struct {
	Type         string `json:"type"`
	NotebookName string `json:"notebook_name"`
	NotebookPath string `json:"notebook_path,omitempty"`
	GitSHA       string `json:"git_sha,omitempty"`
	RunID        string `json:"run_id,omitempty"`
	NotebookYAML string `json:"notebook_yaml,omitempty"`
}

type PromotionRuntime struct {
	WorkerID string `json:"worker_id,omitempty"`
	Backend  string `json:"backend,omitempty"`
	Mode     string `json:"mode,omitempty"`
	Image    string `json:"image,omitempty"`
	Env      string `json:"env,omitempty"`
	WorkDir  string `json:"work_dir,omitempty"`
	Endpoint string `json:"endpoint,omitempty"`
}

type PromotionPreview struct {
	Name       string              `json:"name"`
	Target     PromotionTarget     `json:"target"`
	Source     PromotionSource     `json:"source"`
	Runtime    PromotionRuntime    `json:"runtime"`
	Validation PromotionValidation `json:"validation"`
	Draft      string              `json:"draft"`
}

type PromotionExportResult struct {
	PromotionPreview
	BundlePath string    `json:"bundle_path,omitempty"`
	ObjectKey  string    `json:"object_key,omitempty"`
	ExportedAt time.Time `json:"exported_at"`
}

type PromotionExportRecord struct {
	PromotionExportResult
	ManifestPath string `json:"manifest_path,omitempty"`
}

type PromotionDownload struct {
	Name        string
	ContentType string
	Reader      io.ReadCloser
}

type PromotionRequest struct {
	Target PromotionTarget `json:"target"`
}

type PromotionService interface {
	Preview(ctx context.Context, name string, target PromotionTarget) (*PromotionPreview, error)
	Validate(ctx context.Context, name string, target PromotionTarget) (*PromotionValidation, error)
	Export(ctx context.Context, name string, target PromotionTarget) (*PromotionExportResult, error)
	ListExports(ctx context.Context, name string) ([]*PromotionExportRecord, error)
	DownloadExport(ctx context.Context, name, file string) (*PromotionDownload, error)
}

// BuildPromotionDraft returns a deterministic promotion draft for the notebook.
func BuildPromotionDraft(nb *NotebookServer, target PromotionTarget) string {
	if nb == nil {
		return ""
	}
	target = normalizePromotionTarget(target)
	yamlText := strings.TrimRight(nb.YAML, "\n")
	lines := []string{
		"promotion:",
		fmt.Sprintf("  name: %s", nb.Name),
		fmt.Sprintf("  target: %s", target),
		"  source:",
		"    type: notebook",
		fmt.Sprintf("    notebook_name: %s", nb.Name),
		fmt.Sprintf("    run_id: %s", nb.Name),
		"    notebook_yaml: |-",
	}
	if strings.TrimSpace(yamlText) == "" {
		lines = append(lines, "      ")
	} else {
		for _, line := range strings.Split(yamlText, "\n") {
			lines = append(lines, "      "+line)
		}
	}
	lines = append(lines,
		"  runtime:",
		fmt.Sprintf("    worker_id: %s", nb.WorkerID),
		fmt.Sprintf("    image: %s", nb.Image),
		fmt.Sprintf("    env: %s", nb.Env),
		fmt.Sprintf("    work_dir: %s", nb.WorkDir),
		fmt.Sprintf("    endpoint: %s", nb.Endpoint),
		"  artifacts: []",
		"  params: {}",
		"  variants: []",
	)
	return strings.Join(lines, "\n") + "\n"
}

func BuildPromotionPreview(nb *NotebookServer, target PromotionTarget, validation PromotionValidation, runtime PromotionRuntime) *PromotionPreview {
	if nb == nil {
		return nil
	}
	target = normalizePromotionTarget(target)
	return &PromotionPreview{
		Name:   nb.Name,
		Target: target,
		Source: PromotionSource{
			Type:         "notebook",
			NotebookName: nb.Name,
			RunID:        nb.Name,
			NotebookYAML: nb.YAML,
		},
		Runtime:    runtime,
		Validation: validation,
		Draft:      BuildPromotionDraft(nb, target),
	}
}

func ValidatePromotion(nb *NotebookServer, target PromotionTarget, artifactStoreEnabled bool) PromotionValidation {
	var out PromotionValidation
	if nb == nil {
		return PromotionValidation{Status: "error", Messages: []string{"notebook not found"}}
	}
	target = normalizePromotionTarget(target)
	if !target.Valid() {
		out.Status = "error"
		out.Messages = append(out.Messages, fmt.Sprintf("unsupported export target %q", target))
	}
	if target == PromotionTargetObjectStore && !artifactStoreEnabled {
		out.Status = "error"
		out.Messages = append(out.Messages, "artifact store is disabled")
	}
	if strings.TrimSpace(nb.YAML) == "" {
		out.Status = "error"
		out.Messages = append(out.Messages, "notebook YAML is missing")
		return out
	}
	var spec NotebookServerSpec
	if err := yaml.Unmarshal([]byte(nb.YAML), &spec); err != nil {
		out.Status = "error"
		out.Messages = append(out.Messages, fmt.Sprintf("invalid notebook YAML: %v", err))
	}
	if spec.Metadata.Name != "" && spec.Metadata.Name != nb.Name {
		if out.Status == "" {
			out.Status = "warning"
		}
		out.Messages = append(out.Messages, fmt.Sprintf("notebook metadata.name %q does not match record name %q", spec.Metadata.Name, nb.Name))
	}
	if spec.Spec.Prepare != nil {
		if err := spec.Spec.Prepare.Validate(); err != nil {
			out.Status = "error"
			out.Messages = append(out.Messages, fmt.Sprintf("invalid prepare spec: %v", err))
		}
	}
	if out.Status == "" {
		out.Status = "ok"
	}
	return out
}

func normalizePromotionTarget(target PromotionTarget) PromotionTarget {
	if target.Valid() {
		return target
	}
	return PromotionTargetDraft
}
