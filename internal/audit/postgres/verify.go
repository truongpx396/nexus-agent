package postgres

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/truongpx396/nexus-agent/internal/audit"
)

// Watermark is how far a scheduled verification has got for one tenant: the
// highest receipt_seq it saw, and when it looked. The next pass verifies what is
// after it. The zero value means "from the beginning".
type Watermark struct {
	ReceiptSeq int64
	At         time.Time
}

// overlapReceipts is a variable only so a test can shrink it.
var overlapReceipts int64 = overlapReceiptsDefault

const (
	// overlapReceiptsDefault is how far back before the watermark a pass looks again. A
	// receipt_seq is handed out when the insert runs but is visible only when its
	// transaction commits, so a receipt can appear below the highest one already
	// verified; re-reading this many keeps such a late commit in the window.
	overlapReceiptsDefault = 1000
	// overlapGapWindow is how far back before the last pass the gap check looks.
	overlapGapWindow = 15 * time.Minute
)

// Verify replays every receipt and anchor for tenantID, recomputing each
// hash and signature from the stored structural fields (never Payload —
// this works identically whether or not the tenant's DEK has since been
// crypto-shredded, which is exactly what task 5.5's erasure test asserts)
// and cross-checking receipts against events for a sequence gap. Its cost grows
// with the tenant's whole history, so it is for `nexusd verify-chain` and the
// go-live check; the scheduled pass uses VerifyFrom.
func (c *Chain) Verify(ctx context.Context, tx pgx.Tx, tenantID uuid.UUID) (audit.Report, error) {
	report, _, err := c.VerifyFrom(ctx, tx, tenantID, Watermark{})
	return report, err
}

// VerifyFrom verifies the receipts and anchors written after from (less a short
// overlap) and the events recent enough to still be waiting on a receipt, and
// returns the watermark to continue from. The work is proportional to what is
// new, not to the tenant's history. From the zero Watermark it is a full replay.
// The returned watermark does not advance past a pass that found a problem, so the
// problem is reported again until someone deals with it.
func (c *Chain) VerifyFrom(ctx context.Context, tx pgx.Tx, tenantID uuid.UUID, from Watermark) (audit.Report, Watermark, error) {
	report := audit.Report{TenantID: tenantID}

	pub, currentKeyID, err := c.Signer.PublicKey(ctx)
	if err != nil {
		return audit.Report{}, from, fmt.Errorf("audit: load verifying public key: %w", err)
	}
	keys := c.keyring(pub, currentKeyID)

	started := time.Now()
	var top int64
	if err := tx.QueryRow(ctx, `SELECT COALESCE(MAX(receipt_seq), 0) FROM audit_receipts WHERE tenant_id = $1`, tenantID).Scan(&top); err != nil {
		return audit.Report{}, from, fmt.Errorf("audit: load newest receipt for tenant %s: %w", tenantID, err)
	}
	after := max(0, from.ReceiptSeq-overlapReceipts)
	var gapSince time.Time
	if from.ReceiptSeq > 0 {
		gapSince = from.At.Add(-overlapGapWindow)
	}

	if err := verifyGaps(ctx, tx, tenantID, gapSince, &report); err != nil {
		return audit.Report{}, from, err
	}
	if err := verifyReceiptChain(ctx, tx, tenantID, keys, after, &report); err != nil {
		return audit.Report{}, from, err
	}
	if err := verifyAnchors(ctx, tx, tenantID, keys, after, &report); err != nil {
		return audit.Report{}, from, err
	}
	if !report.OK() {
		return report, from, nil
	}
	return report, Watermark{ReceiptSeq: top, At: started}, nil
}

// keyring is the set of public keys a signature may be verified against: the
// signer's current key and the retired ones. A key id it does not know is
// unverifiable, which is a different finding from a signature that is wrong.
type keyring func(keyID string) (ed25519.PublicKey, bool)

func (c *Chain) keyring(current ed25519.PublicKey, currentKeyID string) keyring {
	return func(keyID string) (ed25519.PublicKey, bool) {
		if keyID == currentKeyID {
			return current, true
		}
		pub, ok := c.Retired[keyID]
		return pub, ok
	}
}

