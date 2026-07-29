package notebookworker

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"sort"
	"strings"
	"sync"
	"time"
)

const (
	jupyterHTTPTimeout   = 3 * time.Second
	jupyterMaxConcurrent = 4
)

type jupyterContentsClient struct {
	httpClient *http.Client
}

func (c *jupyterContentsClient) ReadFile(ctx context.Context, svcHost, baseURL, token, virtualPath string) (io.ReadCloser, int64, error) {
	apiPath := path.Join(strings.TrimRight(baseURL, "/"), "api/contents", virtualPath)
	reqURL := (&url.URL{
		Scheme:   "http",
		Host:     svcHost,
		Path:     apiPath,
		RawQuery: "content=1",
	}).String()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
	if err != nil {
		return nil, 0, err
	}
	if token != "" {
		req.Header.Set("Authorization", "token "+token)
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, 0, fmt.Errorf("jupyter file HTTP %d", resp.StatusCode)
	}
	var item struct {
		Type    string          `json:"type"`
		Format  string          `json:"format"`
		Content json.RawMessage `json:"content"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&item); err != nil {
		return nil, 0, err
	}
	if item.Type != "file" && item.Type != "notebook" {
		return nil, 0, fmt.Errorf("path %q is not a file", virtualPath)
	}
	var data []byte
	if item.Format == "base64" {
		var encoded string
		if err := json.Unmarshal(item.Content, &encoded); err != nil {
			return nil, 0, err
		}
		data, err = base64.StdEncoding.DecodeString(encoded)
		if err != nil {
			return nil, 0, err
		}
	} else if item.Type == "notebook" {
		data = append([]byte(nil), item.Content...)
	} else {
		var content string
		if err := json.Unmarshal(item.Content, &content); err != nil {
			return nil, 0, err
		}
		data = []byte(content)
	}
	return io.NopCloser(bytes.NewReader(data)), int64(len(data)), nil
}

func newJupyterContentsClient() *jupyterContentsClient {
	return &jupyterContentsClient{
		httpClient: &http.Client{Timeout: jupyterHTTPTimeout},
	}
}

type jupyterItem struct {
	Name    string        `json:"name"`
	Path    string        `json:"path"`
	Type    string        `json:"type"` // "directory", "notebook", "file"
	Content []jupyterItem `json:"content"`
}

// ListFiles performs a BFS traversal of the Jupyter Contents API starting at startPath.
// svcHost is the notebook service address, e.g. "piper-nb-<name>.<ns>.svc.cluster.local:8888".
// baseURL is the Jupyter base URL, e.g. "/notebooks/<name>/proxy/".
func (c *jupyterContentsClient) ListFiles(ctx context.Context, svcHost, baseURL, token, startPath string, ext []string, maxFiles int) (files []string, truncated bool, err error) {
	if maxFiles <= 0 || maxFiles > 1000 {
		maxFiles = 500
	}
	allowed := make(map[string]bool, len(ext))
	for _, e := range ext {
		allowed[e] = true
	}

	type workItem struct{ virtualPath string }
	queue := []workItem{{virtualPath: startPath}}

	sem := make(chan struct{}, jupyterMaxConcurrent)
	var mu sync.Mutex

	for len(queue) > 0 && len(files) < maxFiles {
		if ctx.Err() != nil {
			return files, false, ctx.Err()
		}

		// Drain the current queue level concurrently.
		batch := queue
		queue = nil

		type result struct {
			path  string
			items []jupyterItem
			err   error
		}
		results := make(chan result, len(batch))

		for _, item := range batch {
			sem <- struct{}{}
			go func(vp string) {
				defer func() { <-sem }()
				items, fetchErr := c.fetchDirectory(ctx, svcHost, baseURL, token, vp)
				results <- result{path: vp, items: items, err: fetchErr}
			}(item.virtualPath)
		}

		for range batch {
			r := <-results
			if r.err != nil {
				return nil, false, fmt.Errorf("jupyter contents %q: %w", r.path, r.err)
			}
			mu.Lock()
			for _, child := range r.items {
				if len(files) >= maxFiles {
					truncated = true
					break
				}
				switch child.Type {
				case "directory":
					if !strings.HasPrefix(child.Name, ".") {
						queue = append(queue, workItem{virtualPath: child.Path})
					}
				case "notebook", "file":
					if len(allowed) == 0 || allowed[path.Ext(child.Name)] {
						if startPath == "" || strings.HasPrefix(child.Path, startPath) {
							files = append(files, child.Path)
						}
					}
				}
			}
			mu.Unlock()
		}

		if truncated {
			break
		}
	}

	sort.Strings(files)
	return files, truncated, nil
}

func (c *jupyterContentsClient) fetchDirectory(ctx context.Context, svcHost, baseURL, token, virtualPath string) ([]jupyterItem, error) {
	apiPath := path.Join(strings.TrimRight(baseURL, "/"), "api/contents", virtualPath)
	reqURL := (&url.URL{
		Scheme:   "http",
		Host:     svcHost,
		Path:     apiPath,
		RawQuery: "content=1",
	}).String()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
	if err != nil {
		return nil, err
	}
	if token != "" {
		req.Header.Set("Authorization", "token "+token)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}

	var item jupyterItem
	if err := json.NewDecoder(resp.Body).Decode(&item); err != nil {
		return nil, err
	}
	if item.Type != "directory" {
		return nil, fmt.Errorf("expected directory, got %q", item.Type)
	}
	return item.Content, nil
}

// jupyterServiceHost returns the in-cluster DNS address for the notebook service.
func jupyterServiceHost(projectID, notebookName, namespace string) string {
	safeName := notebookWorkloadName(projectID, notebookName)
	return fmt.Sprintf("%s.%s.svc.cluster.local:8888", safeName, namespace)
}

// jupyterBaseURL returns the Jupyter base URL for the given notebook, matching
// the prefix proxyNotebook forwards to (/projects/:project_id/notebooks/:name/proxy).
func jupyterBaseURL(projectID, notebookName string) string {
	return "/projects/" + projectID + "/notebooks/" + notebookName + "/proxy/"
}
