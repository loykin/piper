package secret

import "testing"

func TestRedactString(t *testing.T) {
	cases := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "password_colon",
			input: "password: supersecret",
			want:  "password=[REDACTED]",
		},
		{
			name:  "token_equals",
			input: "token=abc123xyz",
			want:  "token=[REDACTED]",
		},
		{
			name:  "api_key_dash",
			input: "api-key=mykey",
			want:  "api-key=[REDACTED]",
		},
		{
			name:  "aws_access_key",
			input: "AKIAIOSFODNN7EXAMPLE",
			want:  "[REDACTED_AWS_ACCESS_KEY]",
		},
		{
			name:  "no_sensitive_data",
			input: "hello world",
			want:  "hello world",
		},
		{
			name:  "empty_string",
			input: "",
			want:  "",
		},
		{
			name:  "secret_in_yaml",
			input: "secret: my-db-password",
			want:  "secret=[REDACTED]",
		},
		{
			name:  "access_key_underscore",
			input: "access_key=KEYVALUE",
			want:  "access_key=[REDACTED]",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := RedactString(tc.input)
			if got != tc.want {
				t.Errorf("RedactString(%q) = %q, want %q", tc.input, got, tc.want)
			}
		})
	}
}
