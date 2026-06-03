package tunnelproxy

import (
	"net/http"
	"testing"
)

func TestPolicyFuncAndChain(t *testing.T) {
	reqSeen := 0
	respSeen := 0
	p1 := PolicyFunc{
		OnRequest: func(req *http.Request) error {
			reqSeen++
			req.Header.Set("X-One", "1")
			return nil
		},
		OnResponse: func(resp *http.Response) error {
			respSeen++
			resp.Header.Set("X-Resp-One", "1")
			return nil
		},
	}
	p2 := PolicyFunc{
		OnRequest: func(req *http.Request) error {
			reqSeen++
			req.Header.Set("X-Two", "2")
			return nil
		},
		OnResponse: func(resp *http.Response) error {
			respSeen++
			resp.Header.Set("X-Resp-Two", "2")
			return nil
		},
	}

	chain := Chain(p1, p2)
	req, _ := http.NewRequest(http.MethodGet, "http://example.com/", nil)
	if err := chain.RewriteRequest(req); err != nil {
		t.Fatal(err)
	}
	if got, want := req.Header.Get("X-One"), "1"; got != want {
		t.Fatalf("X-One = %q, want %q", got, want)
	}
	if got, want := req.Header.Get("X-Two"), "2"; got != want {
		t.Fatalf("X-Two = %q, want %q", got, want)
	}

	resp := &http.Response{Header: http.Header{}}
	if err := chain.RewriteResponse(resp); err != nil {
		t.Fatal(err)
	}
	if got, want := resp.Header.Get("X-Resp-One"), "1"; got != want {
		t.Fatalf("X-Resp-One = %q, want %q", got, want)
	}
	if got, want := resp.Header.Get("X-Resp-Two"), "2"; got != want {
		t.Fatalf("X-Resp-Two = %q, want %q", got, want)
	}
	if reqSeen != 2 {
		t.Fatalf("request hooks seen = %d, want 2", reqSeen)
	}
	if respSeen != 2 {
		t.Fatalf("response hooks seen = %d, want 2", respSeen)
	}
}

func TestRewriteLocationPrefix(t *testing.T) {
	resp := &http.Response{Header: http.Header{}}
	resp.Header.Set("Location", "http://127.0.0.1:8888/lab?token=upstream&next=1")
	if err := RewriteLocationPrefix(resp, "/notebooks/jj/proxy", "token"); err != nil {
		t.Fatal(err)
	}
	if got, want := resp.Header.Get("Location"), "/notebooks/jj/proxy/lab?next=1"; got != want {
		t.Fatalf("Location = %q, want %q", got, want)
	}

	resp = &http.Response{Header: http.Header{}}
	resp.Header.Set("Location", "/notebooks/jj/proxy/lab/?token=upstream")
	if err := RewriteLocationPrefix(resp, "/notebooks/jj/proxy", "token"); err != nil {
		t.Fatal(err)
	}
	if got, want := resp.Header.Get("Location"), "/notebooks/jj/proxy/lab/"; got != want {
		t.Fatalf("Location = %q, want %q", got, want)
	}
}
