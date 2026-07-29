package commands

import (
	"slices"
	"testing"

	cliconfig "github.com/piper/piper/cmd/piper/config"
)

func TestPublicCommandSurface(t *testing.T) {
	var public []string
	for _, cmd := range Commands(cliconfig.NewLoader(), nil) {
		if !cmd.Hidden {
			public = append(public, cmd.Name())
		}
	}
	slices.Sort(public)
	want := []string{"config", "parse", "run", "server", "user", "worker"}
	if !slices.Equal(public, want) {
		t.Fatalf("public commands = %v, want %v", public, want)
	}
}
