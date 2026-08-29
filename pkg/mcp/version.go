package mcp

import "fmt"

// ProtocolVersion20251125 is the only MCP protocol version Piper implements
// (design doc §8.1). Phase 4 or later may widen SupportedProtocolVersions
// when a newer spec revision is adopted.
const ProtocolVersion20251125 = "2025-11-25"

// SupportedProtocolVersions is the version-negotiation allowlist checked
// against both the "MCP-Protocol-Version" HTTP header (design doc §8.1) and
// an "initialize" request's params.protocolVersion.
var SupportedProtocolVersions = []string{ProtocolVersion20251125}

// IsSupportedProtocolVersion reports whether v is one Piper accepts.
func IsSupportedProtocolVersion(v string) bool {
	for _, sv := range SupportedProtocolVersions {
		if sv == v {
			return true
		}
	}
	return false
}

// ErrUnsupportedProtocolVersion is returned by ValidateProtocolVersionHeader
// when the header is present but not in SupportedProtocolVersions.
type ErrUnsupportedProtocolVersion struct{ Got string }

func (e *ErrUnsupportedProtocolVersion) Error() string {
	return fmt.Sprintf("mcp: unsupported MCP-Protocol-Version %q (supported: %v)", e.Got, SupportedProtocolVersions)
}

// ValidateProtocolVersionHeader validates the "MCP-Protocol-Version" HTTP
// header per design doc §8.1's negotiation requirement.
//
// Per the MCP Streamable HTTP transport spec, a client that has not yet
// completed "initialize" may omit the header entirely (the version is
// instead negotiated through the initialize request/response body); every
// request after that MUST carry it. isInitialize tells this function which
// case applies so it can enforce "header must be present and valid" on
// every non-initialize call while still allowing a bare first request.
func ValidateProtocolVersionHeader(header string, isInitialize bool) error {
	if header == "" {
		if isInitialize {
			return nil
		}
		return fmt.Errorf("mcp: missing required MCP-Protocol-Version header")
	}
	if !IsSupportedProtocolVersion(header) {
		return &ErrUnsupportedProtocolVersion{Got: header}
	}
	return nil
}
