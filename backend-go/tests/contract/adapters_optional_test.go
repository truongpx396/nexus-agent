// Package contract holds cross-cutting contract tests for
// specs/001-agent-platform/contracts (the kernel ABI seams and the
// integration-ports authority boundary). This file covers T029g: every
// optional integration adapter is optional — the core build never takes a
// third-party adapter as a hard dependency, and the platform initializes
// and runs with every IntegrationAdapter row disabled (FR-131, SC-040).
package contract

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"

	"github.com/truongpx396/nexus-agent/backend-go/internal/integrations"
)

// thirdPartyAdapterImport matches source text referencing a known optional
// adapter's client library. It is intentionally over-broad (word-boundary
// substring, not an import-path parser) because the property under test is
// "no reference at all outside internal/integrations/", and a coarse net
// catches more false positives than false negatives — the opposite mistake
// would silently let a hard dependency through.
var thirdPartyAdapterImport = regexp.MustCompile(`(?i)\b(litellm|temporal|langfuse|opik|pgvector|qdrant|braintrust|agui|skillhub)\b`)

// backendGoRoot resolves the backend-go module root from this file's own
// path via runtime.Caller, so the walk is correct regardless of the
// directory `go test` happens to be invoked from.
func backendGoRoot(t *testing.T) string {
	t.Helper()

	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller(0) failed; cannot locate the backend-go module root")
	}

	// This file lives at backend-go/tests/contract/adapters_optional_test.go.
	root := filepath.Clean(filepath.Join(filepath.Dir(thisFile), "..", ".."))
	if filepath.Base(root) != "backend-go" {
		t.Fatalf("resolved backend-go root as %q, which does not end in \"backend-go\"", root)
	}
	return root
}

// TestNoHardDependencyOnOptionalAdapters walks go.mod and every .go file
// under backend-go/ and asserts no known third-party adapter library is
// referenced outside internal/integrations/ — adapters are opt-in plugins,
// never a hard dependency of the core build (FR-131).
func TestNoHardDependencyOnOptionalAdapters(t *testing.T) {
	root := backendGoRoot(t)
	adaptersPrefix := filepath.Join("internal", "integrations") + string(filepath.Separator)

	assertNoThirdPartyImport := func(t *testing.T, path string, content []byte) {
		t.Helper()
		for i, line := range strings.Split(string(content), "\n") {
			if thirdPartyAdapterImport.MatchString(line) {
				t.Errorf(
					"%s:%d: references a third-party adapter (%q) outside internal/integrations/ — "+
						"optional adapters must be opt-in plugins, never a hard dependency of the core build (FR-131)",
					path, i+1, strings.TrimSpace(line),
				)
			}
		}
	}

	goModPath := filepath.Join(root, "go.mod")
	modData, err := os.ReadFile(goModPath)
	if err != nil {
		t.Fatalf("reading %s: %v", goModPath, err)
	}
	assertNoThirdPartyImport(t, goModPath, modData)

	walkErr := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(path, ".go") {
			return nil
		}

		rel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			return relErr
		}
		if strings.HasPrefix(rel, adaptersPrefix) {
			return nil // adapters live here by design (FR-131)
		}

		src, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		assertNoThirdPartyImport(t, path, src)
		return nil
	})
	if walkErr != nil {
		t.Fatalf("walking %s: %v", root, walkErr)
	}
}

// TestRegistryInitializesWithEveryAdapterDisabled constructs the
// IntegrationAdapter registry with every adapter row disabled or absent and
// asserts it initializes successfully on built-in defaults alone: no error,
// no panic (FR-131, FR-050).
func TestRegistryInitializesWithEveryAdapterDisabled(t *testing.T) {
	t.Run("absent", func(t *testing.T) {
		reg, err := integrations.NewRegistry()
		if err != nil {
			t.Fatalf("NewRegistry() with no adapter rows returned error = %v, want nil (built-in defaults only)", err)
		}
		if reg == nil {
			t.Fatal("NewRegistry() returned a nil registry with a nil error")
		}
		if got := reg.Adapters(); len(got) != 0 {
			t.Fatalf("Adapters() = %d entries with no rows registered, want 0", len(got))
		}
	})

	t.Run("disabled", func(t *testing.T) {
		disabled := []integrations.AdapterSpec{
			{Name: "litellm-gateway", Port: integrations.PortProvider, Version: "1.0.0", Enabled: false},
			{Name: "temporal-queue", Port: integrations.PortQueue, Version: "1.0.0", Enabled: false},
			{Name: "langfuse-export", Port: integrations.PortTelemetryExport, Version: "1.0.0", Enabled: false},
		}

		reg, err := integrations.NewRegistry(disabled...)
		if err != nil {
			t.Fatalf("NewRegistry() with every row disabled returned error = %v, want nil", err)
		}

		got := reg.Adapters()
		if len(got) != len(disabled) {
			t.Fatalf("Adapters() = %d entries, want %d", len(got), len(disabled))
		}
		for _, a := range got {
			if a.Enabled {
				t.Errorf("adapter %q is Enabled = true, want false — every row starts disabled in this scenario", a.Name)
			}
		}
	})
}

// TestRegistryInitializesWithoutPanicking is a narrow companion to
// TestRegistryInitializesWithEveryAdapterDisabled: it exists so a panic
// inside construction fails this test by name rather than aborting the
// whole package's test run anonymously.
func TestRegistryInitializesWithoutPanicking(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("NewRegistry() panicked: %v", r)
		}
	}()

	if _, err := integrations.NewRegistry(); err != nil {
		t.Fatalf("NewRegistry() error = %v, want nil", err)
	}
}

// TestCompleteRunPassesWithEveryAdapterDisabled is the full contract +
// integration suite assertion from T029g's original task text: "a complete
// run passes with every adapter disabled." Driving one complete run
// requires the kernel loop (RunControl, the Provider, Persistence, and
// BudgetGate wired together end-to-end), which does not exist until
// Phase 3 (US1). This is written as a real, skipped test — not an
// omission — so the obligation stays visible in `go test` output and gets
// picked up when the loop lands.
func TestCompleteRunPassesWithEveryAdapterDisabled(t *testing.T) {
	t.Skip("needs a running kernel loop end-to-end, deferred to Phase 3 (US1)")

	// Once the US1 kernel loop exists, this test will:
	//   1. Construct an integrations.Registry with every adapter row
	//      disabled (built-in defaults only: NATS JetStream queue, the
	//      platform plan evaluator, OTLP telemetry export, the OCI/gVisor
	//      sandbox, and so on).
	//   2. Submit one run through RunControl and drive it to a terminal
	//      state using only those built-in defaults — no adapter row
	//      touched.
	//   3. Assert the run reaches a valid TerminalReason, proving optional
	//      integrations are strictly additive and never load-bearing for a
	//      passing run (FR-131, SC-040).
}
