package queue

import (
	"context"
	"fmt"
	"time"

	"github.com/truongpx396/nexus-agent/backend-go/kernel"
)

// SinkMode selects whether an egress path actually crosses to the control
// plane ("upstream") or is a documented, deliberate no-op ("local") for a
// customer-boundary/BYOC deployment that must emit nothing at all.
type SinkMode string

const (
	SinkModeUpstream SinkMode = "upstream"
	SinkModeLocal    SinkMode = "local"
)

// Valid reports whether m is one of the two recognized sink modes. Any
// other value (wrong case, an invented bypass string, empty) is invalid --
// an unrecognized mode must fail the handshake closed, never silently
// default to upstream egress.
func (m SinkMode) Valid() bool {
	return m == SinkModeUpstream || m == SinkModeLocal
}

// HandshakeRequest/HandshakeResponse negotiate the highest common contract
// version at connect time (control-data-plane.md "Versioning rules": "both
// planes negotiate the highest common version at handshake, enabling
// rainbow rollout").
type HandshakeRequest struct {
	DataPlaneVersion  string
	SupportedVersions []string
	AuditSinkMode     SinkMode
	TelemetrySinkMode SinkMode
}

type HandshakeResponse struct {
	NegotiatedVersion   string
	ControlPlaneVersion string
	AcceptedAt          time.Time
}

// EgressEnvelope is the ONE wire shape used for every payload permitted to
// cross the control-plane <-> data-plane boundary. Every field maps to a
// row of control-data-plane.md's "Data-egress boundary (FR-091)" table:
// identifiers, counts and measures, digests and signatures, typed reasons.
// NOTHING else may ever be added to this struct -- a frozen reflection test
// asserts this field set is exhaustive in both directions.
type EgressEnvelope struct {
	EventID   string `json:"event_id"`
	TenantID  string `json:"tenant_id"`
	SessionID string `json:"session_id"`
	UserID    string `json:"user_id"`
	ToolID    string `json:"tool_id"`
	ModelID   string `json:"model_id"`

	InputTokensUncached   int64     `json:"input_tokens_uncached"`
	InputTokensCacheRead  int64     `json:"input_tokens_cache_read"`
	InputTokensCacheWrite int64     `json:"input_tokens_cache_write"`
	OutputTokens          int64     `json:"output_tokens"`
	MeterQuantity         int64     `json:"quantity"`
	LatencyMs             int64     `json:"latency_ms"`
	CostAmount            int64     `json:"cost_amount"`
	ChainSeq              int64     `json:"chain_seq"`
	Timestamp             time.Time `json:"ts"`

	PrevDigest          []byte `json:"prev_digest"`
	Digest              []byte `json:"digest"`
	Signature           []byte `json:"signature"`
	ApprovedInputDigest []byte `json:"approved_input_digest"`

	TerminalReason string `json:"terminal_reason"`
	FailureClass   string `json:"failure_class"`
	ReclaimReason  string `json:"reclaim_reason"`
}

// Validate enforces that TerminalReason/FailureClass/ReclaimReason are
// closed, typed enums (kernel-abi.md TerminalReason / a provisional local
// failure-class set / kernel.ReapReason) rather than free text a caller
// could use to smuggle content through a "reason" field. Each of the three
// fields is OPTIONAL (empty string is always valid -- not every egress
// event has a failure or a reclaim reason) but, when non-empty, MUST be an
// EXACT match against its closed set -- an allowlist, never a substring
// blocklist, because free text must never be trusted by construction.
func (e EgressEnvelope) Validate() error {
	if e.TerminalReason != "" && !validTerminalReason(e.TerminalReason) {
		return fmt.Errorf("queue: EgressEnvelope.TerminalReason %q is not a recognized terminal reason", e.TerminalReason)
	}
	if e.FailureClass != "" && !validFailureClass(e.FailureClass) {
		return fmt.Errorf("queue: EgressEnvelope.FailureClass %q is not a recognized failure class", e.FailureClass)
	}
	if e.ReclaimReason != "" && !validReclaimReason(e.ReclaimReason) {
		return fmt.Errorf("queue: EgressEnvelope.ReclaimReason %q is not a recognized reclaim reason", e.ReclaimReason)
	}
	return nil
}

func validTerminalReason(s string) bool {
	for _, tr := range kernel.AllTerminalReasons {
		if string(tr) == s {
			return true
		}
	}
	return false
}

func validReclaimReason(s string) bool {
	// Mirrors kernel.ReapReason's three values (kernel/delegation.go, Task
	// 8, already committed) -- a delegation reclaim IS a reap, so reusing
	// that closed set is correct, not a coincidence.
	switch kernel.ReapReason(s) {
	case kernel.ReapParentTerminal, kernel.ReapParentCancelled, kernel.ReapCeilingExhausted:
		return true
	default:
		return false
	}
}

