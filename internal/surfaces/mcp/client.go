// Package mcp is the MCP client adapter (README Phase 11, task 11.1): each
// remote MCP server's tools are qualified as "mcp/{server}/{tool}@{version}"
// and admitted through the ordinary identity (#13), manifest (#14), and
// descriptor-scan (#15) path — an external tool is exactly as trusted as a
// builtin one, never more. This package never imports kernel/
// (tests/contract/boundaries_test.go's wildcard rule on internal/surfaces/...
// already covers it) and holds no agent control flow of its own — it is a
// hand-rolled JSON-RPC 2.0 client over net/http (MCP's Streamable HTTP
// transport), matching internal/surfaces/rest's own zero-framework style;
// no SDK dependency was added for it.
package mcp

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
)

// Tool is one remote MCP server's own self-description of a tool it offers
// — the wire shape MCP's tools/list result carries.
type Tool struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"inputSchema"`
}

// SchemaDigest is the content-addressed identity adapter.go's ToolRef.Version
// is built from (README task 11.1's own "#15 digest re-verification at use"
// applied to a source that isn't a static bundle, per
// internal/tools/pipeline.go's resolveTool doc comment): a schema change
// produces a DIFFERENT qualified ref, which is a fresh admission decision,
// not a drifted one.
func (t Tool) SchemaDigest() string {
	h := sha256.New()
	_, _ = fmt.Fprintf(h, "name=%s\ndescription=%s\ninput_schema=%s\n", t.Name, t.Description, string(t.InputSchema))
	return hex.EncodeToString(h.Sum(nil))[:8]
}

// CallResult is what tools/call returns — Content is untrusted external
// text by construction (Taint().ReturnsUntrusted is always true for an MCP
// tool, adapter.go), IsError marks a tool-level failure the remote server
// reported rather than a transport failure.
type CallResult struct {
	Content []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"content"`
	IsError bool `json:"isError"`
}
