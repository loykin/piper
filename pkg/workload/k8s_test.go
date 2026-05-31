package workload

import (
	"strings"
	"testing"
)

func TestSafeName(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "lowercase passthrough",
			input: "my-service",
			want:  "my-service",
		},
		{
			name:  "uppercase converted to lowercase",
			input: "MyService",
			want:  "myservice",
		},
		{
			name:  "all uppercase",
			input: "TRAIN-JOB",
			want:  "train-job",
		},
		{
			name:  "special characters replaced with hyphens",
			input: "my_model.v2",
			want:  "my-model-v2",
		},
		{
			name:  "spaces replaced with hyphens",
			input: "my model name",
			want:  "my-model-name",
		},
		{
			name:  "leading hyphens trimmed",
			input: "---foo",
			want:  "foo",
		},
		{
			name:  "trailing hyphens trimmed",
			input: "foo---",
			want:  "foo",
		},
		{
			name:  "leading and trailing hyphens trimmed",
			input: "--foo--",
			want:  "foo",
		},
		{
			name:  "special chars at edges trimmed after replacement",
			input: "_myservice_",
			want:  "myservice",
		},
		{
			name:  "exactly 63 characters unchanged",
			input: strings.Repeat("a", 63),
			want:  strings.Repeat("a", 63),
		},
		{
			name:  "64 characters truncated to 63",
			input: strings.Repeat("a", 64),
			want:  strings.Repeat("a", 63),
		},
		{
			name: "over 63 chars with trailing hyphen after truncation trimmed",
			// 62 a's + '-' + 'b' = 64 chars. After truncating to 63 we get 62 a's + '-'.
			// TrimRight should remove that trailing hyphen.
			input: strings.Repeat("a", 62) + "-b",
			want:  strings.Repeat("a", 62),
		},
		{
			name:  "digits are preserved",
			input: "model123",
			want:  "model123",
		},
		{
			name:  "mixed case with numbers and special chars",
			input: "Model_V2.0/test",
			want:  "model-v2-0-test",
		},
		{
			name: "unicode special char replaced and trailing hyphen trimmed",
			// 'é' is a non-ASCII rune → replaced with '-', then Trim removes it.
			input: "café",
			want:  "caf",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := SafeName(tc.input)
			if got != tc.want {
				t.Errorf("SafeName(%q) = %q, want %q", tc.input, got, tc.want)
			}
			// Invariants
			if got != strings.ToLower(got) {
				t.Errorf("result contains uppercase: %q", got)
			}
			if len(got) > 63 {
				t.Errorf("result exceeds 63 chars: len=%d", len(got))
			}
			if len(got) > 0 {
				if got[0] == '-' {
					t.Errorf("result starts with hyphen: %q", got)
				}
				if got[len(got)-1] == '-' {
					t.Errorf("result ends with hyphen: %q", got)
				}
			}
		})
	}
}