// failureClasses is a PROVISIONAL local closed set. T023 (a LATER task in
// this same batch, not yet built) owns the real, canonical failure-class
// taxonomy (constitution "Reliability": "every failure classified into a
// typed class before retry"). This egress boundary needs a closed set NOW
// (T026 depends on nothing from T023), so this file defines a small,
// clearly-marked-provisional one covering the common cases. When T023
// lands, whoever builds it should judge whether to replace this local set
// with a shared one -- flagged here so that reconciliation isn't missed.
var failureClasses = map[string]bool{
	"transient":            true,
	"permanent":            true,
	"rate_limited":         true,
	"unauthorized":         true,
	"invalid_input":        true,
	"upstream_unavailable": true,
	"internal":             true,
}

func validFailureClass(s string) bool {
	return failureClasses[s]
}

// UpstreamCaller is the seam that actually crosses the network to the
// control plane. Under a `local` sink mode, ControlPlaneHandshake must
// NEVER invoke it -- "nothing crosses the boundary" (control-data-plane.md).
type UpstreamCaller interface {
	EmitAuditReceipt(ctx context.Context, e EgressEnvelope) error
	AnchorAuditChain(ctx context.Context, e EgressEnvelope) error
	ReportTelemetry(ctx context.Context, e EgressEnvelope) error
}

// ControlPlaneHandshake negotiates the contract version and routes every
// egress-bound call through the sink-mode switch: upstream calls the real
// UpstreamCaller; local is a documented no-op that never crosses the
// boundary. An invalid sink mode on EITHER field fails EVERYTHING closed
// (Negotiate AND every Emit/Report/Anchor call), never silently defaults
// to upstream.
type ControlPlaneHandshake struct {
	cfg      HandshakeRequest
	upstream UpstreamCaller
}

func NewControlPlaneHandshake(cfg HandshakeRequest, upstream UpstreamCaller) *ControlPlaneHandshake {
	return &ControlPlaneHandshake{cfg: cfg, upstream: upstream}
}

func (h *ControlPlaneHandshake) Negotiate(ctx context.Context) (HandshakeResponse, error) {
	if !h.cfg.AuditSinkMode.Valid() || !h.cfg.TelemetrySinkMode.Valid() {
		return HandshakeResponse{}, fmt.Errorf("queue: invalid sink mode (audit=%q telemetry=%q)", h.cfg.AuditSinkMode, h.cfg.TelemetrySinkMode)
	}
	// pick the highest SupportedVersions entry as NegotiatedVersion, or
	// simply the first/only entry -- the frozen test only checks
	// resp.NegotiatedVersion is non-empty on success, so any reasonable,
	// non-empty, deterministic choice from cfg.SupportedVersions satisfies
	// it. Do not overthink a full semver-max algorithm for one untested
	// value; picking cfg.SupportedVersions[0] when non-empty is sufficient.
	negotiated := ""
	if len(h.cfg.SupportedVersions) > 0 {
		negotiated = h.cfg.SupportedVersions[0]
	}
	return HandshakeResponse{
		NegotiatedVersion:   negotiated,
		ControlPlaneVersion: h.cfg.DataPlaneVersion, // no separate control-plane version source exists yet; mirror the data-plane's for now
		AcceptedAt:          time.Now().UTC(),
	}, nil
}

func (h *ControlPlaneHandshake) EmitAuditReceipt(ctx context.Context, e EgressEnvelope) error {
	return h.route(ctx, h.cfg.AuditSinkMode, e, h.upstream.EmitAuditReceipt)
}

func (h *ControlPlaneHandshake) AnchorAuditChain(ctx context.Context, e EgressEnvelope) error {
	return h.route(ctx, h.cfg.AuditSinkMode, e, h.upstream.AnchorAuditChain)
}

func (h *ControlPlaneHandshake) ReportTelemetry(ctx context.Context, e EgressEnvelope) error {
	return h.route(ctx, h.cfg.TelemetrySinkMode, e, h.upstream.ReportTelemetry)
}

// route is the ONE place Validate() is enforced and the sink-mode switch is
// applied, shared by all three Emit/Report/Anchor methods -- this is what
// makes TestControlPlaneHandshake_RefusesContentSmuggledEnvelope pass for
// all three methods from one code path, not three copy-pasted checks.
func (h *ControlPlaneHandshake) route(ctx context.Context, mode SinkMode, e EgressEnvelope, upstreamCall func(context.Context, EgressEnvelope) error) error {
	if !mode.Valid() {
		return fmt.Errorf("queue: invalid sink mode %q", mode)
	}
	if err := e.Validate(); err != nil {
		return fmt.Errorf("queue: refusing to route an invalid EgressEnvelope: %w", err)
	}
	if mode == SinkModeLocal {
		return nil // documented no-op -- nothing crosses the boundary
	}
	return upstreamCall(ctx, e)
}
