// Package runs is the application layer between the surfaces and whatever
// executes a run: the vocabulary every surface speaks when it starts or
// resumes one, and the two ports (Starter, Resumer) cmd/nexusd satisfies with
// the kernel.
//
// It exists so each surface (REST, Telegram, Zalo, email, cron) stops
// declaring its own structurally-identical copy of these types. They used to,
// on the reasoning that surfaces must not import each other; a package that
// none of them is, and that imports neither the kernel nor any surface, is
// the shared dependency that reasoning was missing. This package must stay
// that way — tests/contract forbids surfaces from reaching the kernel
// transitively, so runs may not import it.
package runs

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"

	"github.com/truongpx396/nexus-agent/internal/crypto"
	"github.com/truongpx396/nexus-agent/internal/eventlog"
	"github.com/truongpx396/nexus-agent/internal/provider"
)

// SealFunc seals one event's plaintext payload: the sealed bytes, a digest
// over the plaintext (it survives crypto-shredding), and the id of the key it
// was sealed under. cmd/nexusd converts it to kernel.SealFunc, a structurally
// identical type, without this package naming the kernel.
type SealFunc func(plaintext []byte) (sealed, digest []byte, keyID string, err error)

// SealFor returns the SealFunc for events appended to one session under dek.
// The tenant and session ids are bound into every ciphertext as associated
// data, so an event sealed for one session cannot be opened as another's.
func SealFor(dek crypto.DEK, tenantID, sessionID uuid.UUID) SealFunc {
	return func(plaintext []byte) (sealed, digest []byte, keyID string, err error) {
		sealed, err = crypto.Seal(dek, plaintext, tenantID.String(), sessionID.String())
		if err != nil {
			return nil, nil, "", fmt.Errorf("seal event payload: %w", err)
		}
		return sealed, crypto.Digest(plaintext), dek.KeyID, nil
	}
}

// Request is everything Starter.StartRun needs to begin one run. The session
// row has already been durably created before it is built. A surface leaves
// the fields it has no use for at their zero values.
type Request struct {
	SessionID     uuid.UUID
	TenantID      uuid.UUID
	Seal          SealFunc
	Input         string
	ModelID       string
	AutonomyLevel string

	// ExtraCatalog and ExtraLoadedTools are this run's own addition to the
	// process-wide tool catalog — the per-user MCP servers resolved when the
	// session was created, whose digest is already folded into the session's
	// harness_digest. Zero for every surface that attaches no MCP servers.
	ExtraCatalog     []provider.ToolSchema
	ExtraLoadedTools []string

	// Conversational opts the run into pause-not-terminate semantics
	// (kernel.RunConfig.Conversational): a plain reply suspends the session
	// awaiting the next message instead of ending the run.
	Conversational bool
}

// Event is one item on the channel a Starter or Resumer returns. It mirrors
// the (event, error) pair kernel.Kernel.Run yields, so a surface can relay a
// run without naming the kernel.
type Event struct {
	Event eventlog.Event
	Err   error
}

// Starter begins a run. cmd/nexusd's kernelRunStarter is the only
// implementation: the one place a kernel.RunState/RunConfig is built.
type Starter interface {
	// StartRun accepts a run and returns once it is queued, not once it
	// completes. The channel holds only the events its own synchronous opening
	// step appended (Kernel.Seed: the tool and memory pins and the user
	// message), in order, and is closed after them. Everything later is written
	// by whichever worker drives the run, which publishes each event itself, so
	// a caller that relays this channel never sees an event twice.
	StartRun(ctx context.Context, req Request) (<-chan Event, error)
}

// ErrSessionEnded is what a Resumer reports for a session that no longer takes
// messages (it finished, or was ended), so the caller starts a new one.
var ErrSessionEnded = errors.New("session has ended")

// Resumer delivers a person's next message to a session that already exists.
// The session is rehydrated from its durable log, so the caller supplies only
// identifiers and the new input.
type Resumer interface {
	// SendMessage continues a conversation paused awaiting input with the
	// message, and appends it to a session that is still working or waiting on
	// a decision, where the next turn reads it. A message is never dropped:
	// a session that has ended refuses it (runctl.ErrSessionState), and the
	// caller starts a new session instead. Replies are not returned here; the
	// worker that drives the turn publishes them.
	SendMessage(ctx context.Context, tenantID, sessionID uuid.UUID, input string) error
}
