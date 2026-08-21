// Package tools declares the kernel ABI's Tool interface (T016): the
// self-describing capability surface every built-in and connector-exposed
// tool implements (FR-007, FR-008, FR-009, FR-011). This package is
// interface-only -- no concrete tool implementation lives here.
package tools

import (
	"context"
	"encoding/json"
)

// ToolRef is the fully-qualified identity of a tool: {namespace}/{name}@{version}.
type ToolRef struct {
	Namespace string
	Name      string
	Version   string
}

// Disclosure controls whether a tool's schema is always in the cache-stable
// prefix ("resident") or loaded on demand ("deferred") -- FR-062, FR-148.
type Disclosure string

const (
	// DisclosureResident marks a tool schema as always present in the
	// cache-stable prefix.
	DisclosureResident Disclosure = "resident"
	// DisclosureDeferred marks a tool schema as loaded on demand rather
	// than kept resident.
	DisclosureDeferred Disclosure = "deferred"
)

// EffectClass names the high-impact-action category a mutating tool
// belongs to, for the approval system (FR-036, FR-177). EffectClassOther is
// the permissive-default-refusing catch-all -- it is NEVER assigned
// implicitly; an unresolvable class fails closed (see the package's
// governance notes), it does not silently become EffectClassOther.
type EffectClass string

const (
	// EffectClassPayment marks a tool that moves money or payment
	// instruments.
	EffectClassPayment EffectClass = "payment"
	// EffectClassDelete marks a tool that destroys data or resources.
	EffectClassDelete EffectClass = "delete"
	// EffectClassExternalSend marks a tool that sends data to a party
	// outside the system (email, webhook, message, etc.).
	EffectClassExternalSend EffectClass = "external_send"
	// EffectClassProdChange marks a tool that mutates production
	// configuration or infrastructure state.
	EffectClassProdChange EffectClass = "prod_change"
	// EffectClassOther is the permissive-default-refusing catch-all for a
	// mutating tool that does not fit any of the named classes above. It
	// must never be assigned as a fallback for an unresolvable class --
	// an unresolvable class fails closed instead.
	EffectClassOther EffectClass = "other"
)

// TaintDeclaration declares which Rule-of-Two legs a tool invocation
// engages (FR-087). ALL THREE FIELDS DEFAULT TRUE -- a missing or
// unclassifiable declaration must be treated as engaging every leg
// (fail-closed). DefaultTaintDeclaration is the canonical all-true value;
// never construct a TaintDeclaration{} zero-value literal and treat it as
// safe -- Go's bool zero value is false, which would be fail-OPEN and is
// exactly the bug this type exists to prevent. Because Go structs
// zero-initialize to false, any code path that produces a TaintDeclaration
// MUST explicitly set all three fields true by default, e.g. via
// DefaultTaintDeclaration, never rely on an implicit zero value.
type TaintDeclaration struct {
	// ReturnsUntrusted reports whether the tool's output must be treated
	// as untrusted, model-influenceable content.
	ReturnsUntrusted bool
	// ReadsPrivateData reports whether the tool reads private or
	// sensitive data as part of its invocation.
	ReadsPrivateData bool
	// MutatesExternal reports whether the tool mutates state outside the
	// kernel's own control.
	MutatesExternal bool
}

// DefaultTaintDeclaration returns the fail-closed default TaintDeclaration:
// every leg engaged. Use this -- never a bare TaintDeclaration{} zero-value
// literal -- wherever a declaration is missing or unclassifiable (FR-087).
func DefaultTaintDeclaration() TaintDeclaration {
	return TaintDeclaration{ReturnsUntrusted: true, ReadsPrivateData: true, MutatesExternal: true}
}

// PermissionResult is the outcome of a Tool.CheckPermissions call.
type PermissionResult struct {
	// Allowed reports whether the invocation is permitted.
	Allowed bool
	// Reason explains a refusal (or, optionally, a grant); empty when not
	// applicable.
	Reason string
}

// ValidationResult is the outcome of a Tool.ValidateInput call.
type ValidationResult struct {
	// Valid reports whether the supplied input satisfies the tool's input
	// schema and any additional invariants.
	Valid bool
	// Errors lists the validation failures when Valid is false.
	Errors []string
}

// ToolResult is the outcome of a Tool.Call invocation.
type ToolResult struct {
	// Output is the tool's raw output payload.
	Output json.RawMessage
	// IsUntrusted marks Output as untrusted, model-influenceable content
	// by default -- per Rule of Two, a tool's output is untrusted unless
	// the tool's taint declaration says otherwise.
	IsUntrusted bool
}

// Tool is the self-describing capability interface every built-in and
// connector-exposed tool implements (FR-007, FR-008, FR-009, FR-011).
// Safety-relevant checks -- CheckPermissions and IsConcurrencySafe -- are
// evaluated per invocation against the invocation's parsed input, never as
// a fixed, constructor-time, or tool-name-keyed property (TD1): a tool is
// granted no more authority than a given invocation's input warrants.
type Tool interface {
	// ID returns the tool's fully-qualified identity.
	ID() ToolRef
	// Description returns a human-readable description of what the tool
	// does.
	Description() string
	// InputSchema returns the JSON Schema an invocation's input must
	// satisfy.
	InputSchema() json.RawMessage
	// OutputSchema returns the JSON Schema the tool's output conforms to,
	// or nil -- an output schema is optional.
	OutputSchema() json.RawMessage
	// DescriptorDigest returns a content digest of the tool's descriptor
	// (identity, schemas, and declared metadata), used to detect drift.
	DescriptorDigest() []byte
	// Disclosure reports whether the tool's schema is kept resident in
	// the cache-stable prefix or loaded on demand.
	Disclosure() Disclosure
	// Taint returns the tool's declared Rule-of-Two taint legs.
	Taint() TaintDeclaration
	// EffectClass returns the tool's high-impact-action category for the
	// approval system.
	EffectClass() EffectClass
	// IsConcurrencySafe reports whether this specific invocation, given
	// input, is safe to run concurrently with other invocations. This is
	// evaluated per invocation on parsed input, never as a fixed
	// property of the tool.
	IsConcurrencySafe(input json.RawMessage) bool
	// CheckPermissions reports whether this specific invocation, given
	// input, is permitted. This is evaluated per invocation on parsed
	// input, never as a fixed property of the tool (TD1). Identity is
	// sourced from ctx, never from input.
	CheckPermissions(ctx context.Context, input json.RawMessage) (PermissionResult, error)
	// ValidateInput reports whether input satisfies the tool's input
	// schema and any additional invariants.
	ValidateInput(ctx context.Context, input json.RawMessage) (ValidationResult, error)
	// Call invokes the tool with input and returns its result.
	Call(ctx context.Context, input json.RawMessage) (ToolResult, error)
}
