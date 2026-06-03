package piper

import "net/url"

// SystemSettings captures the current server-side capability state exposed to the UI.
type SystemSettings struct {
	ArtifactStore ArtifactStoreSettings `json:"artifact_store"`
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
	if p.cfg.Storage.Disabled {
		out.ArtifactStore.Status = "disabled"
		out.ArtifactStore.Backend = storageScheme(p.cfg.Storage.URL)
		if out.ArtifactStore.Backend == "" && p.cfg.S3.Bucket != "" {
			out.ArtifactStore.Backend = "s3"
		}
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
