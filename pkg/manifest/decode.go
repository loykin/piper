package manifest

import (
	"bytes"
	"fmt"
	"io"

	"gopkg.in/yaml.v3"
)

const APIVersionV1 = "piper/v1"

// DecodeStrict decodes exactly one YAML document and rejects unknown fields.
// Every user-submitted Piper manifest must enter through this function.
func DecodeStrict(data []byte, dst any) error {
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(dst); err != nil {
		return err
	}
	var trailing any
	if err := dec.Decode(&trailing); err != io.EOF {
		if err == nil {
			return fmt.Errorf("multiple YAML documents are not supported")
		}
		return err
	}
	return nil
}

// RequireTypeMeta requires the canonical envelope shared by every Piper
// manifest accepted at an API or execution boundary.
func RequireTypeMeta(meta TypeMeta, kind string) error {
	if meta.APIVersion == "" || meta.Kind == "" {
		return fmt.Errorf("apiVersion and kind are required")
	}
	if meta.APIVersion != APIVersionV1 {
		return fmt.Errorf("unsupported apiVersion %q", meta.APIVersion)
	}
	if meta.Kind != kind {
		return fmt.Errorf("kind must be %q, got %q", kind, meta.Kind)
	}
	return nil
}

// ValidateTypeMeta validates an envelope when one is present. Domain objects
// assembled in-process may omit it; parsers must call RequireTypeMeta first.
func ValidateTypeMeta(meta TypeMeta, kind string) error {
	if meta.APIVersion == "" && meta.Kind == "" {
		return nil
	}
	return RequireTypeMeta(meta, kind)
}
