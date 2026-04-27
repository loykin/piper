// Package client provides a small typed HTTP client for the piper server API.
package client

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"strconv"
	"time"

	"github.com/piper/piper/pkg/logstore"
	"github.com/piper/piper/pkg/proto"
	"github.com/piper/piper/pkg/run"
	"github.com/piper/piper/pkg/schedule"
	"github.com/piper/piper/pkg/serving"
)

type Client struct {
	baseURL    string
	token      string
	httpClient *http.Client
}

type Option func(*Client)

type RunWithSteps struct {
	*run.Run
	Steps []*run.Step `json:"steps"`
}

type CreateRunRequest struct {
	YAML    string            `json:"yaml"`
	Params  map[string]any    `json:"params,omitempty"`
	OwnerID string            `json:"owner_id,omitempty"`
	Vars    proto.BuiltinVars `json:"vars,omitempty"`
}

type CreateScheduleRequest struct {
	Name    string         `json:"name"`
	YAML    string         `json:"yaml"`
	Type    string         `json:"type"`
	Cron    string         `json:"cron,omitempty"`
	RunAt   *time.Time     `json:"run_at,omitempty"`
	OwnerID string         `json:"owner_id,omitempty"`
	Params  map[string]any `json:"params,omitempty"`
}

type Event struct {
	ID     string         `json:"id"`
	Type   string         `json:"type"`
	At     time.Time      `json:"at"`
	Fields map[string]any `json:"fields,omitempty"`
}

type Worker struct {
	ID           string    `json:"id"`
	Label        string    `json:"label"`
	Version      string    `json:"version"`
	Capabilities string    `json:"capabilities"`
	Concurrency  int       `json:"concurrency"`
	Hostname     string    `json:"hostname"`
	RegisteredAt time.Time `json:"registered_at"`
	LastSeen     time.Time `json:"last_seen"`
	Status       string    `json:"status"`
	InFlight     int       `json:"in_flight"`
}

type ArtifactFile struct {
	Path       string    `json:"path"`
	Size       int64     `json:"size"`
	ModifiedAt time.Time `json:"modified_at"`
}

type ArtifactEntry struct {
	Name  string         `json:"name"`
	Files []ArtifactFile `json:"files"`
}

type StepArtifacts struct {
	Step      string          `json:"step"`
	Artifacts []ArtifactEntry `json:"artifacts"`
}

func (c *Client) ListRuns(ctx context.Context, filter run.RunFilter) ([]RunWithSteps, error) {
	q := url.Values{}
	if filter.Status != "" {
		q.Set("status", filter.Status)
	}
	if filter.PipelineName != "" {
		q.Set("pipeline_name", filter.PipelineName)
	}
	var out []RunWithSteps
	return out, c.doJSON(ctx, http.MethodGet, "/runs?"+q.Encode(), nil, &out)
}

func (c *Client) CreateRun(ctx context.Context, req CreateRunRequest) (string, error) {
	var out struct {
		RunID string `json:"run_id"`
	}
	if err := c.doJSON(ctx, http.MethodPost, "/runs", req, &out); err != nil {
		return "", err
	}
	return out.RunID, nil
}

func (c *Client) GetRun(ctx context.Context, id string) (*run.Run, []*run.Step, error) {
	var out struct {
		Run   *run.Run    `json:"run"`
		Steps []*run.Step `json:"steps"`
	}
	if err := c.doJSON(ctx, http.MethodGet, "/runs/"+url.PathEscape(id), nil, &out); err != nil {
		return nil, nil, err
	}
	return out.Run, out.Steps, nil
}

func (c *Client) CancelRun(ctx context.Context, id string) error {
	return c.doJSON(ctx, http.MethodPost, "/runs/"+url.PathEscape(id)+"/cancel", nil, nil)
}

func (c *Client) DeleteRun(ctx context.Context, id string) error {
	return c.doJSON(ctx, http.MethodDelete, "/runs/"+url.PathEscape(id), nil, nil)
}

