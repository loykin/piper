package mlflow

import (
	"strings"
	"testing"
)

func TestEncodeParams_ScalarCanonicalEncoding(t *testing.T) {
	got := EncodeParams(map[string]any{
		"epochs":   float64(10),
		"lr":       float64(0.001),
		"name":     "resnet",
		"enabled":  true,
		"disabled": false,
		"missing":  nil,
	})
	want := map[string]string{
		"epochs":   "10",
		"lr":       "0.001",
		"name":     "resnet",
		"enabled":  "true",
		"disabled": "false",
		"missing":  "null",
	}
	if len(got.Params) != len(want) {
		t.Fatalf("got %d params, want %d: %+v", len(got.Params), len(want), got.Params)
	}
	for _, p := range got.Params {
		if want[p.Key] != p.Value {
			t.Errorf("param %q = %q, want %q", p.Key, p.Value, want[p.Key])
		}
	}
}

func TestEncodeParams_ObjectAndArrayCanonicalJSON(t *testing.T) {
	got := EncodeParams(map[string]any{
		"cfg": map[string]any{"b": float64(2), "a": float64(1)},
		"arr": []any{float64(1), float64(2), float64(3)},
	})
	values := map[string]string{}
	for _, p := range got.Params {
		values[p.Key] = p.Value
	}
	// encoding/json sorts map keys, so "a" must precede "b" regardless of
	// the input map's iteration order.
	if values["cfg"] != `{"a":1,"b":2}` {
		t.Errorf("cfg = %q, want canonical sorted-key JSON", values["cfg"])
	}
	if values["arr"] != `[1,2,3]` {
		t.Errorf("arr = %q, want [1,2,3]", values["arr"])
	}
}

func TestEncodeParams_NoAutoFlatten(t *testing.T) {
	got := EncodeParams(map[string]any{"nested": map[string]any{"inner": "value"}})
	if len(got.Params) != 1 {
		t.Fatalf("expected exactly one param (no flattening), got %d: %+v", len(got.Params), got.Params)
	}
	if got.Params[0].Key != "nested" {
		t.Fatalf("expected the single top-level key %q to survive unflattened, got %q", "nested", got.Params[0].Key)
	}
}

func TestEncodeParams_RedactsSecretByKeyName(t *testing.T) {
	got := EncodeParams(map[string]any{
		"api_key":      "sk-abcdef123456",
		"db_password":  "hunter2",
		"secret_token": "xyz",
		"normal_key":   "sk-abcdef123456", // same-looking value, safe key name
	})
	values := map[string]string{}
	for _, p := range got.Params {
		values[p.Key] = p.Value
	}
	for _, key := range []string{"api_key", "db_password", "secret_token"} {
		if values[key] != "[REDACTED]" {
			t.Errorf("param %q = %q, want [REDACTED] (secret-looking key name)", key, values[key])
		}
	}
	if values["normal_key"] != "sk-abcdef123456" {
		t.Errorf("normal_key was redacted even though its key name isn't secret-shaped: %q", values["normal_key"])
	}
}

func TestEncodeParams_RedactsSecretPatternWithinValue(t *testing.T) {
	// Same pattern internal/redact.String looks for ("key: value"-shaped
	// text), embedded inside an otherwise-safe param's string value —
	// exercises the Run.Redact()-equivalent layer, not the key-name layer.
	got := EncodeParams(map[string]any{"notes": "password: hunter2 was used"})
	if len(got.Params) != 1 {
		t.Fatalf("expected 1 param, got %d", len(got.Params))
	}
	if strings.Contains(got.Params[0].Value, "hunter2") {
		t.Errorf("notes value leaked an embedded secret: %q", got.Params[0].Value)
	}
}

func TestEncodeParams_OverflowBecomesTagNotTruncatedParam(t *testing.T) {
	big := strings.Repeat("x", maxParamValueLen+1)
	got := EncodeParams(map[string]any{"blob": big})
	if len(got.Params) != 0 {
		t.Fatalf("oversized value should not appear as a Param at all, got %+v", got.Params)
	}
	if got.OverflowCount != 1 {
		t.Fatalf("OverflowCount = %d, want 1", got.OverflowCount)
	}
	tag, ok := got.OverflowTags["piper.param_overflow.blob"]
	if !ok {
		t.Fatalf("expected an overflow tag for key %q, got tags=%v", "blob", got.OverflowTags)
	}
	if strings.Contains(tag, big) {
		t.Errorf("overflow tag embeds the full oversized value instead of a hash+preview: %q", tag)
	}
	if !strings.HasPrefix(tag, "sha256:") {
		t.Errorf("overflow tag = %q, want a sha256: prefix", tag)
	}
}

func TestEncodeParams_Empty(t *testing.T) {
	got := EncodeParams(nil)
	if len(got.Params) != 0 || len(got.OverflowTags) != 0 {
		t.Fatalf("EncodeParams(nil) = %+v, want empty", got)
	}
}

func TestExperimentGroupKey(t *testing.T) {
	if got := experimentGroupKey("nightly", "train"); got != "experiment:nightly" {
		t.Errorf("with experiment set: got %q, want experiment:nightly", got)
	}
	if got := experimentGroupKey("", "train"); got != "pipeline:train" {
		t.Errorf("without experiment: got %q, want pipeline:train", got)
	}
}

func TestExperimentNameFromTemplate(t *testing.T) {
	got := experimentNameFromTemplate("", "proj-1", "train")
	want := "piper/proj-1/train"
	if got != want {
		t.Errorf("default template: got %q, want %q", got, want)
	}
	got = experimentNameFromTemplate("custom/{project_id}/{experiment_or_pipeline}/x", "proj-1", "nightly")
	want = "custom/proj-1/nightly/x"
	if got != want {
		t.Errorf("custom template: got %q, want %q", got, want)
	}
}

func TestRunTags_OmitsEmptyFields(t *testing.T) {
	tags := runTags(PipelineRunCreatedPayload{
		ProjectID:    "p1",
		RunID:        "r1",
		PipelineName: "train",
		RuntimeType:  "baremetal",
		CreatedBy:    "",
		Experiment:   "",
		RunURL:       "",
	}, "int-1")
	for _, key := range []string{"piper.created_by", "piper.experiment", "piper.url", "piper.pipeline.version"} {
		if _, ok := tags[key]; ok {
			t.Errorf("tag %q should be omitted when its source field is empty, got %v", key, tags)
		}
	}
	for key, want := range map[string]string{
		"piper.project_id":     "p1",
		"piper.run_id":         "r1",
		"piper.pipeline.name":  "train",
		"piper.runtime":        "baremetal",
		"piper.source":         "pipeline",
		"piper.integration_id": "int-1",
	} {
		if tags[key] != want {
			t.Errorf("tag %q = %q, want %q", key, tags[key], want)
		}
	}
}
