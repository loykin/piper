package process

import (
	"os"
	"testing"
)

func TestExpandVars(t *testing.T) {
	t.Run("envMap takes precedence over os.Getenv", func(t *testing.T) {
		t.Setenv("MYVAR", "from_env")
		envMap := map[string]string{"MYVAR": "from_map"}
		got := ExpandVars("$MYVAR", envMap)
		if got != "from_map" {
			t.Errorf("expected from_map, got %q", got)
		}
	})

	t.Run("falls back to os.Getenv when not in envMap", func(t *testing.T) {
		t.Setenv("FALLBACK_VAR", "env_value")
		got := ExpandVars("$FALLBACK_VAR", map[string]string{})
		if got != "env_value" {
			t.Errorf("expected env_value, got %q", got)
		}
	})

	t.Run("dollar sign format $VAR", func(t *testing.T) {
		got := ExpandVars("hello $NAME world", map[string]string{"NAME": "gopher"})
		if got != "hello gopher world" {
			t.Errorf("expected 'hello gopher world', got %q", got)
		}
	})

	t.Run("curly braces format ${VAR}", func(t *testing.T) {
		got := ExpandVars("hello ${NAME} world", map[string]string{"NAME": "gopher"})
		if got != "hello gopher world" {
			t.Errorf("expected 'hello gopher world', got %q", got)
		}
	})

	t.Run("parentheses format $(VAR)", func(t *testing.T) {
		got := ExpandVars("hello $(NAME) world", map[string]string{"NAME": "gopher"})
		if got != "hello gopher world" {
			t.Errorf("expected 'hello gopher world', got %q", got)
		}
	})

	t.Run("unknown variable expands to empty string when not in env", func(t *testing.T) {
		err := os.Unsetenv("UNKNOWN_VAR_XYZ_123")
		if err != nil {
			t.Errorf("failed to unset env var: %v", err)
		}
		got := ExpandVars("$UNKNOWN_VAR_XYZ_123", map[string]string{})
		if got != "" {
			t.Errorf("expected empty string, got %q", got)
		}
	})

	t.Run("dollar at end of string", func(t *testing.T) {
		got := ExpandVars("trailing$", map[string]string{})
		if got != "trailing$" {
			t.Errorf("expected 'trailing$', got %q", got)
		}
	})

	t.Run("no substitution in plain string", func(t *testing.T) {
		got := ExpandVars("no vars here", map[string]string{})
		if got != "no vars here" {
			t.Errorf("expected 'no vars here', got %q", got)
		}
	})

	t.Run("multiple vars in one string", func(t *testing.T) {
		envMap := map[string]string{"A": "alpha", "B": "beta"}
		got := ExpandVars("$A and $B", envMap)
		if got != "alpha and beta" {
			t.Errorf("expected 'alpha and beta', got %q", got)
		}
	})

	t.Run("${VAR} with empty key (just ${}) emits dollar sign followed by braces", func(t *testing.T) {
		// ${} → key is empty → write '$', then continue from '{' which is a literal char.
		// Result: '$' + '{' + '}' = "${}"
		got := ExpandVars("${}", map[string]string{})
		if got != "${}" {
			t.Errorf("expected '${}'`, got %q", got)
		}
	})

	t.Run("$(VAR) with empty key (just $()) emits dollar sign followed by parens", func(t *testing.T) {
		// $() → key is empty → write '$', then continue from '(' literal.
		// Result: '$' + '(' + ')' = "$()"
		got := ExpandVars("$()", map[string]string{})
		if got != "$()" {
			t.Errorf("expected '$()'`, got %q", got)
		}
	})

	t.Run("$VAR stops at non-identifier character", func(t *testing.T) {
		got := ExpandVars("$FOO.bar", map[string]string{"FOO": "baz"})
		if got != "baz.bar" {
			t.Errorf("expected 'baz.bar', got %q", got)
		}
	})

	t.Run("all three formats in one string", func(t *testing.T) {
		envMap := map[string]string{"X": "1", "Y": "2", "Z": "3"}
		got := ExpandVars("$X ${Y} $(Z)", envMap)
		if got != "1 2 3" {
			t.Errorf("expected '1 2 3', got %q", got)
		}
	})
}

func TestExpandArgs(t *testing.T) {
	t.Run("each argument is expanded", func(t *testing.T) {
		envMap := map[string]string{"CMD": "python", "SCRIPT": "train.py", "LR": "0.01"}
		args := []string{"$CMD", "$SCRIPT", "--lr=$LR"}
		got := ExpandArgs(args, envMap)
		want := []string{"python", "train.py", "--lr=0.01"}
		if len(got) != len(want) {
			t.Fatalf("length mismatch: got %d, want %d", len(got), len(want))
		}
		for i := range want {
			if got[i] != want[i] {
				t.Errorf("arg[%d]: got %q, want %q", i, got[i], want[i])
			}
		}
	})

	t.Run("empty args slice returns empty slice", func(t *testing.T) {
		got := ExpandArgs([]string{}, map[string]string{})
		if len(got) != 0 {
			t.Errorf("expected empty slice, got %v", got)
		}
	})

	t.Run("args without vars are unchanged", func(t *testing.T) {
		args := []string{"echo", "hello"}
		got := ExpandArgs(args, map[string]string{})
		for i, v := range args {
			if got[i] != v {
				t.Errorf("arg[%d]: got %q, want %q", i, got[i], v)
			}
		}
	})

	t.Run("all three var formats across args", func(t *testing.T) {
		envMap := map[string]string{"A": "one", "B": "two", "C": "three"}
		args := []string{"$A", "${B}", "$(C)"}
		got := ExpandArgs(args, envMap)
		want := []string{"one", "two", "three"}
		for i := range want {
			if got[i] != want[i] {
				t.Errorf("arg[%d]: got %q, want %q", i, got[i], want[i])
			}
		}
	})
}
