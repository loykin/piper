package piper

import (
	"context"

	"github.com/loykin/piper/internal/projectcleanup"
)

// projectCleanupDeps assembles the projectcleanup.Deps used by the project
// deletion finalizer (see serve.go's WithBeforeDelete wiring) from the
// managers and repositories already owned by this *Piper.
func (p *Piper) projectCleanupDeps() projectcleanup.Deps {
	return projectcleanup.Deps{
		Notebooks:    p.notebookManager,
		NotebookRepo: p.repos.Notebook,
		Volumes:      p.repos.NotebookVolume,
		Services:     p.serving.manager,
		ServiceRepo:  p.repos.Serving,
		Runs:         p.runs,
		RunRepo:      p.repos.Run,
	}
}

// CleanupProjectLiveResources stops and deletes every live run, notebook,
// notebook volume, and service owned by projectID. Exported so it can be
// invoked directly (e.g. by tests or an operator tool) in addition to being
// wired as the project-delete finalizer.
func (p *Piper) CleanupProjectLiveResources(ctx context.Context, projectID string) error {
	return p.projectCleanupDeps().CleanupLiveResources(ctx, projectID)
}
