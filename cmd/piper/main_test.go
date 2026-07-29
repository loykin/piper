package main

import "testing"

func TestCompletionCommandIsDisabled(t *testing.T) {
	if !rootCmd.CompletionOptions.DisableDefaultCmd {
		t.Fatal("completion command must not be exposed")
	}
}
