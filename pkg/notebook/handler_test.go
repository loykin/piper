package notebook

import (
	"net/http"
	"testing"
)

func TestNotebookProxyRawQueryInjectsToken(t *testing.T) {
	got := notebookProxyRawQuery("foo=bar", "tok-123")
	if got != "foo=bar&token=tok-123" {
		t.Fatalf("query = %q", got)
	}
}

func TestNotebookProxyRawQueryKeepsExistingToken(t *testing.T) {
	got := notebookProxyRawQuery("token=client-token&foo=bar", "stored-token")
	if got != "foo=bar&token=client-token" {
		t.Fatalf("query = %q", got)
	}
}

func TestNotebookProxyRawQueryNoTokenNoChange(t *testing.T) {
	got := notebookProxyRawQuery("foo=bar", "")
	if got != "foo=bar" {
		t.Fatalf("query = %q", got)
	}
}

func TestIsWebSocketUpgrade(t *testing.T) {
	req, err := http.NewRequest(http.MethodGet, "/notebooks/demo/proxy/api/kernels/x/channels", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Upgrade", "websocket")
	req.Header.Set("Connection", "keep-alive, Upgrade")

	if !isWebSocketUpgrade(req) {
		t.Fatal("expected websocket upgrade")
	}
}

func TestIsWebSocketUpgradeRejectsNormalHTTP(t *testing.T) {
	req, err := http.NewRequest(http.MethodGet, "/notebooks/demo/proxy/lab", nil)
	if err != nil {
		t.Fatal(err)
	}
	if isWebSocketUpgrade(req) {
		t.Fatal("normal HTTP request was treated as websocket")
	}
}