// verifyGaps finds events with no receipt. A zero since checks every event; a later
// one only events created at or after it.
func verifyGaps(ctx context.Context, tx pgx.Tx, tenantID uuid.UUID, since time.Time, report *audit.Report) error {
	rows, err := tx.Query(ctx, `
		SELECT e.session_id, e.seq
		FROM events e
		LEFT JOIN audit_receipts r ON r.session_id = e.session_id AND r.seq = e.seq
		WHERE e.tenant_id = $1 AND r.receipt_id IS NULL AND ($2::timestamptz IS NULL OR e.created_at >= $2)
		ORDER BY e.session_id, e.seq`,
		tenantID, nullableTime(since),
	)
	if err != nil {
		return fmt.Errorf("audit: query sequence gaps for tenant %s: %w", tenantID, err)
	}
	defer rows.Close()
	for rows.Next() {
		var g audit.Gap
		if err := rows.Scan(&g.SessionID, &g.MissingSeq); err != nil {
			return fmt.Errorf("audit: scan gap row: %w", err)
		}
		report.Gaps = append(report.Gaps, g)
	}
	return rows.Err()
}

func nullableTime(t time.Time) *time.Time {
	if t.IsZero() {
		return nil
	}
	return &t
}

// verifyReceiptChain checks every receipt with receipt_seq after afterSeq against
// the receipt before it in its own session, which is read with the receipt, so a
// window that starts in the middle of a session's chain is still checked.
func verifyReceiptChain(ctx context.Context, tx pgx.Tx, tenantID uuid.UUID, keys keyring, afterSeq int64, report *audit.Report) error {
	rows, err := tx.Query(ctx, `
		SELECT r.session_id, r.seq, r.event_id, r.event_type, r.payload_digest, r.prev_hash, r.hash, r.signature, r.signer_key_id, p.hash
		FROM audit_receipts r
		LEFT JOIN audit_receipts p ON p.session_id = r.session_id AND p.seq = r.seq - 1
		WHERE r.tenant_id = $1 AND r.receipt_seq > $2
		ORDER BY r.session_id, r.seq ASC`,
		tenantID, afterSeq,
	)
	if err != nil {
		return fmt.Errorf("audit: query receipts for tenant %s: %w", tenantID, err)
	}
	defer rows.Close()

	for rows.Next() {
		var sessionID, eventID uuid.UUID
		var seq int64
		var eventType, signerKeyID string
		var payloadDigest, prevHash, hash, sig, expectedPrev []byte
		if err := rows.Scan(&sessionID, &seq, &eventID, &eventType, &payloadDigest, &prevHash, &hash, &sig, &signerKeyID, &expectedPrev); err != nil {
			return fmt.Errorf("audit: scan receipt row: %w", err)
		}
		report.ReceiptsChecked++

		if !bytesEqualNilAware(prevHash, expectedPrev) {
			report.Breaks = append(report.Breaks, audit.Break{SessionID: sessionID, Seq: seq, Kind: "hash_mismatch", Detail: "stored prev_hash does not match the previous receipt's hash"})
		}

		want := audit.HashInput(prevHash, tenantID, sessionID, seq, eventID, eventType, payloadDigest)
		pub, known := keys(signerKeyID)
		switch {
		case !bytesEqualNilAware(want, hash):
			report.Breaks = append(report.Breaks, audit.Break{SessionID: sessionID, Seq: seq, Kind: "hash_mismatch", Detail: "recomputed hash does not match the stored hash"})
		case !known:
			report.Breaks = append(report.Breaks, audit.Break{SessionID: sessionID, Seq: seq, Kind: "unverifiable_signer_key", Detail: fmt.Sprintf("signed by %q, which is neither the current signer key nor a retired one on file", signerKeyID)})
		case !ed25519.Verify(pub, hash, sig):
			report.Breaks = append(report.Breaks, audit.Break{SessionID: sessionID, Seq: seq, Kind: "signature_invalid", Detail: "signature does not verify against the key it names"})
		}
	}
	return rows.Err()
}

type anchorRow struct {
	anchorID       uuid.UUID
	fromSeq, toSeq int64
	hash, sig      []byte
	signerKeyID    string
}

