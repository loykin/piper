package notify

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const maxResponseBytes = 32 << 10

type httpNotifier struct {
	kind    string
	url     string
	headers map[string]string
	client  *http.Client
}

func newHTTPNotifier(kind, rawURL string, headers map[string]string, client *http.Client) (Notifier, error) {
	if err := validateDestination(rawURL); err != nil {
		return nil, err
	}
	if client == nil {
		client = safeHTTPClient()
	}
	return &httpNotifier{kind: kind, url: rawURL, headers: headers, client: client}, nil
}

func (n *httpNotifier) Send(ctx context.Context, msg Message) error {
	var payload any = msg
	if n.kind == "slack" {
		text := strings.TrimSpace(msg.Title + "\n" + msg.Body)
		payload = map[string]any{"text": text}
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, n.url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	for key, value := range n.headers {
		if !validHeaderName(key) {
			return fmt.Errorf("invalid webhook header name %q", key)
		}
		req.Header.Set(key, value)
	}
	resp, err := n.client.Do(req)
	if err != nil {
		if urlErr, ok := err.(*url.Error); ok {
			return fmt.Errorf("notification request failed: %v", urlErr.Err)
		}
		return fmt.Errorf("notification request failed: %v", err)
	}
	defer resp.Body.Close()
	response, _ := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("notification endpoint returned %s: %s", resp.Status, strings.TrimSpace(string(response)))
	}
	return nil
}

func validateDestination(raw string) error {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil {
		return fmt.Errorf("notification URL must be an https URL without userinfo")
	}
	host := strings.ToLower(strings.TrimSuffix(u.Hostname(), "."))
	if host == "localhost" || strings.HasSuffix(host, ".localhost") {
		return fmt.Errorf("notification URL must not target a private or local address")
	}
	if ip := net.ParseIP(u.Hostname()); ip != nil && !publicIP(ip) {
		return fmt.Errorf("notification URL must not target a private or local address")
	}
	return nil
}

func validHeaderName(name string) bool {
	name = strings.TrimSpace(name)
	if name == "" || strings.EqualFold(name, "host") || strings.EqualFold(name, "content-length") {
		return false
	}
	for _, r := range name {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || strings.ContainsRune("!#$%&'*+-.^_`|~", r)) {
			return false
		}
	}
	return true
}

func safeHTTPClient() *http.Client {
	dialer := &net.Dialer{Timeout: 5 * time.Second, KeepAlive: 30 * time.Second}
	transport := &http.Transport{TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12}, TLSHandshakeTimeout: 5 * time.Second, ResponseHeaderTimeout: 5 * time.Second, IdleConnTimeout: 30 * time.Second}
	transport.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(address)
		if err != nil {
			return nil, err
		}
		ips, err := net.DefaultResolver.LookupIPAddr(ctx, host)
		if err != nil {
			return nil, err
		}
		if len(ips) == 0 {
			return nil, fmt.Errorf("notification host did not resolve")
		}
		for _, resolved := range ips {
			if !publicIP(resolved.IP) {
				return nil, fmt.Errorf("notification host resolved to a private or local address")
			}
		}
		return dialer.DialContext(ctx, network, net.JoinHostPort(ips[0].IP.String(), port))
	}
	return &http.Client{Transport: transport, Timeout: 10 * time.Second, CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }}
}

func publicIP(ip net.IP) bool {
	return ip != nil && !ip.IsLoopback() && !ip.IsPrivate() && !ip.IsLinkLocalUnicast() && !ip.IsLinkLocalMulticast() && !ip.IsUnspecified() && !ip.IsMulticast()
}
