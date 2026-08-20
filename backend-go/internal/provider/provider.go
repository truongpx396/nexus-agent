package provider

import (
	"context"
	"encoding/json"
)

// ChunkKind is the discriminant of a normalized provider stream chunk
// (kernel-abi.md's Chunk union, translated to Go as a tagged struct since
// Go has no native discriminated union).
type ChunkKind string

const (
	ChunkContent   ChunkKind = "content"
	ChunkReasoning ChunkKind = "reasoning"
	ChunkToolUse   ChunkKind = "tool_use"
	ChunkUsage     ChunkKind = "usage"
	ChunkDone      ChunkKind = "done"
)

// DoneReason is the terminal reason a stream ended (kernel-abi.md: "stop" | "max_output" | "error").
type DoneReason string

const (
	DoneStop      DoneReason = "stop"
	DoneMaxOutput DoneReason = "max_output"
	DoneError     DoneReason = "error"
)

// Chunk is one normalized item in a Provider's output stream. Only the
// fields relevant to Kind are populated; the rest are zero-valued. This
// mirrors kernel-abi.md's tagged union exactly — MUST NOT leak vendor JSON
// into the loop (FR-027).
type Chunk struct {
	Kind ChunkKind

	// kind == "content"
	Text string

	// kind == "reasoning" — opaque, round-tripped, NEVER shown to a user (FR-064)
	Opaque []byte

	// kind == "tool_use"
	ToolUseID string
	ToolName  string
	ToolInput json.RawMessage

	// kind == "usage" — split by token class; this split is what makes the
	// >90% cache-read gate (FR-014, SC-003) measurable. NEVER collapse
	// these into one field.
	InputUncached   int
	InputCacheRead  int
	InputCacheWrite int
	OutputTokens    int

	// kind == "done"
	DoneReason DoneReason
}

// Message is one turn in a Prompt's history.
type Message struct {
	Role    string // "system" | "user" | "assistant" | "tool"
	Content string
}

// ToolSchema is the schema advertised to the provider for native tool-calling.
type ToolSchema struct {
	Name        string
	Description string
	InputSchema json.RawMessage
}

// Prompt is the normalized request shape passed to Stream.
type Prompt struct {
	System   string
	Messages []Message
}

// Provider is the ONE abstraction all model access goes through — native
// tool-calling only, no parsing tools out of free-form text, no scattered
// SDK calls anywhere else in the codebase (constitution Principle VII).
type Provider interface {
	Stream(ctx context.Context, prompt Prompt, tools []ToolSchema) (<-chan Chunk, error)
}