func (c *Client) GetLogs(ctx context.Context, runID, stepName string, afterID int64) ([]*logstore.Line, error) {
	q := url.Values{}
	if afterID > 0 {
		q.Set("after", strconv.FormatInt(afterID, 10))
	}
	var out []*logstore.Line
	return out, c.doJSON(ctx, http.MethodGet, "/runs/"+url.PathEscape(runID)+"/steps/"+url.PathEscape(stepName)+"/logs?"+q.Encode(), nil, &out)
}

func (c *Client) ListArtifacts(ctx context.Context, runID string) ([]StepArtifacts, error) {
	var out []StepArtifacts
	return out, c.doJSON(ctx, http.MethodGet, "/runs/"+url.PathEscape(runID)+"/artifacts", nil, &out)
}

func (c *Client) DownloadArtifact(ctx context.Context, runID, step, artifact, filePath string) (io.ReadCloser, error) {
	u := "/runs/" + url.PathEscape(runID) + "/artifacts/" + path.Join(url.PathEscape(step), url.PathEscape(artifact), filePath)
	req, err := c.newRequest(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		defer func() { _ = resp.Body.Close() }()
		return nil, decodeError(resp)
	}
	return resp.Body, nil
}

func (c *Client) ListSchedules(ctx context.Context) ([]*schedule.Schedule, error) {
	var out []*schedule.Schedule
	return out, c.doJSON(ctx, http.MethodGet, "/schedules", nil, &out)
}

func (c *Client) CreateSchedule(ctx context.Context, req CreateScheduleRequest) (string, error) {
	var out struct {
		ScheduleID string `json:"schedule_id"`
	}
	if err := c.doJSON(ctx, http.MethodPost, "/schedules", req, &out); err != nil {
		return "", err
	}
	return out.ScheduleID, nil
}

func (c *Client) SetScheduleEnabled(ctx context.Context, id string, enabled bool) error {
	return c.doJSON(ctx, http.MethodPatch, "/schedules/"+url.PathEscape(id), map[string]bool{"enabled": enabled}, nil)
}

func (c *Client) ListWorkers(ctx context.Context) ([]Worker, error) {
	var out []Worker
	return out, c.doJSON(ctx, http.MethodGet, "/api/workers", nil, &out)
}

func (c *Client) ListServices(ctx context.Context) ([]*serving.Service, error) {
	var out []*serving.Service
	return out, c.doJSON(ctx, http.MethodGet, "/services", nil, &out)
}

func (c *Client) DeployService(ctx context.Context, yaml string) (*serving.Service, error) {
	var out serving.Service
	if err := c.doJSON(ctx, http.MethodPost, "/services", map[string]string{"yaml": yaml}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *Client) StopService(ctx context.Context, name string) error {
	return c.doJSON(ctx, http.MethodDelete, "/services/"+url.PathEscape(name), nil, nil)
}

func (c *Client) RestartService(ctx context.Context, name string) error {
	return c.doJSON(ctx, http.MethodPost, "/services/"+url.PathEscape(name)+"/restart", nil, nil)
}

func (c *Client) Events(ctx context.Context) (io.ReadCloser, error) {
	req, err := c.newRequest(ctx, http.MethodGet, "/events", nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		defer func() { _ = resp.Body.Close() }()
		return nil, decodeError(resp)
	}
	return resp.Body, nil
}

func (c *Client) doJSON(ctx context.Context, method, apiPath string, in, out any) error {
	var body io.Reader
	if in != nil {
		data, err := json.Marshal(in)
		if err != nil {
			return err
		}
		body = bytes.NewReader(data)
	}
	req, err := c.newRequest(ctx, method, apiPath, body)
	if err != nil {
		return err
	}
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return decodeError(resp)
	}
	if out == nil || resp.StatusCode == http.StatusNoContent {
		return nil
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

func (c *Client) newRequest(ctx context.Context, method, apiPath string, body io.Reader) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+apiPath, body)
	if err != nil {
		return nil, err
	}
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	return req, nil
}

func decodeError(resp *http.Response) error {
	var body struct {
		Error string `json:"error"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&body)
	if body.Error == "" {
		body.Error = resp.Status
	}
	return fmt.Errorf("piper API %s %s: %s", resp.Request.Method, resp.Request.URL.Path, body.Error)
}
