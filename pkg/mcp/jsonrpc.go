// Package mcp implements the transport- and protocol-level pieces of the
// Model Context Protocol (MCP) Streamable HTTP transport that
// docs/jupyter-mcp-execution.md §8 specifies: JSON-RPC 2.0 message framing,
// MCP-Protocol-Version negotiation, Origin/Host allowlisting (DNS-rebinding
// defense), and transport session bookkeeping (design doc §8.1: "도메인 실행
// 상태는 MCP session 메모리에 저장하지 않는다" — this package only ever tracks
// which identity/project/client a session ID belongs to, never any Piper
// domain object).
//
// This package deliberately knows nothing about Piper's domain model
// (notebooks, executions, ...) so a later MCP surface for a different
// domain can reuse it — see design doc §4.1's package-boundary rule, the
// same "protocol package must not import the domain package" discipline
// pkg/notebook/execution.Service already follows for Gin.
package mcp

import "encoding/json"

// JSONRPCVersion is the only JSON-RPC version MCP uses.
const JSONRPCVersion = "2.0"

// Standard JSON-RPC 2.0 error codes (https://www.jsonrpc.org/specification#error_object).
const (
	CodeParseError     = -32700
	CodeInvalidRequest = -32600
	CodeMethodNotFound = -32601
	CodeInvalidParams  = -32602
	CodeInternalError  = -32603
)

// RequestID is a JSON-RPC id: string, number, or (for a notification) absent.
// json.RawMessage round-trips whichever form the client sent without Piper
// needing to normalize it.
type RequestID = json.RawMessage

// Request is one JSON-RPC 2.0 request or notification. A notification is a
// Request with a nil/empty ID — RawID reports which.
type Request struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      RequestID       `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

// IsNotification reports whether r carries no id, per JSON-RPC 2.0 §4.1 —
// the server MUST NOT reply to a notification.
func (r Request) IsNotification() bool {
	return len(r.ID) == 0 || string(r.ID) == "null"
}

// Response is one JSON-RPC 2.0 response — exactly one of Result/Error is set.
type Response struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      RequestID       `json:"id"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *ErrorObject    `json:"error,omitempty"`
}

// ErrorObject is a JSON-RPC 2.0 error.
type ErrorObject struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
}

func newErrorResponse(id RequestID, code int, message string) *Response {
	return &Response{JSONRPC: JSONRPCVersion, ID: id, Error: &ErrorObject{Code: code, Message: message}}
}

func newResultResponse(id RequestID, result any) *Response {
	raw, err := json.Marshal(result)
	if err != nil {
		return newErrorResponse(id, CodeInternalError, "failed to encode result")
	}
	return &Response{JSONRPC: JSONRPCVersion, ID: id, Result: raw}
}

// ParseMessage decodes raw into either a single Request or a batch of
// Requests, matching JSON-RPC 2.0's optional batch form. ok is false when
// raw is neither a JSON object nor a JSON array (a parse error).
func ParseMessage(raw []byte) (batch []Request, ok bool) {
	trimmed := trimLeadingSpace(raw)
	if len(trimmed) == 0 {
		return nil, false
	}
	switch trimmed[0] {
	case '[':
		var reqs []Request
		if err := json.Unmarshal(raw, &reqs); err != nil {
			return nil, false
		}
		return reqs, true
	case '{':
		var req Request
		if err := json.Unmarshal(raw, &req); err != nil {
			return nil, false
		}
		return []Request{req}, true
	default:
		return nil, false
	}
}

func trimLeadingSpace(b []byte) []byte {
	i := 0
	for i < len(b) {
		switch b[i] {
		case ' ', '\t', '\n', '\r':
			i++
			continue
		}
		break
	}
	return b[i:]
}
