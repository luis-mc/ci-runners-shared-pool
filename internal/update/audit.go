package update

import (
	"strings"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/internal/update/lifecycle"
)

// AuditTarget is the audit target type of every update row; the target id is the version.
const AuditTarget = "update"

// AuditAction names an audit row: update.<kind>.<result>. Kinds are check, install, rollback,
// refused, skip and remove; the remove command writes its own rows with the same names.
func AuditAction(kind, result string) string { return "update." + kind + "." + result }

// RowAction is the audit action of a lifecycle row.
func RowAction(r lifecycle.Row) string {
	result := r.Result
	if r.Kind == "refused" && r.Detail != "" {
		result = r.Detail // bad_signature, bad_hash, expired, older_version
	}
	return AuditAction(r.Kind, result)
}

// HistoryFromAudit reads an audit row written by RowAction back into a history row.
// ok is false for a row that is not an update row.
func HistoryFromAudit(at time.Time, actor, action, version string) (HistoryRow, bool) {
	parts := strings.SplitN(action, ".", 3)
	if len(parts) != 3 || parts[0] != "update" {
		return HistoryRow{}, false
	}
	kind, result := parts[1], parts[2]
	switch kind {
	case "refused":
		kind, result = "install", "refused"
	case "skip":
		kind, result = "install", "skipped"
	case "check", "install", "rollback", "remove":
	default:
		return HistoryRow{}, false
	}
	return HistoryRow{At: at, Actor: actor, Action: kind, Version: version, Result: result}, true
}
