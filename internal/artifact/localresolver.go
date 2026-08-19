package artifact

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/loykin/piper/pkg/pipeline/run"
	"github.com/loykin/piper/pkg/project"
	"github.com/loykin/piper/pkg/storage"
)

// CacheDirName is where localResolver stages a local copy of a remote-store
// artifact for reuse across resolutions. Callers that sweep OutputDir for
// orphaned run directories must exclude this name.
const CacheDirName = "artifact-cache"

// localResolver is the default Resolver: it reads run records from runRepo
// and resolves local/S3/remote artifact addresses against the configured
// storage.Store and the per-run workspace under outputDir.
type localResolver struct {
	runRepo    run.Repository
	outputDir  string
	storageURL string        // resolved storage URL; empty means local-only
	store      storage.Store // nil when storage is disabled
}

// NewResolver returns the default Resolver implementation.
func NewResolver(runRepo run.Repository, outputDir, storageURL string, store storage.Store) Resolver {
	return &localResolver{
		runRepo:    runRepo,
		outputDir:  outputDir,
		storageURL: storageURL,
		store:      store,
	}
}

func (r *localResolver) Resolve(ctx context.Context, pipeline, step, artName, runRef string, target Target) (Resolved, error) {
	runID := runRef
	if runID == "latest" || runID == "" {
		projectContext, _ := project.FromContext(ctx)
		latest, err := r.runRepo.GetLatestSuccessful(ctx, projectContext.ID, pipeline)
		if err != nil {
			return Resolved{}, fmt.Errorf("lookup latest run for pipeline %q: %w", pipeline, err)
		}
		if latest == nil {
			return Resolved{}, fmt.Errorf("no successful run found for pipeline %q", pipeline)
		}
		runID = latest.ID
	}

	artKey := fmt.Sprintf("%s/%s/%s", runID, step, artName)

	switch target {
	case TargetS3:
		uri, err := r.artifactURI(artKey)
		if err != nil {
			return Resolved{}, err
		}
		return Resolved{RunID: runID, S3URI: uri}, nil
	case TargetRemote:
		resolved := Resolved{RunID: runID, ArtifactKey: artKey}
		if strings.HasPrefix(r.storageURL, "s3://") {
			uri, err := r.artifactURI(artKey)
			if err != nil {
				return Resolved{}, err
			}
			resolved.S3URI = uri
			resolved.RemoteURI = uri
		}
		if r.storageURL == "" {
			return Resolved{}, fmt.Errorf("remote artifact delivery requires storage")
		}
		return resolved, nil
	default:
		return r.resolveLocal(ctx, runID, step, artKey)
	}
}

// resolveLocal produces a local filesystem directory for artKey, preferring
// the artifact repository (Store) over the ephemeral per-run workspace
// (r.outputDir/runID/step) — the workspace is not guaranteed to survive
// artifactTTL cleanup, and with a non-local store the runner already deletes
// it right after upload (see pkg/pipeline/worker/agent's cleanWorkdir), so
// it cannot be treated as a durable copy (fed.md §13.6).
func (r *localResolver) resolveLocal(ctx context.Context, runID, step, artKey string) (Resolved, error) {
	if ls, ok := r.store.(*storage.LocalStore); ok {
		// Same host, same disk: the store already holds a durable copy under
		// this exact key — use it directly, no copy needed.
		return Resolved{RunID: runID, LocalPath: filepath.Join(ls.Root(), artKey)}, nil
	}
	if r.store != nil {
		// Remote store (S3/HTTP/cloud): stage a local copy once, under a
		// cache directory distinct from both the workspace and the store,
		// and reuse it on subsequent resolutions of the same artifact
		// instead of re-downloading every time.
		dest := filepath.Join(r.outputDir, CacheDirName, filepath.FromSlash(artKey))
		if _, err := os.Stat(dest); err == nil {
			return Resolved{RunID: runID, LocalPath: dest}, nil
		}
		if err := storage.DownloadDir(ctx, r.store, artKey+"/", dest); err != nil {
			return Resolved{}, fmt.Errorf("stage local copy of %s: %w", artKey, err)
		}
		return Resolved{RunID: runID, LocalPath: dest}, nil
	}
	// No store configured at all: only the raw workspace copy exists.
	return Resolved{RunID: runID, LocalPath: filepath.Join(r.outputDir, runID, step)}, nil
}

// artifactURI constructs a URI for the artifact key based on the configured storage.
func (r *localResolver) artifactURI(artKey string) (string, error) {
	if r.storageURL == "" {
		return "", fmt.Errorf("artifact URI requires a storage backend (configure storage.url or s3)")
	}
	u, err := url.Parse(r.storageURL)
	if err != nil {
		return "", err
	}
	switch u.Scheme {
	case "s3":
		return "s3://" + u.Host + "/" + artKey, nil
	case "http", "https":
		return "", fmt.Errorf("remote serving requires s3 storage; HTTP artifact storage is not supported")
	default:
		return "", fmt.Errorf("storage backend %q cannot provide artifact URIs for remote serving", u.Scheme)
	}
}
