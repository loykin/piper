package mcp

import (
	"regexp"
	"strings"
)

// uriPrefix is the fixed scheme+authority prefix of every resource URI this
// package produces or accepts (design doc §8.3's four patterns all start
// this way).
const uriPrefix = "piper://projects/"

var (
	reDocument        = regexp.MustCompile(`^notebooks/([^/]+)/documents/(.+)$`)
	reFile            = regexp.MustCompile(`^notebooks/([^/]+)/files/(.+)$`)
	reExecutionResult = regexp.MustCompile(`^notebook-executions/([^/]+)/result$`)
	reExecution       = regexp.MustCompile(`^notebook-executions/([^/]+)$`)
)

// parsePiperURI splits a piper:// resource URI into its project id and the
// remainder after "projects/{project_id}/". ok is false for anything that
// doesn't even have the right scheme/prefix shape.
func parsePiperURI(uri string) (projectID, rest string, ok bool) {
	if !strings.HasPrefix(uri, uriPrefix) {
		return "", "", false
	}
	remainder := uri[len(uriPrefix):]
	idx := strings.IndexByte(remainder, '/')
	if idx < 0 || idx == 0 {
		return "", "", false
	}
	return remainder[:idx], remainder[idx+1:], true
}

// documentURI builds the piper://.../documents/{path} URI for a notebook
// document — used both to advertise the resource template and to build a
// resource_link back-reference from piper_read_notebook's tool result when
// the document is too large to inline (design doc §8.3).
func documentURI(projectID, notebookName, path string) string {
	return uriPrefix + projectID + "/notebooks/" + notebookName + "/documents/" + path
}

// fileURI builds the piper://.../files/{path} URI for a non-notebook file.
func fileURI(projectID, notebookName, path string) string {
	return uriPrefix + projectID + "/notebooks/" + notebookName + "/files/" + path
}

// executionURI builds the piper://.../notebook-executions/{id} URI.
func executionURI(projectID, executionID string) string {
	return uriPrefix + projectID + "/notebook-executions/" + executionID
}

// executionResultURI builds the piper://.../notebook-executions/{id}/result URI.
func executionResultURI(projectID, executionID string) string {
	return uriPrefix + projectID + "/notebook-executions/" + executionID + "/result"
}
