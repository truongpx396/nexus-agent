package fake

import (
	"context"

	"github.com/truongpx396/nexus-agent/backend-go/internal/provider"
)

// Provider is the deterministic, scripted implementation of provider.Provider
// (T015a, FR-097). It exists so the correctness test suite is reproducible
// and never bills a live model. Construct with New(script...); Stream
// replays the script verbatim, in order, then closes the channel.
//
// Scope note: scripted failure-mode fixtures (truncation, stall,
// malformed-stream, failover) are a planned extension point for a later
// task, not implemented here — e.g. a future NewWithError constructor or a
// Chunk variant that carries an injected error. This batch only needs the
// basic scripted-stream replay above.
type Provider struct {
	script []provider.Chunk
}

// New constructs a fake Provider that replays script, in order, on every
// call to Stream (each call gets a fresh copy of the same scripted turn —
// this is a SCRIPTED fixture provider, not a stateful mock that consumes
// itself after one use).
func New(script ...provider.Chunk) *Provider {
	return &Provider{script: script}
}

func (p *Provider) Stream(ctx context.Context, prompt provider.Prompt, tools []provider.ToolSchema) (<-chan provider.Chunk, error) {
	ch := make(chan provider.Chunk, len(p.script))
	for _, c := range p.script {
		select {
		case ch <- c:
		case <-ctx.Done():
			close(ch)
			return ch, ctx.Err()
		}
	}
	close(ch)
	return ch, nil
}
