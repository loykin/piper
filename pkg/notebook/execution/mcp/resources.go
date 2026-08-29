package mcp

import (
	"context"
	"encoding/json"

	piperMCP "github.com/loykin/piper/pkg/mcp"
	"github.com/loykin/piper/pkg/notebook/execution"
)

// resourceTemplates returns design doc §8.3's four piper:// resource
// templates. "resources/templates/list" vs plain "resources/list" —
// see pkg/mcp.Server's doc comment for why templates is the right choice
// here.
func resourceTemplates() []piperMCP.ResourceTemplate {
	return []piperMCP.ResourceTemplate{
		{
			URITemplate: uriPrefix + "{project_id}/notebooks/{name}/documents/{path}",
			Name:        "notebook-document",
			Title:       "Notebook document",
			Description: "A .ipynb document's full content.",
			MIMEType:    "application/json",
		},
		{
			URITemplate: uriPrefix + "{project_id}/notebook-executions/{execution_id}",
			Name:        "notebook-execution",
			Title:       "Notebook execution",
			Description: "A notebook or cell execution's status and metadata.",
			MIMEType:    "application/json",
		},
		{
			URITemplate: uriPrefix + "{project_id}/notebook-executions/{execution_id}/result",
			Name:        "notebook-execution-result",
			Title:       "Notebook execution result",
			Description: "The result .ipynb document an execution produced.",
			MIMEType:    "application/json",
		},
		{
			URITemplate: uriPrefix + "{project_id}/notebooks/{name}/files/{path}",
			Name:        "notebook-file",
			Title:       "Notebook workspace file",
			Description: "A non-notebook file from a notebook server's workspace.",
		},
	}
}

// readResource dispatches a "resources/read" call across the four URI
// patterns. Path traversal / symlink escape / absolute-path rejection is
// not re-implemented here — every branch below ends up calling
// execution.Service methods that already validate through
// notebook.CleanWorkspacePath (the same function pkg/notebook's
// WorkspaceReader itself uses), so a "../" or absolute-path segment
// anywhere in {path} is rejected at the Service layer with ErrCodePathInvalid,
// consistently with every other Notebook content API in this codebase.
func (d toolDeps) readResource(ctx context.Context, uri string) (*piperMCP.ReadResourceResult, error) {
	endpointProjectID := projectIDFrom(ctx)
	projectID, rest, ok := parsePiperURI(uri)
	if !ok {
		return nil, invalidParams("invalid piper:// resource uri %q", uri)
	}
	// design doc §8.3: "URI의 project는 현재 endpoint project와 반드시 일치해야
	// 한다" — reject rather than silently redirecting to another project.
	if projectID != endpointProjectID {
		return nil, invalidParams("resource project %q does not match this connection's project %q", projectID, endpointProjectID)
	}

	switch {
	case reExecutionResult.MatchString(rest):
		m := reExecutionResult.FindStringSubmatch(rest)
		return d.readExecutionResultResource(ctx, projectID, m[1])
	case reExecution.MatchString(rest):
		m := reExecution.FindStringSubmatch(rest)
		return d.readExecutionResource(ctx, projectID, m[1])
	case reDocument.MatchString(rest):
		m := reDocument.FindStringSubmatch(rest)
		return d.readDocumentResource(ctx, projectID, m[1], m[2])
	case reFile.MatchString(rest):
		m := reFile.FindStringSubmatch(rest)
		return d.readFileResource(ctx, projectID, m[1], m[2])
	default:
		return nil, invalidParams("unrecognized resource uri %q", uri)
	}
}

func (d toolDeps) readDocumentResource(ctx context.Context, projectID, name, path string) (*piperMCP.ReadResourceResult, error) {
	doc, _, err := d.Executions.ReadDocument(ctx, projectID, name, path)
	if err != nil {
		return nil, resourceError(err)
	}
	raw, err := doc.Marshal()
	if err != nil {
		return nil, err
	}
	uri := documentURI(projectID, name, path)
	return &piperMCP.ReadResourceResult{Contents: []piperMCP.ResourceContents{{URI: uri, MIMEType: "application/json", Text: string(raw)}}}, nil
}

func (d toolDeps) readFileResource(ctx context.Context, projectID, name, path string) (*piperMCP.ReadResourceResult, error) {
	fc, err := d.Executions.ReadFile(ctx, projectID, name, path)
	if err != nil {
		return nil, resourceError(err)
	}
	uri := fileURI(projectID, name, path)
	mime := fc.MimeType
	contents := piperMCP.ResourceContents{URI: uri, MIMEType: mime}
	if fc.Format == "base64" {
		if mime == "" {
			contents.MIMEType = "application/octet-stream"
		}
		contents.Blob = fc.Content
	} else {
		if mime == "" {
			contents.MIMEType = "text/plain"
		}
		contents.Text = fc.Content
	}
	return &piperMCP.ReadResourceResult{Contents: []piperMCP.ResourceContents{contents}}, nil
}

func (d toolDeps) readExecutionResource(ctx context.Context, projectID, executionID string) (*piperMCP.ReadResourceResult, error) {
	exec, err := d.Executions.GetExecution(ctx, projectID, executionID)
	if err != nil {
		return nil, resourceError(err)
	}
	raw, err := marshalExecution(exec)
	if err != nil {
		return nil, err
	}
	uri := executionURI(projectID, executionID)
	return &piperMCP.ReadResourceResult{Contents: []piperMCP.ResourceContents{{URI: uri, MIMEType: "application/json", Text: string(raw)}}}, nil
}

func (d toolDeps) readExecutionResultResource(ctx context.Context, projectID, executionID string) (*piperMCP.ReadResourceResult, error) {
	exec, err := d.Executions.GetExecution(ctx, projectID, executionID)
	if err != nil {
		return nil, resourceError(err)
	}
	doc, _, err := d.Executions.ReadDocument(ctx, projectID, exec.NotebookName, exec.ResultPath)
	if err != nil {
		return nil, resourceError(err)
	}
	raw, err := doc.Marshal()
	if err != nil {
		return nil, err
	}
	uri := executionResultURI(projectID, executionID)
	return &piperMCP.ReadResourceResult{Contents: []piperMCP.ResourceContents{{URI: uri, MIMEType: "application/json", Text: string(raw)}}}, nil
}

// marshalExecution encodes a NotebookExecution's public response shape —
// the same DTO REST returns, which already excludes any token/endpoint
// (execution.NewNotebookExecutionResponse's own doc comment).
func marshalExecution(exec *execution.NotebookExecution) ([]byte, error) {
	return json.Marshal(execution.NewNotebookExecutionResponse(exec))
}

// resourceError wraps a Service-layer error as a JSON-RPC "invalid params"
// protocol error for resources/read — unlike tools/call, there is no
// model-visible isError result channel for a resource read, so a
// not-found/not-running/path-invalid outcome is reported as a normal
// JSON-RPC error (design doc §11.3's error codes stay in the message text;
// they never include a token/endpoint/host path, by execution.Error's own
// contract).
func resourceError(err error) error {
	return &piperMCP.ProtocolError{Code: piperMCP.CodeInvalidParams, Message: err.Error()}
}
