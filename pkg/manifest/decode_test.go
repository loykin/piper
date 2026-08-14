package manifest

import (
	"strings"
	"testing"
)

func TestDecodeStrictRejectsRemovedWorkerPlacement(t *testing.T) {
	var dst struct {
		Driver DriverSpec `yaml:"driver"`
	}
	err := DecodeStrict([]byte("driver:\n  placement:\n    worker: old-worker\n"), &dst)
	if err == nil || !strings.Contains(err.Error(), "field worker not found") {
		t.Fatalf("DecodeStrict() error = %v, want removed worker field rejection", err)
	}
}

func TestDecodeStrictRejectsUnknownK8sDriverField(t *testing.T) {
	var dst struct {
		Driver DriverSpec `yaml:"driver"`
	}
	err := DecodeStrict([]byte("driver:\n  k8s:\n    image: alpine\n    mystery: true\n"), &dst)
	if err == nil || !strings.Contains(err.Error(), "field mystery not found") {
		t.Fatalf("DecodeStrict() error = %v, want nested k8s field rejection", err)
	}
}

func TestDecodeStrictRejectsMultipleDocuments(t *testing.T) {
	var dst map[string]any
	err := DecodeStrict([]byte("name: one\n---\nname: two\n"), &dst)
	if err == nil || !strings.Contains(err.Error(), "multiple YAML documents") {
		t.Fatalf("DecodeStrict() error = %v, want multiple-document rejection", err)
	}
}

func TestValidateTypeMeta(t *testing.T) {
	for _, tc := range []struct {
		name string
		meta TypeMeta
		ok   bool
	}{
		{name: "in-process object without envelope", meta: TypeMeta{}, ok: true},
		{name: "canonical", meta: TypeMeta{APIVersion: APIVersionV1, Kind: "Pipeline"}, ok: true},
		{name: "partial", meta: TypeMeta{Kind: "Pipeline"}},
		{name: "wrong version", meta: TypeMeta{APIVersion: "piper/v2", Kind: "Pipeline"}},
		{name: "wrong kind", meta: TypeMeta{APIVersion: APIVersionV1, Kind: "Notebook"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateTypeMeta(tc.meta, "Pipeline")
			if (err == nil) != tc.ok {
				t.Fatalf("ValidateTypeMeta() error = %v, ok=%v", err, tc.ok)
			}
		})
	}
}

func TestRequireTypeMetaRejectsMissingEnvelope(t *testing.T) {
	if err := RequireTypeMeta(TypeMeta{}, "Pipeline"); err == nil {
		t.Fatal("missing envelope was accepted at a manifest boundary")
	}
}
