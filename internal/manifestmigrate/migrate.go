package manifestmigrate

import (
	"context"
	"fmt"

	storemod "github.com/loykin/piper/internal/store"
	"github.com/loykin/piper/pkg/template"
)

// listAllLimit is passed as template.Filter.Limit to fetch every version of
// every pipeline template in a project — List defaults Limit<=0 to 50, which
// would silently under-scan a project with more templates/versions than that.
const listAllLimit = 1_000_000

// Kind identifies which manifest domain a Finding came from.
type Kind string

const (
	KindPipeline Kind = "pipeline"
	KindNotebook Kind = "notebook"
	KindServing  Kind = "serving"
)

// Finding is one stored manifest that still carries a non-empty
// placement.worker or placement.label field.
type Finding struct {
	Kind      Kind
	ProjectID string
	Name      string
	// Version is the pipeline template version the field was found in.
	// Zero for Notebook/ModelService, which are not versioned.
	Version int
	// Fixed is true once Apply successfully cleaned this finding. For
	// Kind==KindPipeline, "fixed" means a new, clean version was created —
	// the affected version itself is immutable and left as historical
	// record, matching how templates are already edited (every edit is a
	// new version, never an in-place rewrite).
	Fixed bool
	// NewVersion is the version number of the newly created clean template,
	// set only when Kind==KindPipeline and Fixed is true.
	NewVersion int
	// Err is set if Apply attempted to fix this finding and failed.
	Err error
}

// Scan reports every stored Pipeline template (latest version per name),
// Notebook, and ModelService manifest across all projects that still
// contains a non-empty placement.worker or placement.label field. It never
// modifies stored data.
func Scan(ctx context.Context, repos *storemod.Repos) ([]Finding, error) {
	return run(ctx, repos, false)
}

// Apply does what Scan does, and additionally cleans up every finding it
// reports: Notebook and ModelService records are rewritten in place;
// Pipeline templates are fixed by creating a new version with the field
// removed; the previous version is left untouched. Each returned Finding
// reflects whether its fix succeeded (Finding.Fixed) or failed
// (Finding.Err) — Apply itself only returns a non-nil error for failures
// that stop the scan outright (e.g. a project list failure).
func Apply(ctx context.Context, repos *storemod.Repos) ([]Finding, error) {
	return run(ctx, repos, true)
}

func run(ctx context.Context, repos *storemod.Repos, apply bool) ([]Finding, error) {
	projects, err := repos.Project.List(ctx)
	if err != nil {
		return nil, fmt.Errorf("list projects: %w", err)
	}

	var findings []Finding
	for _, p := range projects {
		pf, err := scanPipelines(ctx, repos, apply, p.ID)
		if err != nil {
			return nil, fmt.Errorf("project %s: scan pipeline templates: %w", p.ID, err)
		}
		findings = append(findings, pf...)

		nf, err := scanNotebooks(ctx, repos, apply, p.ID)
		if err != nil {
			return nil, fmt.Errorf("project %s: scan notebooks: %w", p.ID, err)
		}
		findings = append(findings, nf...)

		sf, err := scanServices(ctx, repos, apply, p.ID)
		if err != nil {
			return nil, fmt.Errorf("project %s: scan services: %w", p.ID, err)
		}
		findings = append(findings, sf...)
	}
	return findings, nil
}

// scanPipelines checks only the latest version of each (project, name)
// template group. An older, superseded version with a stale placement field
// can never be dispatched again except by explicitly re-submitting it as a
// new run, at which point the existing runtime validation (fed.md §13.6,
// ValidateDirectPlacement) already rejects it loudly — proactively fixing
// it here would mean silently minting new versions nobody asked for.
func scanPipelines(ctx context.Context, repos *storemod.Repos, apply bool, projectID string) ([]Finding, error) {
	templates, err := repos.PipelineTemplate.List(ctx, projectID, template.Filter{Limit: listAllLimit})
	if err != nil {
		return nil, err
	}

	latest := make(map[string]*template.Template, len(templates))
	for _, t := range templates {
		cur, ok := latest[t.Name]
		if !ok || t.Version > cur.Version {
			latest[t.Name] = t
		}
	}

	var findings []Finding
	for _, t := range latest {
		stripped, changed, err := StripPlacementWorkerLabel(t.YAML)
		if err != nil {
			return nil, fmt.Errorf("template %s/%s v%d: %w", projectID, t.Name, t.Version, err)
		}
		if !changed {
			continue
		}
		f := Finding{Kind: KindPipeline, ProjectID: projectID, Name: t.Name, Version: t.Version}
		if apply {
			nv, err := repos.PipelineTemplate.NextVersion(ctx, projectID, t.Name)
			if err != nil {
				f.Err = fmt.Errorf("next version: %w", err)
			} else {
				newT := &template.Template{
					ProjectID:   projectID,
					Name:        t.Name,
					Version:     nv,
					Description: t.Description,
					Tags:        t.Tags,
					YAML:        stripped,
					SnapshotID:  t.SnapshotID,
					VolumeID:    t.VolumeID,
				}
				if err := repos.PipelineTemplate.Create(ctx, newT); err != nil {
					f.Err = fmt.Errorf("create v%d: %w", nv, err)
				} else {
					f.Fixed = true
					f.NewVersion = nv
				}
			}
		}
		findings = append(findings, f)
	}
	return findings, nil
}

func scanNotebooks(ctx context.Context, repos *storemod.Repos, apply bool, projectID string) ([]Finding, error) {
	notebooks, err := repos.Notebook.List(ctx, projectID)
	if err != nil {
		return nil, err
	}
	var findings []Finding
	for _, nb := range notebooks {
		stripped, changed, err := StripPlacementWorkerLabel(nb.YAML)
		if err != nil {
			return nil, fmt.Errorf("notebook %s/%s: %w", projectID, nb.Name, err)
		}
		if !changed {
			continue
		}
		f := Finding{Kind: KindNotebook, ProjectID: projectID, Name: nb.Name}
		if apply {
			updated := *nb
			updated.YAML = stripped
			if err := repos.Notebook.Update(ctx, &updated); err != nil {
				f.Err = fmt.Errorf("update: %w", err)
			} else {
				f.Fixed = true
			}
		}
		findings = append(findings, f)
	}
	return findings, nil
}

func scanServices(ctx context.Context, repos *storemod.Repos, apply bool, projectID string) ([]Finding, error) {
	services, err := repos.Serving.List(ctx, projectID, 0, 0)
	if err != nil {
		return nil, err
	}
	var findings []Finding
	for _, svc := range services {
		stripped, changed, err := StripPlacementWorkerLabel(svc.YAML)
		if err != nil {
			return nil, fmt.Errorf("service %s/%s: %w", projectID, svc.Name, err)
		}
		if !changed {
			continue
		}
		f := Finding{Kind: KindServing, ProjectID: projectID, Name: svc.Name}
		if apply {
			updated := *svc
			updated.YAML = stripped
			if err := repos.Serving.Update(ctx, &updated); err != nil {
				f.Err = fmt.Errorf("update: %w", err)
			} else {
				f.Fixed = true
			}
		}
		findings = append(findings, f)
	}
	return findings, nil
}
