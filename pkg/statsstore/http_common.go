package statsstore

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type CredentialResolver func(context.Context, string) (map[string]string, error)

type httpBackend struct {
	base        *url.URL
	client      *http.Client
	credential  map[string]string
	tokenScheme string
}

func newHTTPBackend(rawURL string, credential map[string]string) (*httpBackend, *url.URL, error) {
	u, err := url.Parse(rawURL)
	if err != nil || u.Host == "" {
		return nil, nil, fmt.Errorf("invalid statistics backend URL")
	}
	if u.User != nil {
		return nil, nil, fmt.Errorf("statistics backend credentials must use credential_ref, not URL userinfo")
	}
	transportScheme := "http"
	if strings.HasSuffix(u.Scheme, "+https") || u.Scheme == "https" {
		transportScheme = "https"
	}
	base := *u
	base.Scheme = transportScheme
	base.RawQuery = ""
	base.Fragment = ""
	return &httpBackend{base: &base, client: &http.Client{Timeout: 15 * time.Second}, credential: credential, tokenScheme: "Bearer"}, u, nil
}

func (h *httpBackend) request(ctx context.Context, method, path string, query url.Values, body []byte, contentType string) ([]byte, error) {
	u := *h.base
	u.Path = strings.TrimRight(h.base.Path, "/") + "/" + strings.TrimLeft(path, "/")
	u.RawQuery = query.Encode()
	req, err := http.NewRequestWithContext(ctx, method, u.String(), bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	if apiKey := h.credential["api_key"]; apiKey != "" {
		req.Header.Set("Authorization", "ApiKey "+apiKey)
	} else if token := h.credential["token"]; token != "" {
		if strings.Contains(token, " ") {
			req.Header.Set("Authorization", token)
		} else {
			req.Header.Set("Authorization", h.tokenScheme+" "+token)
		}
	} else if user := firstCredential(h.credential, "username", "user"); user != "" {
		req.SetBasicAuth(user, h.credential["password"])
	}
	resp, err := h.client.Do(req)
	if err != nil {
		return nil, errors.Join(ErrBackendUnavailable, err)
	}
	defer resp.Body.Close()
	data, readErr := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if readErr != nil {
		return nil, readErr
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, errors.Join(ErrBackendUnavailable, fmt.Errorf("statistics backend returned HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(data))))
	}
	return data, nil
}

func firstCredential(values map[string]string, keys ...string) string {
	for _, key := range keys {
		if values[key] != "" {
			return values[key]
		}
	}
	return ""
}

func jsonBody(value any) ([]byte, error) { return json.Marshal(value) }
