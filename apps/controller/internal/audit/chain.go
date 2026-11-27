package audit

import (
	"context"
	"fmt"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/store"
)

// The audit hash chain (observability R5.28). Each row stores the hash of the row before it (store.AuditHash),
// so an edited, removed or inserted row in the middle of the log shows as a broken row. Rows written before the
// chain existed have no hash and are counted as unchained; the chain starts at the first row that has one. The
// retention job deletes the oldest rows, so the oldest row left is the start of what can be checked: its own
// hash is checked, its link to a deleted row is not. A row removed from the very end cannot be seen by a chain
// alone; the spec says so.

// ChainReport is the result of one check of the chain (store.ChainReport).
type ChainReport = store.ChainReport

// Verify walks the whole log oldest first and names the first row that does not check out.
func (l *Log) Verify(ctx context.Context) (ChainReport, error) { return l.db.VerifyAuditChain(ctx) }

// ChainChecker is what the doctor depends on, so it can be swapped in tests. The doctor package calls
// CheckChain with the audit log and turns the report into a finding (doctor R5.x, owned by another row).
type ChainChecker interface {
	Verify(ctx context.Context) (ChainReport, error)
}

var _ ChainChecker = (*Log)(nil)

// CheckChain is the doctor's entry: it runs the check and returns the report and a one-line statement.
// A read error is returned, not hidden, so the doctor can report "could not check" rather than "intact".
func CheckChain(ctx context.Context, c ChainChecker) (ChainReport, string, error) {
	rep, err := c.Verify(ctx)
	if err != nil {
		return ChainReport{}, "", fmt.Errorf("audit chain: %w", err)
	}
	return rep, rep.Words(), nil
}
