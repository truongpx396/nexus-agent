package kernel

import (
	"encoding/json"
	"fmt"
)

// Event is the in-memory decoded shape of an appended event, upcast to the
// schema_version the caller requested.
type Event struct {
	SchemaVersion int
	Type          string
	Payload       map[string]any
}

// UpcastFunc transforms an event's payload from schema_version N to N+1,
// adding, renaming, or defaulting fields as needed. It must not discard
// information the prior version's readers depended on.
type UpcastFunc func(payload map[string]any) (map[string]any, error)

// UpcastPathError is returned by UpcastEvent when no registered upcaster
// path connects an event's raw schema_version to the requested current
// version.
type UpcastPathError struct {
	FromVersion int
	ToVersion   int
}

func (e *UpcastPathError) Error() string {
	return fmt.Sprintf("kernel: no upcast path from schema_version %d to %d", e.FromVersion, e.ToVersion)
}

// upcasters is the process-global registry of schema upcasters, keyed by
// the version they upgrade FROM (fromVersion -> fromVersion+1).
var upcasters = map[int]UpcastFunc{}

// RegisterUpcaster registers the transform that upgrades a payload from
// fromVersion to fromVersion+1. Registration is process-global (a package-
// level registry) — this matches the frozen test's usage, which calls
// kernel.RegisterUpcaster at test-run time with no separate registry
// handle threaded through.
func RegisterUpcaster(fromVersion int, fn UpcastFunc) {
	upcasters[fromVersion] = fn
}

// eventEnvelope is the raw JSON shape of an appended event, decoded prior
// to any upcasting.
type eventEnvelope struct {
	SchemaVersion int            `json:"schema_version"`
	Type          string         `json:"type"`
	Payload       map[string]any `json:"payload"`
}

// UpcastEvent decodes raw (a JSON event envelope with at least
// "schema_version", "type", and "payload") and, if raw's schema_version
// does not already equal currentSchemaVersion, walks the registered
// upcaster chain (fromVersion -> fromVersion+1 -> fromVersion+2 -> ...)
// until it reaches currentSchemaVersion. An event already at
// currentSchemaVersion passes through unchanged (no upcaster lookup at
// all). An event with no registered path to currentSchemaVersion returns
// a *UpcastPathError wrapped as error -- FromVersion is the event's
// original raw schema_version (not wherever the chain happened to break),
// ToVersion is the requested currentSchemaVersion -- never a panic, never
// a zero-valued Event alongside a non-nil error, except when returning the
// error itself, where Event is the zero value.
//
// "No registered path" covers TWO distinct cases, both returning the same
// *UpcastPathError:
//   - a gap in the forward upcaster chain (some version between the event's
//     schema_version and currentSchemaVersion has no registered upcaster);
//   - an event whose schema_version is GREATER than currentSchemaVersion,
//     i.e. written by a newer deployment than this reader (a rolling
//     deploy). Upcasting is one-directional and no downcaster mechanism
//     exists, so there is genuinely no path from a higher version down to
//     currentSchemaVersion. This case MUST NOT silently relabel the event's
//     version downward over an untransformed payload.
func UpcastEvent(raw json.RawMessage, currentSchemaVersion int) (Event, error) {
	var env eventEnvelope
	if err := json.Unmarshal(raw, &env); err != nil {
		return Event{}, fmt.Errorf("kernel: decode event envelope: %w", err)
	}

	originalVersion := env.SchemaVersion

	if env.SchemaVersion == currentSchemaVersion {
		// eventEnvelope and Event have identical field sets and types (the
		// struct tags are ignored by a Go struct conversion), so this is an
		// exact, field-for-field copy.
		return Event(env), nil
	}

	if env.SchemaVersion > currentSchemaVersion {
		// The event was written under a NEWER schema than this reader knows
		// (a rolling deploy where an old worker reads an event a newer
		// worker already wrote). Upcasting is one-directional and no
		// downcaster mechanism exists, so there is genuinely no path from
		// the higher version down to currentSchemaVersion -- refuse loudly
		// rather than relabel the version downward over an untransformed
		// payload.
		return Event{}, &UpcastPathError{FromVersion: originalVersion, ToVersion: currentSchemaVersion}
	}

	payload := env.Payload
	for v := env.SchemaVersion; v < currentSchemaVersion; v++ {
		fn, ok := upcasters[v]
		if !ok {
			return Event{}, &UpcastPathError{FromVersion: originalVersion, ToVersion: currentSchemaVersion}
		}
		upcast, err := fn(payload)
		if err != nil {
			return Event{}, fmt.Errorf("kernel: upcast event from schema_version %d: %w", v, err)
		}
		payload = upcast
	}

	return Event{
		SchemaVersion: currentSchemaVersion,
		Type:          env.Type,
		Payload:       payload,
	}, nil
}
