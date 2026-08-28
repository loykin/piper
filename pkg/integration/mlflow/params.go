package mlflow

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/loykin/piper/internal/redact"
)

// maxParamValueLen bounds a single MLflow param value's encoded length
// (design doc section 7.2: "길이 제한을 넘는 값은 조용히 자르지 않고 hash와
// 정제된 preview를 tag로 남기며 integration warning을 기록한다"). MLflow's
// own server-side limit has changed across versions (500 chars in older
// releases, 6000 in current ones); this package picks the more permissive
// 6000 as a deliberate judgment call (design doc section 21 leaves the
// minimum-supported-MLflow-version question open) — a value that fits here
// but not on an older target server will surface as a normal retryable/
// non-retryable LogBatch error from the real server, not silently succeed.
const maxParamValueLen = 6000

// secretKeyPattern flags a param key as secret-by-name — the same
// key-name vocabulary internal/redact.String's value-pattern regex uses
// (password/token/secret/api_key/access_key), applied here to the *key*
// so an entire secret value is dropped rather than relying on the value's
// own text happening to match a "key: value" shape.
var secretKeyPattern = regexp.MustCompile(`(?i)(password|passwd|token|secret|api[_-]?key|access[_-]?key)`)

// EncodedParams is the result of EncodeParams: params short enough to log
// directly via LogBatch, plus tags recording any that overflowed
// maxParamValueLen (hash + redacted preview, never the full value).
type EncodedParams struct {
	Params        []Param
	OverflowTags  map[string]string
	OverflowCount int
}

// EncodeParams applies design doc section 7.2's canonical param encoding:
//   - string/number/bool/null -> a stable string representation
//   - object/array -> canonical (sorted-key) JSON
//   - no automatic flattening of nested structures
//   - values are redacted at least as strongly as run.Run.Redact() (see
//     redactParamValue's doc comment for why this is strictly stronger)
//   - oversized values become an overflow tag (hash + preview) instead of
//     being silently truncated
//
// params is expected to already be the decoded JSON snapshot Piper stored
// on the run (map[string]any, i.e. run.Run.ParamsJSON unmarshaled) — the
// same shape run.Run.ParamsJSON always has.
func EncodeParams(params map[string]any) EncodedParams {
	out := EncodedParams{OverflowTags: map[string]string{}}
	if len(params) == 0 {
		return out
	}
	keys := make([]string, 0, len(params))
	for k := range params {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, key := range keys {
		encoded := canonicalScalarOrJSON(params[key])
		redacted := redactParamValue(key, encoded)
		if len(redacted) <= maxParamValueLen {
			out.Params = append(out.Params, Param{Key: key, Value: redacted})
			continue
		}
		out.OverflowCount++
		sum := sha256.Sum256([]byte(redacted))
		preview := redacted
		if len(preview) > 64 {
			preview = preview[:64] + "…"
		}
		out.OverflowTags["piper.param_overflow."+key] = fmt.Sprintf("sha256:%s preview=%q", hex.EncodeToString(sum[:]), preview)
	}
	return out
}

// redactParamValue redacts a single already-encoded param value at least
// as strongly as run.Run.Redact() does for the whole ParamsJSON blob:
// Run.Redact() applies internal/redact.String (a "key: value"-shaped
// pattern scan) to the entire JSON text. Applying the same String() pass to
// each individual value here catches everything Run.Redact() would, plus
// this adds a key-name check Run.Redact() doesn't have: a param whose *key*
// itself looks like a secret (password/token/secret/api_key/access_key) is
// fully redacted regardless of what its value looks like, since a value
// like a bare API key or password often doesn't match the "key: value"
// text pattern String() looks for (there is no embedded "key:" text — the
// value *is* the secret).
func redactParamValue(key, value string) string {
	if secretKeyPattern.MatchString(key) {
		return "[REDACTED]"
	}
	return redact.String(value)
}

// canonicalScalarOrJSON implements the encoding rule for a single decoded
// JSON value (design doc section 7.2).
func canonicalScalarOrJSON(v any) string {
	switch t := v.(type) {
	case nil:
		return "null"
	case bool:
		return strconv.FormatBool(t)
	case string:
		return t
	case float64:
		return formatJSONNumber(t)
	case json.Number:
		return t.String()
	case map[string]any, []any:
		// encoding/json sorts map[string]any keys alphabetically at every
		// level, so a plain Marshal is already canonical/deterministic.
		b, err := json.Marshal(t)
		if err != nil {
			return fmt.Sprintf("%v", t)
		}
		return string(b)
	default:
		return fmt.Sprintf("%v", t)
	}
}

// formatJSONNumber renders a decoded JSON number (always float64 via
// encoding/json's default decoding) as an integer string when it has no
// fractional part, matching how the value was almost certainly authored
// (e.g. a param `{"epochs": 10}` should log as "10", not "1e+01" or
// "10.000000") — otherwise the shortest round-tripping decimal form.
func formatJSONNumber(f float64) string {
	if !math.IsInf(f, 0) && !math.IsNaN(f) && f == math.Trunc(f) && math.Abs(f) < 1e15 {
		return strconv.FormatInt(int64(f), 10)
	}
	return strconv.FormatFloat(f, 'g', -1, 64)
}

// runTags builds the design doc section 7.2 recommended `piper.*` tag set
// for a pipeline run's MLflow run.
func runTags(payload PipelineRunCreatedPayload, integrationID string) map[string]string {
	tags := map[string]string{
		"piper.project_id":     payload.ProjectID,
		"piper.run_id":         payload.RunID,
		"piper.pipeline.name":  payload.PipelineName,
		"piper.runtime":        payload.RuntimeType,
		"piper.created_by":     payload.CreatedBy,
		"piper.source":         "pipeline",
		"piper.integration_id": integrationID,
	}
	if payload.PipelineVersion > 0 {
		tags["piper.pipeline.version"] = strconv.Itoa(payload.PipelineVersion)
	}
	if payload.Experiment != "" {
		tags["piper.experiment"] = payload.Experiment
	}
	if payload.RunURL != "" {
		tags["piper.url"] = payload.RunURL
	}
	for k, v := range tags {
		if v == "" {
			delete(tags, k)
		}
	}
	return tags
}

// experimentGroupKey computes the design doc section 6.1 PiperGroupKey:
// "experiment:<name>" when the run has a Piper Experiment, otherwise
// "pipeline:<name>".
func experimentGroupKey(experiment, pipelineName string) string {
	if strings.TrimSpace(experiment) != "" {
		return "experiment:" + experiment
	}
	return "pipeline:" + pipelineName
}

// experimentNameFromTemplate resolves an MLflow experiment name from an
// ExperimentTemplate string (design doc section 5.1). Supported
// placeholders: {project_id} and {experiment_or_pipeline}. An empty
// template falls back to DefaultExperimentTemplate.
func experimentNameFromTemplate(tmpl, projectID, experimentOrPipeline string) string {
	if strings.TrimSpace(tmpl) == "" {
		tmpl = DefaultExperimentTemplate
	}
	name := strings.ReplaceAll(tmpl, "{project_id}", projectID)
	name = strings.ReplaceAll(name, "{experiment_or_pipeline}", experimentOrPipeline)
	return name
}
