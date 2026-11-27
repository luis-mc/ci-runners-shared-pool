package debugaccess

import (
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/store"
)

// How a session ended (R5.24).
const (
	EndTimeLimit  = "time_limit"
	EndIdle       = "idle"
	EndJobEnded   = "job_ended"
	EndUser       = "user"
	EndAdmin      = "admin"
	EndKillSwitch = "kill_switch"
	EndLinkLost   = "link_lost"
	EndError      = "error"
)

// fullEnd is when a session ends by its time limit alone: its limit, plus the
// one extension when it took it.
func fullEnd(s store.DebugSession) time.Time {
	end := s.OpenedAt.Add(time.Duration(s.LimitMin) * time.Minute)
	if s.Extended {
		end = end.Add(ExtendBy)
	}
	return end
}

// Due says whether an open session must end now and why: the time limit
// (R5.17), the idle limit (R5.18) or the notice after its job ended (R5.20).
func Due(s store.DebugSession, now time.Time) (string, bool) {
	if !s.EndedAt.IsZero() {
		return "", false
	}
	if !now.Before(s.EndsAt) {
		if s.EndsAt.Before(fullEnd(s)) {
			return EndJobEnded, true
		}
		return EndTimeLimit, true
	}
	idle := time.Duration(s.IdleMin) * time.Minute
	if idle > 0 && !now.Before(s.LastInputAt.Add(idle)) {
		return EndIdle, true
	}
	return "", false
}

// ExtendTo is the new end of a session that asks for its one extension
// (R5.19). It never passes the repository's own session limit from the open:
// repoLimitMin is that limit as the repository sets it now, not a fixed ceiling.
func ExtendTo(s store.DebugSession, now time.Time, repoLimitMin int) (time.Time, *Refusal) {
	if !s.EndedAt.IsZero() || !now.Before(s.EndsAt) {
		return time.Time{}, &Refusal{Code: CodeEnded, Text: "This session has ended."}
	}
	if s.Extended {
		return time.Time{}, &Refusal{Code: CodeExtended, Text: "A session can be extended once."}
	}
	if s.EndsAt.Before(fullEnd(s)) {
		// The job ended; the notice cannot be stretched.
		return time.Time{}, &Refusal{Code: CodeEnded, Text: "The job has ended, so the session cannot be extended."}
	}
	next := s.EndsAt.Add(ExtendBy)
	if hard := s.OpenedAt.Add(time.Duration(repoLimitMin) * time.Minute); next.After(hard) {
		// Clamp at the repository's limit; with no room left there is nothing to extend.
		if !hard.After(s.EndsAt) {
			return time.Time{}, &Refusal{Code: CodePastLimit, Text: "The session is at this repository's session limit."}
		}
		next = hard
	}
	return next, nil
}

// Long is true when an open session has passed 80% of its limit (the
// debug.session.long notice, R5.27).
func Long(s store.DebugSession, now time.Time) bool {
	if !s.EndedAt.IsZero() {
		return false
	}
	total := s.EndsAt.Sub(s.OpenedAt)
	return total > 0 && now.Sub(s.OpenedAt)*5 >= total*4
}
