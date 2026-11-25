package store

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"github.com/luis-mc/ci-runners-shared-pool/internal/store/sqlcgen"
	"strconv"
)

// AuditHash is the hash an audit row stores (observability R5.28): SHA-256 over the hash of the row before it
// and every field of the row, each field written with its length so no two rows give the same input. The row id
// is not part of it: the id is only known after the insert, and the chain already fixes the order.
func AuditHash(prev, at string, e AuditEntry) string {
	h := sha256.New()
	for _, f := range []string{prev, at, e.Actor, e.ActorID, e.Source, e.Action, e.TargetType, e.TargetID, e.Detail, e.Result} {
		h.Write([]byte(strconv.Itoa(len(f))))
		h.Write([]byte{':'})
		h.Write([]byte(f))
	}
	return hex.EncodeToString(h.Sum(nil))
}

// AuditChainRow is an audit row with the fields the chain covers, as stored.
type AuditChainRow struct {
	ID       int64
	At       string // as stored, the text the hash was made from
	Entry    AuditEntry
	PrevHash string
	Hash     string
}

// ListAuditChain returns up to limit rows with an id above afterID, oldest first. It reads the text the hash was
// made from, so a verifier does not depend on a time round trip.
func (s *Store) ListAuditChain(ctx context.Context, afterID int64, limit int) ([]AuditChainRow, error) {
	ctx, cancel := withDeadline(ctx)
	defer cancel()
	rows, err := s.q.ListAuditChain(ctx, sqlcgen.ListAuditChainParams{ID: afterID, Limit: int64(limit)})
	if err != nil {
		return nil, fmt.Errorf("list audit chain: %w", err)
	}
	out := make([]AuditChainRow, 0, len(rows))
	for _, r := range rows {
		out = append(out, AuditChainRow{
			ID: r.ID, At: r.At, PrevHash: r.PrevHash, Hash: r.Hash,
			Entry: AuditEntry{Actor: r.Actor, ActorID: r.ActorID, Source: r.Source, Action: r.Action,
				TargetType: r.TargetType, TargetID: r.Subject, Detail: r.Detail, Result: r.Result},
		})
	}
	return out, nil
}

const chainBatch = 500

// ChainReport is the result of one check of the audit chain.
type ChainReport struct {
	// OK is true when every chained row checked out.
	OK bool
	// Checked is the number of chained rows read.
	Checked int64
	// Unchained is the number of old rows that carry no hash (written before the chain). They are not an error.
	Unchained int64
	// BrokenID is the id of the first row that does not check out, or 0.
	BrokenID int64
	// Reason says in words why that row is broken, or is empty.
	Reason string
}

// Words is one line for a person: what was checked, and the first broken row when there is one.
func (r ChainReport) Words() string {
	if !r.OK {
		return fmt.Sprintf("the audit log is not intact: row %d %s (%d rows checked before it)", r.BrokenID, r.Reason, r.Checked-1)
	}
	if r.Checked == 0 {
		return "the audit log has no chained rows yet"
	}
	s := fmt.Sprintf("the audit log is intact: %d rows checked", r.Checked)
	if r.Unchained > 0 {
		s += fmt.Sprintf(", %d older rows have no hash", r.Unchained)
	}
	return s
}

// VerifyAuditChain walks the whole log oldest first and names the first row that does not check out. A row with
// no hash before the first hashed row was written before the chain and is counted, not an error. The oldest
// chained row has its own hash checked; its link to a row that retention deleted is not.
func (s *Store) VerifyAuditChain(ctx context.Context) (ChainReport, error) {
	var rep ChainReport
	var after int64
	var prev string
	started := false
	fail := func(id int64, reason string) (ChainReport, error) {
		rep.BrokenID, rep.Reason = id, reason
		return rep, nil
	}
	for {
		rows, err := s.ListAuditChain(ctx, after, chainBatch)
		if err != nil {
			return ChainReport{}, err
		}
		if len(rows) == 0 {
			rep.OK = true
			return rep, nil
		}
		for _, r := range rows {
			after = r.ID
			if !started && r.Hash == "" && r.PrevHash == "" {
				rep.Unchained++
				continue
			}
			rep.Checked++
			switch {
			case r.Hash == "":
				return fail(r.ID, "has no hash")
			case started && r.PrevHash != prev:
				return fail(r.ID, "does not follow the row before it")
			case AuditHash(r.PrevHash, r.At, r.Entry) != r.Hash:
				return fail(r.ID, "was changed after it was written")
			}
			started, prev = true, r.Hash
		}
	}
}
