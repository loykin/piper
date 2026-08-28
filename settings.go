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
	case "gs":
		// gs://bucket — GCS bucket names are globally unique across all of
		// GCS (gocloud.dev/blob resolves the bucket from u.Host), so the
		// bucket name alone is a sufficient, unambiguous identity: two
		// different "gs://data" URLs are necessarily the same bucket.
		return "gs:" + u.Host
	case "s3":
		// s3://bucket?region=…&endpoint=…&s3ForcePathStyle=…&accessKey=…&secretKey=…
		// (pkg/storage/s3store.go's openS3). Unlike GCS, S3 bucket names are
		// only globally unique against AWS itself — an S3-*compatible*
		// custom endpoint (MinIO, R2, SeaweedFS, …) has its own separate
		// bucket namespace, so "s3://data" against one MinIO server and
		// "s3://data" against a completely different one must NOT collapse
		// to the same identity. Include endpoint and region (both routing
		// info, never secrets) alongside the bucket; accessKey/secretKey and
		// any other query param are deliberately excluded.
		q := u.Query()
		id := "s3:" + u.Host
		if endpoint := q.Get("endpoint"); endpoint != "" {
			id += "@" + endpoint
		}
		if region := q.Get("region"); region != "" {
			id += "#" + region
		}
		return id
	case "azblob":
		// azblob://container?accountName=…&accountKey=… (pkg/storage/
		// cloudstore.go's doc comment on openCloud). Container names are
		// only unique *within* a storage account, not globally — two
		// different accounts can each have a "data" container — so the
		// container alone (u.Host) is not a sufficient identity; accountName
		// (routing info) must be included, accountKey (secret) must not.
		q := u.Query()
		id := "azblob:" + u.Host
		if account := q.Get("accountName"); account != "" {
			id += "@" + account
		}
		return id
	case "http", "https":
		// Path included (but not the query string, where some deployments
		// put auth tokens): two different base paths on the same host are
		// genuinely different artifact roots, e.g. https://host/store-a vs
		// https://host/store-b must not collapse to the same identity.
		return u.Scheme + "://" + u.Host + u.Path
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
