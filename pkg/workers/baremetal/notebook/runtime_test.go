package notebookworker

import (
	"testing"

	"github.com/piper/piper/pkg/notebook"
)

func TestProcessNotebookCommandUsesCanonicalArgs(t *testing.T) {
	req := RuntimeStartRequest{
		BaseURL: "/notebooks/demo/proxy/",
		WorkDir: "/work/demo",
		Port:    18888,
	}

	script, err := notebook.BuildLaunchScript(nil, nil,
		append([]string{"/venv/bin/jupyter-lab"}, notebook.JupyterLabArgs(req.BaseURL, req.Token, req.WorkDir, req.Port)...),
		req.WorkDir)
	if err != nil {
		t.Fatalf("BuildLaunchScript error: %v", err)
	}
	// Token is empty → --ServerApp.token= disables auth; master proxy is the security boundary.
	if want := "exec /venv/bin/jupyter-lab --ServerApp.base_url=/notebooks/demo/proxy/ --ServerApp.token= --IdentityProvider.token= --ServerApp.root_dir=/work/demo --ServerApp.trust_xheaders=True '--ServerApp.allow_origin=*' --no-browser --ServerApp.port_retries=0 --ServerApp.port=18888"; script == "" || !containsLine(script, want) {
		t.Fatalf("script = %q, want line %q", script, want)
	}
}

func containsLine(script, want string) bool {
	start := 0
	for i := 0; i <= len(script); i++ {
		if i == len(script) || script[i] == '\n' {
			if script[start:i] == want {
				return true
			}
			start = i + 1
		}
	}
	return false
}
