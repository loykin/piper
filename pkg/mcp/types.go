package mcp

import "encoding/json"

// ServerInfo identifies this MCP server in the initialize handshake.
type ServerInfo struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

// ClientInfo is the client-supplied identification from the initialize
// request's params.clientInfo — design doc §8.1's "MCP client ID"
// (`initialize` request의 clientInfo) that a session gets bound to.
type ClientInfo struct {
	Name    string `json:"name"`
	Version string `json:"version,omitempty"`
}

// InitializeParams is the params object of an "initialize" request.
type InitializeParams struct {
	ProtocolVersion string          `json:"protocolVersion"`
	ClientInfo      ClientInfo      `json:"clientInfo"`
	Capabilities    json.RawMessage `json:"capabilities,omitempty"`
}

// ServerCapabilities advertises what this server offers. Phase 2 only ever
// offers tools and resources (read-only) — no prompts (design doc §8.3:
// "Prompts는 핵심 실행 기능이 아니므로 v1 범위에서 제외한다"), no logging/sampling.
type ServerCapabilities struct {
	Tools     *ToolsCapability     `json:"tools,omitempty"`
	Resources *ResourcesCapability `json:"resources,omitempty"`
}

type ToolsCapability struct {
	ListChanged bool `json:"listChanged"`
}

type ResourcesCapability struct {
	Subscribe   bool `json:"subscribe"`
	ListChanged bool `json:"listChanged"`
}

// InitializeResult is the result of a successful "initialize" call.
type InitializeResult struct {
	ProtocolVersion string             `json:"protocolVersion"`
	ServerInfo      ServerInfo         `json:"serverInfo"`
	Capabilities    ServerCapabilities `json:"capabilities"`
	Instructions    string             `json:"instructions,omitempty"`
}

// ToolAnnotations are client UI hints only (design doc §8.2: "annotation은
// 클라이언트 UI 힌트일 뿐이므로 Piper의 서버 측 권한/승인 검사를 대체하지 않는다") —
// never used by Piper itself to gate access.
type ToolAnnotations struct {
	Title           string `json:"title,omitempty"`
	ReadOnlyHint    bool   `json:"readOnlyHint"`
	DestructiveHint *bool  `json:"destructiveHint,omitempty"`
	IdempotentHint  bool   `json:"idempotentHint,omitempty"`
	OpenWorldHint   bool   `json:"openWorldHint"`
}

// ToolDefinition is one entry of a "tools/list" result.
type ToolDefinition struct {
	Name         string          `json:"name"`
	Title        string          `json:"title,omitempty"`
	Description  string          `json:"description,omitempty"`
	InputSchema  json.RawMessage `json:"inputSchema"`
	OutputSchema json.RawMessage `json:"outputSchema,omitempty"`
	Annotations  ToolAnnotations `json:"annotations"`
}

// ContentBlock is one entry of a tool result's or resource read's "content"
// array — Phase 2 only ever produces "text" and "resource_link" blocks (no
// binary/image content yet).
type ContentBlock struct {
	Type     string          `json:"type"` // "text" | "resource_link"
	Text     string          `json:"text,omitempty"`
	URI      string          `json:"uri,omitempty"`      // resource_link
	Name     string          `json:"name,omitempty"`     // resource_link
	MIMEType string          `json:"mimeType,omitempty"` // resource_link
	Meta     json.RawMessage `json:"_meta,omitempty"`
}

// ToolCallResult is the result of a "tools/call" — design doc §8.2: "각 tool은
// ... structured output을 반환한다", i.e. StructuredContent alongside the
// human-readable Content per the MCP spec's tools/call result shape.
type ToolCallResult struct {
	Content           []ContentBlock  `json:"content"`
	StructuredContent json.RawMessage `json:"structuredContent,omitempty"`
	IsError           bool            `json:"isError,omitempty"`
}

// TextResult builds a ToolCallResult carrying both a plain-text summary and
// the structured JSON payload — the shape every Phase 2 read tool returns.
func TextResult(text string, structured any) (*ToolCallResult, error) {
	raw, err := json.Marshal(structured)
	if err != nil {
		return nil, err
	}
	return &ToolCallResult{
		Content:           []ContentBlock{{Type: "text", Text: text}},
		StructuredContent: raw,
	}, nil
}

// ResourceTemplate is one entry of a "resources/templates/list" result
// (design doc §8.3's four piper:// URI patterns).
type ResourceTemplate struct {
	URITemplate string `json:"uriTemplate"`
	Name        string `json:"name"`
	Title       string `json:"title,omitempty"`
	Description string `json:"description,omitempty"`
	MIMEType    string `json:"mimeType,omitempty"`
}

// ResourceContents is one entry of a "resources/read" result's "contents"
// array. Exactly one of Text/Blob is set, matching the MCP spec's
// TextResourceContents/BlobResourceContents union.
type ResourceContents struct {
	URI      string `json:"uri"`
	MIMEType string `json:"mimeType,omitempty"`
	Text     string `json:"text,omitempty"`
	Blob     string `json:"blob,omitempty"` // base64
}

// ReadResourceResult is the result of a "resources/read" call.
type ReadResourceResult struct {
	Contents []ResourceContents `json:"contents"`
}
