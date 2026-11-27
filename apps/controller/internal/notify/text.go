package notify

import (
	"fmt"
	"strings"
)

// Tones of a notice (design system UX-26). A tone is a word in the text, never colour alone (R6.8).
const (
	ToneDone     = "done"
	ToneNote     = "note"
	ToneNeedsYou = "needs_you"
	ToneProblem  = "problem"
)

// ToneWords are the tone names as people read them (UX-26).
var ToneWords = map[string]string{ToneDone: "Done", ToneNote: "Note", ToneNeedsYou: "Needs you", ToneProblem: "Problem"}

// States of an event key (R5.2).
const (
	StateFiring   = "firing"
	StateResolved = "resolved"
)

// TypeSummary is the type of the one notice that stands for notices held by the hourly cap (R5.12).
const TypeSummary = "notice.summary"

// Title is the event in plain words, for a desktop notice title and an email subject (section 4).
func Title(typ, state string) string {
	r := state == StateResolved
	switch typ {
	case "machine.offline":
		if r {
			return "Machine back online"
		}
		return "Machine offline"
	case "job.waiting":
		if r {
			return "Job started"
		}
		return "Job waiting"
	case "disk.low":
		if r {
			return "Disk space recovered"
		}
		return "Disk low"
	case "runner.outdated":
		if r {
			return "Runner up to date"
		}
		return "Runner outdated"
	case "doctor.finding":
		if r {
			return "Doctor finding gone"
		}
		return "Doctor finding"
	case "update.available":
		if r {
			return "Update installed"
		}
		return "Update available"
	case TypeSummary:
		return "More notices"
	}
	return typ
}

// EventWords names an event type in plain words, for the notices list.
func EventWords(typ string) string { return Title(typ, StateFiring) }

// resolvedSummary is the past-tense line of a resolved notice (section 4, "Desktop notice").
func resolvedSummary(typ, name string) string {
	switch typ {
	case "machine.offline":
		return name + " is back online."
	case "job.waiting":
		return name + " started or was cancelled."
	case "disk.low":
		return name + " has enough free space again."
	case "runner.outdated":
		return name + " is up to date or was removed."
	case "doctor.finding":
		return name + " is gone on the latest doctor run."
	case "update.available":
		return name + " is installed."
	}
	return name + " is resolved."
}

// groupedSummary is the one line that stands for a burst of notices of one type (R5.13).
func groupedSummary(typ, state string, n int) string {
	r := state == StateResolved
	switch typ {
	case "machine.offline":
		if r {
			return fmt.Sprintf("%d machines are back online.", n)
		}
		return fmt.Sprintf("%d machines are offline.", n)
	case "job.waiting":
		if r {
			return fmt.Sprintf("%d waiting jobs started.", n)
		}
		return fmt.Sprintf("%d jobs are waiting.", n)
	case "disk.low":
		if r {
			return fmt.Sprintf("%d disks have enough free space again.", n)
		}
		return fmt.Sprintf("%d disks are low on space.", n)
	case "runner.outdated":
		if r {
			return fmt.Sprintf("%d runners are up to date.", n)
		}
		return fmt.Sprintf("%d runners are out of date.", n)
	case "doctor.finding":
		if r {
			return fmt.Sprintf("%d doctor findings are gone.", n)
		}
		return fmt.Sprintf("%d new doctor findings.", n)
	}
	return fmt.Sprintf("%d notices about %s.", n, strings.ToLower(Title(typ, state)))
}

// heldSummary is the one summary of notices held by the hourly cap (R5.12).
func heldSummary(n int) string {
	if n == 1 {
		return "1 more notice. Open the console to see it."
	}
	return fmt.Sprintf("%d more notices. Open the console to see them.", n)
}

// toneFor is the default tone of an event (R5.1).
func toneFor(typ, severity string) string {
	switch typ {
	case "machine.offline":
		return ToneProblem
	case "update.available":
		return ToneNote
	case "doctor.finding":
		switch severity {
		case "error":
			return ToneProblem
		case "info":
			return ToneNote
		}
		return ToneNeedsYou
	}
	return ToneNeedsYou
}

func severityRank(s string) int {
	for i, v := range Severities {
		if v == s {
			return i
		}
	}
	return 1
}
