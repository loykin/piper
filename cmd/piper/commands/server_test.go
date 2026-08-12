package commands

import (
	"testing"

	cliconfig "github.com/loykin/piper/cmd/piper/config"
)

func TestServerCommandHasNoOperationalFlags(t *testing.T) {
	cmd := newServerCmd(cliconfig.NewLoader(), nil)
	if cmd.HasAvailableLocalFlags() {
		t.Fatalf("server command should be config-only, got flags:\n%s", cmd.LocalNonPersistentFlags().FlagUsages())
	}
}

func TestRunCommandHasNoOperationalFlags(t *testing.T) {
	cmd := newRunCmd(cliconfig.NewLoader(), nil)
	if cmd.HasAvailableLocalFlags() {
		t.Fatalf("run command should be config-only, got flags:\n%s", cmd.LocalNonPersistentFlags().FlagUsages())
	}
}

func TestResolveMemberIDUsesConfiguredValue(t *testing.T) {
	root := cliconfig.RootConfig{Deployment: cliconfig.DeploymentConfig{MemberID: "member-explicit"}}
	if got := resolveMemberID(root); got != "member-explicit" {
		t.Fatalf("resolveMemberID = %q, want member-explicit", got)
	}
}

func TestResolveMemberIDGeneratesDefaultWhenEmpty(t *testing.T) {
	root := cliconfig.RootConfig{}
	got := resolveMemberID(root)
	if got == "" {
		t.Fatal("resolveMemberID returned empty string")
	}
	if got2 := resolveMemberID(root); got2 != got {
		t.Fatalf("resolveMemberID not stable across calls: %q != %q", got, got2)
	}
}
