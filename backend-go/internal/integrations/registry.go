package integrations

import (
	"errors"
	"fmt"
)

// Port is one of the closed, twelve-value set of integration points the
// platform will ever accept a third-party adapter for
// (contracts/integration-ports.md's port map). This set is CLOSED — do not
// add a thirteenth port; a new integration category is a spec change, not
// an implementation detail.
type Port string

const (
	PortProvider        Port = "provider"
	PortQueue           Port = "queue"
	PortPlanRunner      Port = "plan_runner"
	PortTelemetryExport Port = "telemetry_export"
	PortSandbox         Port = "sandbox"
	PortConnector       Port = "connector"
	PortRetrieval       Port = "retrieval"
	PortPromptSource    Port = "prompt_source"
	PortVault           Port = "vault"
	PortEval            Port = "eval"
	PortSurface         Port = "surface"
	PortSkillSource     Port = "skill_source"
)

// AllPorts is the closed, normative set of every valid Port, in the same
// order as the constants above. TestPortEnumIsClosedAndMatchesContract
// asserts this has EXACTLY twelve entries matching the contract's port
// map — no more, no fewer.
var AllPorts = []Port{
	PortProvider, PortQueue, PortPlanRunner, PortTelemetryExport,
	PortSandbox, PortConnector, PortRetrieval, PortPromptSource,
	PortVault, PortEval, PortSurface, PortSkillSource,
}

// FeatureSupport is a capability-matrix dimension's support level. Only
// FeatureSupported is exercised by name in the current tests, but this is
// modeled as a distinct type (not a bare bool) because a real adapter's
// capability matrix has more than two states (e.g. "partial") in the full
// spec -- do not collapse it to bool.
type FeatureSupport string

const (
	FeatureSupported   FeatureSupport = "supported"
	FeatureUnsupported FeatureSupport = "unsupported"
)

// CapabilityMatrix declares what an adapter claims to support, keyed by a
// free-form dimension name. Six dimension names are RESERVED and can never
// be claimed as FeatureSupported by any adapter -- see withheldAuthorities
// below -- because the platform never delegates them (FR-131).
type CapabilityMatrix struct {
	Dimensions map[string]FeatureSupport
}

// AdapterSpec is one row describing an optional third-party integration
// adapter -- disabled or absent by default; the platform must run on
// built-in defaults alone with every row disabled (FR-131, SC-040).
type AdapterSpec struct {
	Name         string
	Port         Port
	Version      string
	Enabled      bool
	Capabilities CapabilityMatrix
}

// ErrWithheldAuthority is returned (wrapped, via errors.Is) when an
// AdapterSpec's CapabilityMatrix claims one of the six authorities the
// platform structurally never delegates to any adapter (FR-131).
var ErrWithheldAuthority = errors.New("integrations: adapter claims a withheld authority the platform never delegates")

// withheldAuthorities is the six-dimension-name closed set no
// CapabilityMatrix may ever claim as FeatureSupported. Names match
// contracts/integration-ports.md's authority-boundary table exactly:
// routing_authority (FR-037, FR-076, FR-088), cost_ceiling_authority
// (FR-083), source_of_truth (FR-006, FR-124), release_gate_authority
// (FR-043), audit_record_authority (FR-081), content_access_authority
// (FR-117, FR-118). None of these six has a dedicated Port in AllPorts --
// there is no "router" or "ledger" port an adapter could bind to in the
// first place; this map is the runtime half of that boundary, checked
// against whatever Port an adapter DOES claim (e.g. PortProvider).
var withheldAuthorities = map[string]bool{
	"routing_authority":        true,
	"cost_ceiling_authority":   true,
	"source_of_truth":          true,
	"release_gate_authority":   true,
	"audit_record_authority":   true,
	"content_access_authority": true,
}

// Registry holds the set of registered optional adapters. The zero value
// is not meaningful -- construct with NewRegistry.
type Registry struct {
	adapters []AdapterSpec
}

// NewRegistry constructs a Registry from zero or more adapter rows. Every
// row is validated against the closed Port enum and the withheld-authority
// boundary (see Register) -- if ANY row claims a withheld authority,
// construction fails with an error wrapping ErrWithheldAuthority; if any
// row names an unenumerated port, construction fails with a plain error.
// Either way the registry holds NONE of the rows (fail closed, no partial
// admission). Called with no rows at all, NewRegistry succeeds with an
// empty registry -- the platform must initialize on built-in defaults
// alone (FR-131, FR-050).
func NewRegistry(specs ...AdapterSpec) (*Registry, error) {
	r := &Registry{}
	for _, spec := range specs {
		if err := r.Register(spec); err != nil {
			return nil, err
		}
	}
	return r, nil
}

// Register adds spec to the registry after checking (a) that its Port is
// one of the twelve enumerated ports -- the enum is CLOSED, so an
// unenumerated port must be refused, not silently admitted -- and (b) its
// CapabilityMatrix against the withheld-authority boundary. A refused
// registration must not partially admit the row -- Adapters() must be
// unchanged after a failed Register call.
//
// The withheld-authority check is an ALLOWLIST, not a blocklist: for a
// withheld dimension, ANY support value other than the explicit
// FeatureUnsupported is refused. FeatureSupport is a named string type, not
// a compiler-checked closed enum, so a future value ("partial", which the
// type's own doc comment anticipates) would slip past an `== FeatureSupported`
// blocklist. "Safety Is Per-Invocation and Fails Closed: deny unless
// explicitly granted."
func (r *Registry) Register(spec AdapterSpec) error {
	if !validPort(spec.Port) {
		return fmt.Errorf("integrations: adapter %q has unenumerated port %q", spec.Name, spec.Port)
	}
	for dim, support := range spec.Capabilities.Dimensions {
		if withheldAuthorities[dim] && support != FeatureUnsupported {
			return fmt.Errorf("integrations: adapter %q claims withheld authority %q (support=%q): %w", spec.Name, dim, support, ErrWithheldAuthority)
		}
	}
	r.adapters = append(r.adapters, spec)
	return nil
}

// validPort reports whether p is one of the twelve enumerated ports.
func validPort(p Port) bool {
	for _, ap := range AllPorts {
		if ap == p {
			return true
		}
	}
	return false
}

// Adapters returns every currently-registered adapter row.
func (r *Registry) Adapters() []AdapterSpec {
	return r.adapters
}
