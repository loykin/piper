package piper

import "net/url"

// storageIdentity computes a short, deterministic, non-secret identity for
// a resolved storage URL: it changes if and only if the practical target
// changes (scheme + bucket/host/path) and never includes credentials,
// tokens, or other query-string secrets. It is the basis for the
// storage-backend stamp recorded on each Run and pipeline template version
// at write time (see pkg/pipeline/run.Run.StorageBackend and
// pkg/template.Template.StorageBackend) and compared against the live
// backend at read time to distinguish "artifact legitimately absent" from
// "artifact unreachable because the storage backend changed since this data
// was written" (precedent: MLflow's per-experiment artifact_location,
// Argo Workflows' artifactRepositoryRef).
//
// rawURL == "" means no object storage is configured at all — artifacts
// resolve directly against cfg.OutputDir on the local filesystem — which is
// reported as the constant "file", matching the "", "file" case below.
func storageIdentity(rawURL string) string {
	if rawURL == "" {
		return "file"
	}
	u, err := url.Parse(rawURL)
	if err != nil {
		return ""
	}
	switch u.Scheme {
	case "", "file":
		// Mirrors pkg/storage/helpers.go's Open(): file:///abs/path has
		// u.Host=="" and the root in u.Path; file://./rel treats a non-empty,
		// non-"localhost" Host as part of the root too.
		root := u.Path
		if u.Host != "" && u.Host != "localhost" {
			root = u.Host + u.Path
		}
		return "file:" + root
	case "s3", "gs", "azblob":
		// Confirmed against pkg/storage/s3store.go (openS3: bucket := u.Host)
		// and cloudstore.go (openCloud: blob.OpenBucket(ctx, rawURL) with
		// gocloud.dev/blob resolving gs://bucket and azblob://container from
		// u.Host) — the bucket/container name lives in u.Host for all three
		// schemes Piper supports, never in u.Path or the query string, so
		// credentials/tokens carried as query params (accessKey, secretKey,
		// serviceAccountKey, accountKey, …) never leak into the identity.
		return u.Scheme + ":" + u.Host
	case "http", "https":
		// No path, no query — some deployments put auth tokens in the query
		// string for an http(s) artifact endpoint; only scheme+host identify
		// the practical target without risking a leak.
		return u.Scheme + "://" + u.Host
	default:
		return u.Scheme
	}
}

// SystemSettings captures the current server-side capability state exposed to the UI.
type SystemSettings struct {
	ArtifactStore ArtifactStoreSettings `json:"artifact_store"`
	Runtime       RuntimeSettings       `json:"runtime"`
}

// RuntimeSettings identifies the direct in-process execution runtime this
// Piper server owns for pipeline, notebook, and serving workloads.
type RuntimeSettings struct {
	Type string `json:"type"`
}

// ArtifactStoreSettings describes whether artifact storage is usable.
type ArtifactStoreSettings struct {
	Status  string `json:"status"`            // enabled | disabled | unavailable
	Backend string `json:"backend,omitempty"` // s3 | file | http | https | gs | azblob
	Reason  string `json:"reason,omitempty"`  // only set when unavailable
}

// Settings returns the current server capability state.
func (p *Piper) Settings() SystemSettings {
	out := SystemSettings{ArtifactStore: ArtifactStoreSettings{Status: "disabled"}}
	if p == nil {
		return out
	}
	out.Runtime.Type = p.cfg.Runtime.Type
	if p.cfg.Storage.Disabled {
		out.ArtifactStore.Status = "disabled"
		out.ArtifactStore.Backend = storageScheme(p.cfg.Storage.URL)
		return out
	}
	if p.store != nil {
		out.ArtifactStore.Status = "enabled"
		out.ArtifactStore.Backend = storageScheme(p.storageURL)
		return out
	}
	out.ArtifactStore.Status = "unavailable"
	out.ArtifactStore.Backend = storageScheme(resolveStorageURL(p.cfg))
	if p.storageErr != nil {
		out.ArtifactStore.Reason = p.storageErr.Error()
	}
	return out
}

func storageScheme(raw string) string {
	if raw == "" {
		return ""
	}
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	return u.Scheme
}
