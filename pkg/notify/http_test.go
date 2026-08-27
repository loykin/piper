package notify

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestSlackAndWebhookPayloads(t *testing.T) {
	var bodies []string
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		body, _ := io.ReadAll(r.Body)
		bodies = append(bodies, string(body))
		return &http.Response{StatusCode: http.StatusOK, Status: "200 OK", Body: io.NopCloser(strings.NewReader("ok")), Header: make(http.Header)}, nil
	})}
	for _, tc := range []struct {
		kind string
		data map[string]string
	}{{"slack", map[string]string{"webhook_url": "https://hooks.slack.com/services/test"}}, {"webhook", map[string]string{"url": "https://example.com/hook", "header_Authorization": "Bearer secret"}}} {
		n, err := Open(tc.kind, tc.data, client)
		if err != nil {
			t.Fatal(err)
		}
		if err := n.Send(context.Background(), Message{Title: "Title", Body: "Body", Fields: map[string]any{"status": "failed"}}); err != nil {
			t.Fatal(err)
		}
	}
	if !strings.Contains(bodies[0], `"text":"Title\nBody"`) {
		t.Fatalf("slack body=%s", bodies[0])
	}
	if !strings.Contains(bodies[1], `"fields":{"status":"failed"}`) {
		t.Fatalf("webhook body=%s", bodies[1])
	}
}

func TestOpenRejectsSSRFAndInsecureURLs(t *testing.T) {
	for _, raw := range []string{"http://example.com/hook", "https://127.0.0.1/hook", "https://[::1]/hook", "https://localhost/hook", "https://api.localhost/hook", "https://user:pass@example.com/hook"} {
		if _, err := Open("webhook", map[string]string{"url": raw}, nil); err == nil {
			t.Fatalf("accepted unsafe URL %q", raw)
		}
	}
}
