package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"

	piperMCP "github.com/loykin/piper/pkg/mcp"
	"github.com/loykin/piper/pkg/notebook"
	"github.com/loykin/piper/pkg/notebook/execution"
)

// toolDeps holds the two dependencies every Phase 2 tool/resource needs —
// see Deps' doc comment in handler.go for why both are held directly.
type toolDeps struct {
	Notebooks  notebook.Repository
	Executions *execution.Service
}

func boolPtr(b bool) *bool { return &b }

// readOnlyAnnotations is the fixed annotation set every Phase 2 tool uses
// (design doc §8.2: "조회 tool: readOnlyHint=true, openWorldHint=false" — none
// of these tools reach any network beyond Piper's own Jupyter server, which
// is already the project's own managed infrastructure, not an open/unknown
// endpoint).
func readOnlyAnnotations(title string) piperMCP.ToolAnnotations {
	return piperMCP.ToolAnnotations{
		Title:           title,
		ReadOnlyHint:    true,
		DestructiveHint: boolPtr(false),
		IdempotentHint:  true,
		OpenWorldHint:   false,
	}
}

// invalidParams builds the ProtocolError a tool handler returns for
// malformed/missing input — a caller-input problem, distinct from a
// domain-level failure (not found, notebook not running, ...) which
// server.go instead turns into a model-visible isError:true result.
func invalidParams(format string, args ...any) error {
	return &piperMCP.ProtocolError{Code: piperMCP.CodeInvalidParams, Message: fmt.Sprintf(format, args...)}
}

func (d toolDeps) tools() map[string]piperMCP.Tool {
	return map[string]piperMCP.Tool{
		"piper_list_notebook_servers": {
			Definition: piperMCP.ToolDefinition{
				Name:        "piper_list_notebook_servers",
				Title:       "List notebook servers",
				Description: "List the Jupyter notebook servers managed by Piper in this project.",
				InputSchema: json.RawMessage(`{"type":"object","properties":{},"additionalProperties":false}`),
				Annotations: readOnlyAnnotations("List notebook servers"),
			},
			Handler: d.listNotebookServers,
		},
		"piper_get_notebook_server": {
			Definition: piperMCP.ToolDefinition{
				Name:        "piper_get_notebook_server",
				Title:       "Get notebook server",
				Description: "Get one Jupyter notebook server's status by name.",
				InputSchema: json.RawMessage(`{"type":"object","properties":{"name":{"type":"string","description":"Notebook server name"}},"required":["name"],"additionalProperties":false}`),
				Annotations: readOnlyAnnotations("Get notebook server"),
			},
			Handler: d.getNotebookServer,
		},
		"piper_list_notebook_files": {
			Definition: piperMCP.ToolDefinition{
				Name:        "piper_list_notebook_files",
				Title:       "List notebook files",
				Description: "List files and directories under a running notebook server's workspace.",
				InputSchema: json.RawMessage(`{"type":"object","properties":{"name":{"type":"string","description":"Notebook server name"},"path":{"type":"string","description":"Workspace-relative directory path; empty for the workspace root"}},"required":["name"],"additionalProperties":false}`),
				Annotations: readOnlyAnnotations("List notebook files"),
			},
			Handler: d.listNotebookFiles,
		},
		"piper_read_notebook": {
			Definition: piperMCP.ToolDefinition{
				Name:        "piper_read_notebook",
				Title:       "Read notebook",
				Description: "Read a .ipynb document's content. Small documents are returned inline; larger ones are returned as a resource link.",
				InputSchema: json.RawMessage(`{"type":"object","properties":{"name":{"type":"string","description":"Notebook server name"},"path":{"type":"string","description":"Workspace-relative .ipynb path"}},"required":["name","path"],"additionalProperties":false}`),
				Annotations: readOnlyAnnotations("Read notebook"),
			},
			Handler: d.readNotebook,
		},
		"piper_get_execution": {
			Definition: piperMCP.ToolDefinition{
				Name:        "piper_get_execution",
				Title:       "Get notebook execution",
				Description: "Get a notebook or cell execution's status, progress, and result location by id.",
				InputSchema: json.RawMessage(`{"type":"object","properties":{"execution_id":{"type":"string"}},"required":["execution_id"],"additionalProperties":false}`),
				Annotations: readOnlyAnnotations("Get notebook execution"),
			},
			Handler: d.getExecution,
		},
		"piper_list_executions": {
			Definition: piperMCP.ToolDefinition{
				Name:        "piper_list_executions",
				Title:       "List notebook executions",
				Description: "List the notebook execution history for a notebook, most recent first.",
				InputSchema: json.RawMessage(`{"type":"object","properties":{"name":{"type":"string","description":"Notebook server name"},"limit":{"type":"integer","minimum":1},"offset":{"type":"integer","minimum":0}},"required":["name"],"additionalProperties":false}`),
				Annotations: readOnlyAnnotations("List notebook executions"),
			},
			Handler: d.listExecutions,
		},
	}
}

