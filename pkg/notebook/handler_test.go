package notebook

import "testing"

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
