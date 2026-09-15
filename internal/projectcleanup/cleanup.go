// Package projectcleanup stops and removes every live run, notebook,
// notebook volume, and service owned by a project before that project's row
// is allowed to be deleted.
//
// pkg/project.Handler's BeforeDelete hook already behaves like a Kubernetes
// finalizer: it runs synchronously before the project row is removed, and if
// it returns an error the delete is aborted and the row survives untouched.
// Without this package, deleting a project only purged its stats — the
// notebooks/services/runs tables have ON DELETE CASCADE FKs to projects(id),
// so their rows were silently wiped from the DB while the real k8s/docker/
// baremetal workloads behind them kept running, orphaned. CleanupLiveResources
// fills that gap; the CASCADE FKs remain in place as a defense-in-depth
// backstop, not the primary mechanism.
package projectcleanup

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/loykin/piper/internal/runlifecycle"
	"github.com/loykin/piper/pkg/notebook"
	"github.com/loykin/piper/pkg/pipeline/run"
	"github.com/loykin/piper/pkg/project"
	"github.com/loykin/piper/pkg/serving"
)

// Deps holds everything CleanupLiveResources needs to reach live resources.
type Deps struct {
	Notebooks    *notebook.Manager
	NotebookRepo notebook.Repository
	Volumes      notebook.VolumeRepository
	Services     *serving.Manager
	ServiceRepo  serving.Repository
	Runs         *runlifecycle.Manager
	RunRepo      run.Repository
}

// CleanupLiveResources stops and deletes every live run, notebook, notebook
// volume, and service owned by projectID. It must fully succeed before the
// caller is allowed to remove the project row — this is Piper's equivalent
// of a Kubernetes finalizer: cleanup runs first, and any failure here blocks
// the project deletion rather than letting it silently orphan compute.
func (d Deps) CleanupLiveResources(ctx context.Context, projectID string) error {
	ctx = project.WithContext(ctx, project.Context{ID: projectID})

	if err := d.cleanupRuns(ctx, projectID); err != nil {
		return fmt.Errorf("projectcleanup: runs: %w", err)
	}
	if err := d.cleanupNotebooks(ctx, projectID); err != nil {
		return fmt.Errorf("projectcleanup: notebooks: %w", err)
	}
	if err := d.cleanupServices(ctx, projectID); err != nil {
		return fmt.Errorf("projectcleanup: services: %w", err)
	}
	return nil
}

// cleanupRuns cancels every non-terminal run owned by projectID and then
// deletes every run (terminal and just-canceled) along with its artifacts,
// so nothing is left for the ON DELETE CASCADE FK to silently wipe. A
// Cancel failure is best-effort/non-fatal — Queue.Cancel already treats
// remote cancellation as best-effort — so it is logged and cleanup
// continues; a failure listing runs or deleting them is fatal.
func (d Deps) cleanupRuns(ctx context.Context, projectID string) error {
	if d.Runs == nil || d.RunRepo == nil {
		return nil
	}
	runs, err := d.RunRepo.List(ctx, projectID, run.RunFilter{})
	if err != nil {
		return fmt.Errorf("list runs: %w", err)
	}
	if len(runs) == 0 {
		return nil
	}
	ids := make([]string, 0, len(runs))
	for _, r := range runs {
		ids = append(ids, r.ID)
		if r.Status == run.StatusRunning || r.Status == run.StatusScheduled {
			if err := d.Runs.CancelRun(ctx, r.ID); err != nil {
				slog.Warn("projectcleanup: cancel run failed", "project_id", projectID, "run_id", r.ID, "err", err)
			}
		}
	}
	if err := d.Runs.DeleteRunsWithArtifacts(ctx, ids); err != nil {
		return fmt.Errorf("delete runs with artifacts: %w", err)
	}
	return nil
}

// cleanupNotebooks deletes every notebook owned by projectID (stopping any
// still running) and then purges every volume left in "released" status.
// A notebook Delete failure (e.g. driver.Stop erroring) aborts and
// propagates — this is the deliberate "finalizer blocks on real failure"
// behavior: an operator must know cleanup didn't finish rather than have
// the project silently disappear out from under a still-running notebook.
func (d Deps) cleanupNotebooks(ctx context.Context, projectID string) error {
	if d.Notebooks == nil || d.NotebookRepo == nil {
		return nil
	}
	notebooks, err := d.NotebookRepo.List(ctx, projectID)
	if err != nil {
		return fmt.Errorf("list notebooks: %w", err)
	}
	for _, nb := range notebooks {
		if err := d.Notebooks.Delete(ctx, projectID, nb.Name); err != nil {
			return fmt.Errorf("delete notebook %q: %w", nb.Name, err)
		}
	}

	if d.Volumes == nil {
		return nil
	}
	volumes, err := d.Volumes.List(ctx, projectID, 0, 0)
	if err != nil {
		return fmt.Errorf("list notebook volumes: %w", err)
	}
	for _, vol := range volumes {
		if vol.Status != notebook.VolumeStatusReleased {
			slog.Warn("projectcleanup: skipping notebook volume not in released status",
				"project_id", projectID, "volume_id", vol.ID, "status", vol.Status)
			continue
		}
		if err := d.Notebooks.PurgeVolume(ctx, projectID, vol.ID); err != nil {
			return fmt.Errorf("purge notebook volume %q: %w", vol.ID, err)
		}
	}
	return nil
}

// cleanupServices deletes every service owned by projectID (stopping any
// still running). A failure aborts and propagates, same reasoning as
// cleanupNotebooks.
func (d Deps) cleanupServices(ctx context.Context, projectID string) error {
	if d.Services == nil || d.ServiceRepo == nil {
		return nil
	}
	services, err := d.ServiceRepo.List(ctx, projectID, 0, 0)
	if err != nil {
		return fmt.Errorf("list services: %w", err)
	}
	for _, svc := range services {
		if err := d.Services.Delete(ctx, projectID, svc.Name); err != nil {
			return fmt.Errorf("delete service %q: %w", svc.Name, err)
		}
	}
	return nil
}