// verifyAnchors checks the anchors that end after afterSeq. The first is chained to
// the anchor before it, which is read separately.
func verifyAnchors(ctx context.Context, tx pgx.Tx, tenantID uuid.UUID, keys keyring, afterSeq int64, report *audit.Report) error {
	// Collected into a slice (and rows fully closed) BEFORE the loop below
	// issues any further query — pgx allows only one active result set per
	// connection/tx at a time, and recomputeAnchorHash below runs its own
	// tx.Query per anchor, which would otherwise nest inside this one and
	// leave the connection "busy."
	rows, err := tx.Query(ctx, `
		SELECT anchor_id, from_receipt_seq, to_receipt_seq, hash, signature, signer_key_id
		FROM audit_anchors WHERE tenant_id = $1 AND to_receipt_seq > $2 ORDER BY to_receipt_seq ASC`,
		tenantID, afterSeq,
	)
	if err != nil {
		return fmt.Errorf("audit: query anchors for tenant %s: %w", tenantID, err)
	}
	var anchors []anchorRow
	for rows.Next() {
		var a anchorRow
		if err := rows.Scan(&a.anchorID, &a.fromSeq, &a.toSeq, &a.hash, &a.sig, &a.signerKeyID); err != nil {
			rows.Close()
			return fmt.Errorf("audit: scan anchor row: %w", err)
		}
		anchors = append(anchors, a)
	}
	rerr := rows.Err()
	rows.Close()
	if rerr != nil {
		return fmt.Errorf("audit: iterate anchors for tenant %s: %w", tenantID, rerr)
	}

	var prevAnchorHash []byte
	if afterSeq > 0 {
		perr := tx.QueryRow(ctx, `SELECT hash FROM audit_anchors WHERE tenant_id = $1 AND to_receipt_seq <= $2 ORDER BY to_receipt_seq DESC LIMIT 1`, tenantID, afterSeq).Scan(&prevAnchorHash)
		if perr != nil && !errors.Is(perr, pgx.ErrNoRows) {
			return fmt.Errorf("audit: load the anchor before the window for tenant %s: %w", tenantID, perr)
		}
	}
	for _, a := range anchors {
		report.AnchorsChecked++

		want, werr := recomputeAnchorHash(ctx, tx, tenantID, a.fromSeq, a.toSeq, prevAnchorHash)
		if werr != nil {
			return werr
		}
		pub, known := keys(a.signerKeyID)
		switch {
		case !bytesEqualNilAware(want, a.hash):
			report.Breaks = append(report.Breaks, audit.Break{Seq: a.toSeq, Kind: "hash_mismatch", Detail: fmt.Sprintf("anchor %s: recomputed aggregate hash does not match the stored hash", a.anchorID)})
		case !known:
			report.Breaks = append(report.Breaks, audit.Break{Seq: a.toSeq, Kind: "unverifiable_signer_key", Detail: fmt.Sprintf("anchor %s: signed by %q, which is neither the current signer key nor a retired one on file", a.anchorID, a.signerKeyID)})
		case !ed25519.Verify(pub, a.hash, a.sig):
			report.Breaks = append(report.Breaks, audit.Break{Seq: a.toSeq, Kind: "signature_invalid", Detail: fmt.Sprintf("anchor %s: signature does not verify against the key it names", a.anchorID)})
		}

		prevAnchorHash = a.hash
	}
	return nil
}

func recomputeAnchorHash(ctx context.Context, tx pgx.Tx, tenantID uuid.UUID, fromSeq, toSeq int64, prevAnchorHash []byte) ([]byte, error) {
	rows, err := tx.Query(ctx,
		`SELECT hash FROM audit_receipts WHERE tenant_id = $1 AND receipt_seq > $2 AND receipt_seq <= $3 ORDER BY receipt_seq ASC`,
		tenantID, fromSeq, toSeq,
	)
	if err != nil {
		return nil, fmt.Errorf("audit: load receipts (%d,%d] for tenant %s: %w", fromSeq, toSeq, tenantID, err)
	}
	defer rows.Close()

	h := sha256.New()
	if prevAnchorHash != nil {
		h.Write(prevAnchorHash)
	} else {
		h.Write(audit.GenesisPrevHash)
	}
	for rows.Next() {
		var receiptHash []byte
		if err := rows.Scan(&receiptHash); err != nil {
			return nil, fmt.Errorf("audit: scan receipt hash: %w", err)
		}
		h.Write(receiptHash)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return h.Sum(nil), nil
}

// bytesEqualNilAware treats nil and a zero-length slice as equal — a
// receipt's prev_hash column round-trips a Go nil as SQL NULL, which pgx
// scans back as a nil []byte, so a strict bytes.Equal(nil, []byte{}) split
// would report a false break on every session's very first receipt.
func bytesEqualNilAware(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
