// This file covers T029h — the authority boundary from
// contracts/integration-ports.md, asserted as a property: every registered
// IntegrationAdapter binds to one port in the closed, twelve-value port
// enum, and an adapter attempting to supply any of the six authorities the
// platform never delegates is refused (FR-131, FR-133, SC-040).
package contract

import (
	"errors"
	"reflect"
	"regexp"
	"testing"

	"github.com/truongpx396/nexus-agent/backend-go/internal/integrations"
)

// TestPortEnumIsClosedAndMatchesContract lists the twelve ports from
// contracts/integration-ports.md's port map explicitly, so a future change
// to the enum — an addition, removal, or rename — fails this test rather
// than passing silently because both sides drifted together.
func TestPortEnumIsClosedAndMatchesContract(t *testing.T) {
	tests := []struct {
		name  string
		port  integrations.Port
		value string
	}{
		{"provider", integrations.PortProvider, "provider"},
		{"queue", integrations.PortQueue, "queue"},
		{"plan_runner", integrations.PortPlanRunner, "plan_runner"},
		{"telemetry_export", integrations.PortTelemetryExport, "telemetry_export"},
		{"sandbox", integrations.PortSandbox, "sandbox"},
		{"connector", integrations.PortConnector, "connector"},
		{"retrieval", integrations.PortRetrieval, "retrieval"},
		{"prompt_source", integrations.PortPromptSource, "prompt_source"},
		{"vault", integrations.PortVault, "vault"},
		{"eval", integrations.PortEval, "eval"},
		{"surface", integrations.PortSurface, "surface"},
		{"skill_source", integrations.PortSkillSource, "skill_source"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if string(tt.port) != tt.value {
				t.Errorf("integrations.Port%s = %q, want %q", tt.name, tt.port, tt.value)
			}
		})
	}

	got := integrations.AllPorts
	if len(got) != len(tests) {
		t.Fatalf("integrations.AllPorts has %d entries, want %d: %v — the port map is a closed, normative set", len(got), len(tests), got)
	}

	gotSet := make(map[integrations.Port]bool, len(got))
	for _, p := range got {
		gotSet[p] = true
	}
	for _, tt := range tests {
		if !gotSet[tt.port] {
			t.Errorf("integrations.AllPorts is missing port %q", tt.port)
		}
	}

	wantSet := make(map[integrations.Port]bool, len(tests))
	for _, tt := range tests {
		wantSet[tt.port] = true
	}
	for _, p := range got {
		if !wantSet[p] {
			t.Errorf("integrations.AllPorts contains unexpected port %q not in the twelve-value contract set", p)
		}
	}
}

// TestWithheldAuthoritiesAreRefusedAtRegistration attempts to construct a
// fixture IntegrationAdapter claiming each of the six authorities
// contracts/integration-ports.md says the platform always owns, and
// asserts registration is refused with a typed error rather than silently
// admitted. The port an adapter binds to is orthogonal to this — the
// boundary is about the authority claimed, not the port — so every
// fixture uses the `provider` port, an admitted port for an ordinary
// adapter, to isolate the property under test.
func TestWithheldAuthoritiesAreRefusedAtRegistration(t *testing.T) {
	tests := []struct {
		name      string // the withheld authority, matching integration-ports.md's table rows
		dimension string // the CapabilityMatrix key a real adapter's capability matrix would use
	}{
		{"routing_authority", "routing_authority"},               // FR-037, FR-076, FR-088
		{"cost_ceiling_authority", "cost_ceiling_authority"},     // FR-083
		{"source_of_truth", "source_of_truth"},                   // FR-006, FR-124
		{"release_gate_authority", "release_gate_authority"},     // FR-043
		{"audit_record_authority", "audit_record_authority"},     // FR-081
		{"content_access_authority", "content_access_authority"}, // FR-117, FR-118
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fixture := integrations.AdapterSpec{
				Name:    "fixture-" + tt.name,
				Port:    integrations.PortProvider,
				Version: "1.0.0",
				Capabilities: integrations.CapabilityMatrix{
					Dimensions: map[string]integrations.FeatureSupport{
						tt.dimension: integrations.FeatureSupported,
					},
				},
			}

			if _, err := integrations.NewRegistry(fixture); err == nil {
				t.Fatalf("NewRegistry() admitted an adapter claiming %s; this authority is never delegated (FR-131)", tt.name)
			} else if !errors.Is(err, integrations.ErrWithheldAuthority) {
				t.Fatalf("NewRegistry() error = %v, want an error wrapping integrations.ErrWithheldAuthority", err)
			}

			// The same fixture must also be refused when registered into an
			// already-constructed registry, not only at construction time —
			// and a refusal must not partially admit the row.
			reg, err := integrations.NewRegistry()
			if err != nil {
				t.Fatalf("NewRegistry() with no rows returned error = %v, want nil", err)
			}
			if err := reg.Register(fixture); err == nil {
				t.Fatalf("Register() admitted an adapter claiming %s; this authority is never delegated (FR-131)", tt.name)
			} else if !errors.Is(err, integrations.ErrWithheldAuthority) {
				t.Fatalf("Register() error = %v, want an error wrapping integrations.ErrWithheldAuthority", err)
			}
			if got := reg.Adapters(); len(got) != 0 {
				t.Fatalf("Adapters() = %d entries after a refused registration, want 0 (refusal must fail closed, not partially admit)", len(got))
			}
		})
	}
}

// TestAdapterRegistrationSurfaceHasNoGrantingMethod is defense-in-depth
// beyond TestWithheldAuthoritiesAreRefusedAtRegistration. The routing,
// cost-ceiling, and source-of-truth authorities have no dedicated port at
// all in the twelve-value port map above — there is no "router" or
// "ledger" port an adapter could bind to in the first place — so part of
// this boundary is a structural absence rather than a runtime refusal.
// That absence is asserted here: the public registration surface exposes
// no method whose name could plausibly grant one of the six withheld
// authorities after a row is already registered.
func TestAdapterRegistrationSurfaceHasNoGrantingMethod(t *testing.T) {
	grantingMethod := regexp.MustCompile(`(?i)^Set(As)?(Route|Router|Gate|Ceiling|AuditSink|SourceOfTruth|ContentAccess)`)

	types := []struct {
		name string
		typ  reflect.Type
	}{
		{"Registry", reflect.TypeOf(&integrations.Registry{})},
		{"AdapterSpec", reflect.TypeOf(integrations.AdapterSpec{})},
	}

	for _, ty := range types {
		t.Run(ty.name, func(t *testing.T) {
			for i := 0; i < ty.typ.NumMethod(); i++ {
				name := ty.typ.Method(i).Name
				if grantingMethod.MatchString(name) {
					t.Errorf("%s.%s grants an authority the platform must never delegate (FR-131)", ty.name, name)
				}
			}
		})
	}
}
