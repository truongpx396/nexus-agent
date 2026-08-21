// Package observability holds the content-free export allowlist (T024a)
// and the trace<->event-log join (T024c, join half only). Dev Workflow
// (constitution): "observability captures STRUCTURE, not content — a
// deny-by-default attribute allowlist, unlisted key DROPPED not truncated,
// no flag/env var may ever admit content (no LOG_PROMPTS equivalent)."
package observability

import "context"

// Exporter is the sink AllowlistProcessor writes to once a span attribute /
// metric label / log field has survived the allowlist. Production wires
// this to an OTLP exporter; tests supply an in-memory fake.
type Exporter interface {
	ExportSpan(ctx context.Context, name string, attrs map[string]string)
	ExportMetric(ctx context.Context, name string, value float64, labels map[string]string)
	ExportLog(ctx context.Context, msg string, fields map[string]string)
}

// SpanAttrAllowlist / MetricLabelAllowlist / LogFieldAllowlist are the
// fixed, deny-by-default key sets for each call site (FR-117, FR-122). A
// key absent from the relevant set is DROPPED, never truncated or
// hashed-and-kept. No exported function, method, or environment variable
// in this package can add to them at runtime.
var SpanAttrAllowlist = map[string]bool{
	"tenant.id":       true,
	"session.id":      true,
	"trace.id":        true,
	"span.id":         true,
	"model.id":        true,
	"execution.class": true,
	"terminal.reason": true,
	"error.class":     true,
	"error.digest":    true, // hard-tested: truncation-at-bound test
}

var MetricLabelAllowlist = map[string]bool{
	"tenant":          true, // hard-tested
	"model":           true,
	"surface":         true,
	"terminal_reason": true,
	"execution_class": true,
}

var LogFieldAllowlist = map[string]bool{
	"session_id":   true, // hard-tested
	"trace_id":     true,
	"span_id":      true,
	"event_type":   true,
	"error_class":  true,
	"error_digest": true,
}

// MaxAttrValueLen bounds a surviving (allowlisted) attribute value --
// bounded, not content-free by truncation: a DROPPED key's value is
// wholly absent, never present-but-shortened. A key that IS allowlisted
// still gets its value truncated to this bound before export, defensively.
const MaxAttrValueLen = 256

// AllowlistProcessor is the ONE choke point every span/metric/log emission
// passes through before reaching the Exporter. There is deliberately no
// configuration field, method, or environment variable that can admit a
// non-allowlisted key (FR-117: "no environment variable, debug mode, or
// support flag that changes this") -- do not add ANY exported method
// beyond EmitSpan/EmitMetric/EmitLog, and do not read any environment
// variable anywhere in this file.
type AllowlistProcessor struct {
	exporter Exporter
}

// NewAllowlistProcessor constructs an AllowlistProcessor writing to exp.
func NewAllowlistProcessor(exp Exporter) *AllowlistProcessor {
	return &AllowlistProcessor{exporter: exp}
}

// EmitSpan filters attrs through SpanAttrAllowlist before exporting.
func (p *AllowlistProcessor) EmitSpan(ctx context.Context, name string, attrs map[string]string) {
	p.exporter.ExportSpan(ctx, name, filterAndBound(attrs, SpanAttrAllowlist))
}

// EmitMetric filters labels through MetricLabelAllowlist before exporting.
func (p *AllowlistProcessor) EmitMetric(ctx context.Context, name string, value float64, labels map[string]string) {
	p.exporter.ExportMetric(ctx, name, value, filterAndBound(labels, MetricLabelAllowlist))
}

// EmitLog filters fields through LogFieldAllowlist before exporting.
func (p *AllowlistProcessor) EmitLog(ctx context.Context, msg string, fields map[string]string) {
	p.exporter.ExportLog(ctx, msg, filterAndBound(fields, LogFieldAllowlist))
}

// filterAndBound drops every key not in allowlist, and truncates every
// SURVIVING value to MaxAttrValueLen. This is the ONE place both rules are
// enforced, shared by all three Emit* methods.
func filterAndBound(in map[string]string, allowlist map[string]bool) map[string]string {
	out := make(map[string]string, len(in))
	for k, v := range in {
		if !allowlist[k] {
			continue // deny-by-default: dropped entirely, not truncated
		}
		if len(v) > MaxAttrValueLen {
			v = v[:MaxAttrValueLen]
		}
		out[k] = v
	}
	return out
}
