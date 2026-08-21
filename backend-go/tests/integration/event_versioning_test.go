// Package integration holds tests that specify behavior which does not
// exist yet, so a failing build is the expected "RED" state until the
// corresponding implementation lands.
//
// Unlike its sibling files in this package, this file carries NO
// `//go:build integration` tag: it exercises only in-process code
// (kernel.RegisterUpcaster / kernel.UpcastEvent), with no Postgres or
// Docker dependency, so it must be visible to the project's default
// `go test ./...` run (.github/workflows/ci.yml has no `-tags=integration`)
// rather than hidden behind the same gate as the Docker-requiring tests.
//
// This file is the T029a / T014a integration test: it specifies the
// envelope versioning + upcasting registry that lets an event written under
// an older schema_version replay correctly after a schema change (FR-086).
// The implementer is expected to produce a backend-go/kernel package
// (backend-go/kernel/eventversion.go) shaped like this:
//
//	// Event is the in-memory decoded shape of an appended event, upcast to
//	// the schema_version the caller requested.
//	type Event struct {
//		SchemaVersion int
//		Type          string
//		Payload       map[string]any
//	}
//
//	// UpcastFunc transforms an event's payload from schema_version N to
//	// N+1, adding, renaming, or defaulting fields as needed. It must not
//	// discard information the prior version's readers depended on.
//	type UpcastFunc func(payload map[string]any) (map[string]any, error)
//
//	// UpcastPathError is returned by UpcastEvent when no registered
//	// upcaster path connects an event's raw schema_version to the
//	// requested current version.
//	type UpcastPathError struct {
//		FromVersion int
//		ToVersion   int
//	}
//
//	func (e *UpcastPathError) Error() string
//
//	// RegisterUpcaster registers the transform that upgrades a payload
//	// from fromVersion to fromVersion+1. Registration is process-global.
//	func RegisterUpcaster(fromVersion int, fn UpcastFunc)
//
//	// UpcastEvent decodes raw (a JSON event envelope with at least
//	// "schema_version", "type", and "payload") and, if raw's
//	// schema_version does not already equal currentSchemaVersion, walks
//	// the registered upcaster chain until it does. An event already at
//	// currentSchemaVersion passes through unchanged. An event with no
//	// registered path to currentSchemaVersion returns a *UpcastPathError
//	// wrapped as error -- never a panic, never a zero-valued Event
//	// alongside a non-nil error.
//	func UpcastEvent(raw json.RawMessage, currentSchemaVersion int) (Event, error)
package integration

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/truongpx396/nexus-agent/backend-go/kernel"
)

func TestEventVersioning_UpcastRegistry(t *testing.T) {
	t.Run("registered upcaster adds a defaulted field from v1 to v2", func(t *testing.T) {
		kernel.RegisterUpcaster(1, func(payload map[string]any) (map[string]any, error) {
			upcast := make(map[string]any, len(payload)+1)
			for k, v := range payload {
				upcast[k] = v
			}
			if _, ok := upcast["retry_count"]; !ok {
				upcast["retry_count"] = float64(0)
			}
			return upcast, nil
		})

		raw := json.RawMessage(`{
			"schema_version": 1,
			"type": "tool_result",
			"payload": {"tool_id": "builtin/file_read@1.0.0", "status": "ok"}
		}`)

		got, err := kernel.UpcastEvent(raw, 2)
		if err != nil {
			t.Fatalf("UpcastEvent: %v", err)
		}

		if got.SchemaVersion != 2 {
			t.Errorf("SchemaVersion = %d, want 2", got.SchemaVersion)
		}
		if got.Type != "tool_result" {
			t.Errorf("Type = %q, want %q", got.Type, "tool_result")
		}
		if got.Payload["tool_id"] != "builtin/file_read@1.0.0" {
			t.Errorf("Payload[tool_id] = %v, want the preserved original value", got.Payload["tool_id"])
		}
		if got.Payload["status"] != "ok" {
			t.Errorf("Payload[status] = %v, want the preserved original value", got.Payload["status"])
		}

		retryCount, ok := got.Payload["retry_count"]
		if !ok {
			t.Fatal("Payload[retry_count] is missing after upcasting to v2; expected the registered upcaster's default")
		}
		if retryCount != float64(0) {
			t.Errorf("Payload[retry_count] = %v, want the defaulted value 0", retryCount)
		}
	})

	t.Run("an event already at the current version passes through unchanged", func(t *testing.T) {
		raw := json.RawMessage(`{
			"schema_version": 7,
			"type": "content",
			"payload": {"text": "already current"}
		}`)

		got, err := kernel.UpcastEvent(raw, 7)
		if err != nil {
			t.Fatalf("UpcastEvent: %v", err)
		}
		if got.SchemaVersion != 7 {
			t.Errorf("SchemaVersion = %d, want 7 (unchanged passthrough)", got.SchemaVersion)
		}
		if got.Type != "content" {
			t.Errorf("Type = %q, want %q (unchanged passthrough)", got.Type, "content")
		}
		if got.Payload["text"] != "already current" {
			t.Errorf("Payload[text] = %v, want the unchanged passthrough value", got.Payload["text"])
		}
	})

	t.Run("an event with no registered upcast path returns a typed error, never a panic or a zeroed struct", func(t *testing.T) {
		raw := json.RawMessage(`{
			"schema_version": 91,
			"type": "thought",
			"payload": {"text": "orphaned schema version"}
		}`)

		var (
			got kernel.Event
			err error
		)
		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("UpcastEvent panicked on an event with no registered upcast path: %v", r)
				}
			}()
			got, err = kernel.UpcastEvent(raw, 92)
		}()

		if err == nil {
			t.Fatal("expected an error for an event with no registered upcast path, got nil")
		}

		var pathErr *kernel.UpcastPathError
		if !errors.As(err, &pathErr) {
			t.Fatalf("error = %v (%T), want errors.As match for *kernel.UpcastPathError", err, err)
		}
		if pathErr.FromVersion != 91 {
			t.Errorf("UpcastPathError.FromVersion = %d, want 91", pathErr.FromVersion)
		}
		if pathErr.ToVersion != 92 {
			t.Errorf("UpcastPathError.ToVersion = %d, want 92", pathErr.ToVersion)
		}

		if got.SchemaVersion != 0 || got.Type != "" || got.Payload != nil {
			t.Errorf("Event = %+v, want the zero value alongside a non-nil error -- never a partially-populated struct", got)
		}
	})
}