// audit logs one tool call's outcome per design doc §13.2/§13.3: actor id,
// client id, project, tool name, and status — never arguments/output, which
// may carry notebook content.
func audit(ctx context.Context, tool string, err error, isError bool) {
	scope := requestScopeFrom(ctx)
	status := "ok"
	if err != nil || isError {
		status = "error"
	}
	slog.Info("mcp tool call", "tool", tool, "project_id", scope.ProjectID, "actor_id", scope.Actor.ID, "client_id", scope.Actor.ClientID, "status", status)
}

func (d toolDeps) listNotebookServers(ctx context.Context, _ json.RawMessage) (*piperMCP.ToolCallResult, error) {
	projectID := projectIDFrom(ctx)
	servers, err := d.Notebooks.List(ctx, projectID)
	audit(ctx, "piper_list_notebook_servers", err, false)
	if err != nil {
		return nil, err
	}
	public := NewNotebookServerPublics(servers)
	return piperMCP.TextResult(fmt.Sprintf("%d notebook server(s)", len(public)), map[string]any{"servers": public})
}

type getNotebookServerArgs struct {
	Name string `json:"name"`
}

func (d toolDeps) getNotebookServer(ctx context.Context, args json.RawMessage) (*piperMCP.ToolCallResult, error) {
	var a getNotebookServerArgs
	if err := json.Unmarshal(args, &a); err != nil || strings.TrimSpace(a.Name) == "" {
		return nil, invalidParams("name is required")
	}
	projectID := projectIDFrom(ctx)
	server, err := d.Notebooks.Get(ctx, projectID, a.Name)
	audit(ctx, "piper_get_notebook_server", err, server == nil)
	if err != nil {
		return nil, err
	}
	if server == nil {
		return &piperMCP.ToolCallResult{IsError: true, Content: []piperMCP.ContentBlock{{Type: "text", Text: fmt.Sprintf("notebook server %q not found", a.Name)}}}, nil
	}
	return piperMCP.TextResult("notebook server "+a.Name, NewNotebookServerPublic(server))
}

type listNotebookFilesArgs struct {
	Name string `json:"name"`
	Path string `json:"path"`
}

func (d toolDeps) listNotebookFiles(ctx context.Context, args json.RawMessage) (*piperMCP.ToolCallResult, error) {
	var a listNotebookFilesArgs
	if err := json.Unmarshal(args, &a); err != nil || strings.TrimSpace(a.Name) == "" {
		return nil, invalidParams("name is required")
	}
	projectID := projectIDFrom(ctx)
	entries, err := d.Executions.ListContents(ctx, projectID, a.Name, a.Path)
	audit(ctx, "piper_list_notebook_files", err, false)
	if err != nil {
		return domainErrorResult(err)
	}
	out := make([]ContentEntryPublic, 0, len(entries))
	for _, e := range entries {
		out = append(out, ContentEntryPublic{Name: e.Name, Path: e.Path, Type: e.Type, Size: e.Size, LastModified: e.LastModified})
	}
	return piperMCP.TextResult(fmt.Sprintf("%d entrie(s)", len(out)), map[string]any{"entries": out})
}

type readNotebookArgs struct {
	Name string `json:"name"`
	Path string `json:"path"`
}

