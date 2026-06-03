package notebookworker

import (
	"testing"

	"github.com/piper/piper/pkg/notebook"
)

func TestProcessNotebookCommandUsesCanonicalArgs(t *testing.T) {
	req := RuntimeStartRequest{
		BaseURL: "/notebooks/demo/proxy/",
		Token:   "tok",
		WorkDir: "/work/demo",
		Port:    18888,
	}

	got := processNotebookCommand("/venv/bin/jupyter-lab", []string{"lab"}, req)
	want := append([]string{"/venv/bin/jupyter-lab", "lab"}, notebook.JupyterLabArgs(req.BaseURL, req.Token, req.WorkDir, req.Port)...)
	if len(got) != len(want) {
		t.Fatalf("command len = %d, want %d: %#v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("command[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}
