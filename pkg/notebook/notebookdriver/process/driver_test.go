package process

import (
	"strings"
	"testing"

	"github.com/loykin/piper/pkg/notebook/notebookdriver"
	"github.com/loykin/piper/pkg/notebook/notebookdriver/drivertest"
)

var _ notebookdriver.Driver = (*Driver)(nil)
var _ notebookdriver.Recoverable = (*Driver)(nil)

func TestProcessDriverContract(t *testing.T) {
	root := t.TempDir()
	drivertest.RunContract(t, func() notebookdriver.Driver { return New(root) })
}

func TestProcessRecoverableContract(t *testing.T) {
	root := t.TempDir()
	drivertest.RunRecoverableContract(t, func() interface {
		notebookdriver.Driver
		notebookdriver.Recoverable
	} {
		return New(root)
	})
}

// The baremetal Jupyter listens on a host port, so it must require the
// per-start token like the docker and k8s drivers; Piper's proxy injects it.
func TestProcessLaunchCommandRequiresToken(t *testing.T) {
	req := notebookdriver.StartRequest{
		BaseURL: "/notebooks/demo/proxy/",
		WorkDir: "/work/demo",
		Port:    18888,
		Token:   "tok-123",
	}
	for _, tc := range []struct {
		name, bin, envPath string
		extraArgs          []string
	}{
		{name: "venv", bin: "/venv/bin/jupyter-lab", envPath: "/venv"},
		{name: "conda", bin: "/usr/bin/conda", envPath: "conda:ml"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cmd, err := launchCommand(req, tc.bin, tc.extraArgs, tc.envPath, nil)
			if err != nil {
				t.Fatal(err)
			}
			script := cmd[len(cmd)-1]
			for _, want := range []string{"--ServerApp.token=tok-123", "--IdentityProvider.token=tok-123"} {
				if !strings.Contains(script, want) {
					t.Fatalf("script = %q, want %q", script, want)
				}
			}
		})
	}
}