func (d toolDeps) readNotebook(ctx context.Context, args json.RawMessage) (*piperMCP.ToolCallResult, error) {
	var a readNotebookArgs
	if err := json.Unmarshal(args, &a); err != nil || strings.TrimSpace(a.Name) == "" || strings.TrimSpace(a.Path) == "" {
		return nil, invalidParams("name and path are required")
	}
	projectID := projectIDFrom(ctx)
	doc, hash, err := d.Executions.ReadDocument(ctx, projectID, a.Name, a.Path)
	audit(ctx, "piper_read_notebook", err, false)
	if err != nil {
		return domainErrorResult(err)
	}
	raw, merr := doc.Marshal()
	if merr != nil {
		return nil, merr
	}
	uri := documentURI(projectID, a.Name, a.Path)
	inlineLimit := d.Executions.Limits().InlineOutputBytes
	if len(raw) <= inlineLimit {
		return piperMCP.TextResult(string(raw), map[string]any{
			"content":      json.RawMessage(raw),
			"content_hash": hash,
			"inline":       true,
			"size_bytes":   len(raw),
			"resource_uri": uri,
		})
	}
	result, err := piperMCP.TextResult(fmt.Sprintf("notebook %s/%s is %d bytes, larger than the %d byte inline limit — read it via the resource link", a.Name, a.Path, len(raw), inlineLimit), map[string]any{
		"content":      nil,
		"content_hash": hash,
		"inline":       false,
		"size_bytes":   len(raw),
		"resource_uri": uri,
	})
	if err != nil {
		return nil, err
	}
	result.Content = append(result.Content, piperMCP.ContentBlock{Type: "resource_link", URI: uri, Name: a.Path, MIMEType: "application/json"})
	return result, nil
}

type getExecutionArgs struct {
	ExecutionID string `json:"execution_id"`
}

func (d toolDeps) getExecution(ctx context.Context, args json.RawMessage) (*piperMCP.ToolCallResult, error) {
	var a getExecutionArgs
	if err := json.Unmarshal(args, &a); err != nil || strings.TrimSpace(a.ExecutionID) == "" {
		return nil, invalidParams("execution_id is required")
	}
	projectID := projectIDFrom(ctx)
	exec, err := d.Executions.GetExecution(ctx, projectID, a.ExecutionID)
	audit(ctx, "piper_get_execution", err, false)
	if err != nil {
		return domainErrorResult(err)
	}
	resp := execution.NewNotebookExecutionResponse(exec)
	return piperMCP.TextResult("execution "+exec.ID+" is "+exec.Status, map[string]any{
		"execution":    resp,
		"result_uri":   executionResultURI(projectID, exec.ID),
		"resource_uri": executionURI(projectID, exec.ID),
	})
}

type listExecutionsArgs struct {
	Name   string `json:"name"`
	Limit  int    `json:"limit"`
	Offset int    `json:"offset"`
}

func (d toolDeps) listExecutions(ctx context.Context, args json.RawMessage) (*piperMCP.ToolCallResult, error) {
	var a listExecutionsArgs
	if err := json.Unmarshal(args, &a); err != nil || strings.TrimSpace(a.Name) == "" {
		return nil, invalidParams("name is required")
	}
	projectID := projectIDFrom(ctx)
	list, total, err := d.Executions.ListExecutions(ctx, projectID, a.Name, a.Limit, a.Offset)
	audit(ctx, "piper_list_executions", err, false)
	if err != nil {
		return domainErrorResult(err)
	}
	return piperMCP.TextResult(fmt.Sprintf("%d execution(s) (total %d)", len(list), total), map[string]any{
		"executions": execution.NewNotebookExecutionResponses(list),
		"total":      total,
	})
}

// domainErrorResult turns a Service-layer error (not found, notebook not
// running, path invalid, output too large, ...) into a model-visible
// isError:true tool result rather than a JSON-RPC protocol error — these
// are outcomes the AI client should see and can reason about (design doc
// §11.3's stable error codes), not malformed requests. Message text already
// never carries a token/endpoint/host path (execution.Error's own
// contract), so it's safe to surface verbatim.
func domainErrorResult(err error) (*piperMCP.ToolCallResult, error) {
	return &piperMCP.ToolCallResult{IsError: true, Content: []piperMCP.ContentBlock{{Type: "text", Text: err.Error()}}}, nil
}
