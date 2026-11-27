package builds

import (
	"sort"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/api/gen"
)

// SecondsLeft is the typical time minus the time so far, never below zero; it
// is absent with no typical time (no history) or a build that is not running
// (R5.70).
func SecondsLeft(typical float64, startedAt, now time.Time) (float64, bool) {
	if typical <= 0 || startedAt.IsZero() {
		return 0, false
	}
	left := typical - now.Sub(startedAt).Seconds()
	if left < 0 {
		left = 0
	}
	return left, true
}

// ToAPI shows a record as the API does. The log tail is only in the one-build
// read (R5.69); machineName is added when the caller knows it.
func ToAPI(r Record, machineName string, now time.Time, withLog bool) gen.ImageBuild {
	out := gen.ImageBuild{
		Id: r.ID, MachineId: r.MachineID, Key: r.Key,
		State: gen.ImageBuildState(r.State), PushState: gen.ImagePushState(r.PushState),
		Step: r.Step, StepsTotal: r.StepsTotal, QueuedAt: r.QueuedAt,
	}
	if out.PushState == "" {
		out.PushState = gen.ImagePushState("none")
	}
	if machineName != "" {
		out.MachineName = &machineName
	}
	if r.Profile != "" {
		p := r.Profile
		out.Profile = &p
	}
	if r.Arch != "" {
		a := gen.RegistryArch(r.Arch)
		out.Arch = &a
	}
	if r.Reason != "" {
		why := gen.ImageBuildReason(r.Reason)
		out.Reason = &why
	}
	if r.StepName != "" {
		n := r.StepName
		out.StepName = &n
	}
	if r.TypicalSeconds > 0 {
		t := float32(r.TypicalSeconds)
		out.TypicalSeconds = &t
	}
	if r.SizeBytes > 0 {
		n := r.SizeBytes
		out.SizeBytes = &n
	}
	if !r.StartedAt.IsZero() {
		t := r.StartedAt
		out.StartedAt = &t
	}
	if !r.FinishedAt.IsZero() {
		t := r.FinishedAt
		out.FinishedAt = &t
	}
	if r.State == StateBuilding && out.StartedAt != nil {
		if left, ok := SecondsLeft(r.TypicalSeconds, r.StartedAt, now); ok {
			l := float32(left)
			out.SecondsLeft = &l
		}
	}
	if r.State == StateFailed {
		out.Failure = &struct {
			Class gen.ImageBuildFailureClass `json:"class"`
			Step  string                     `json:"step"`
			Why   string                     `json:"why"`
		}{Class: gen.ImageBuildFailureClass(orOther(r.FailureClass)), Step: r.FailureStep, Why: r.FailureWhy}
	}
	if withLog && r.LogTail != "" {
		l := r.LogTail
		out.LogTail = &l
	}
	return out
}

func orOther(c string) string {
	if c == "" {
		return ClassOther
	}
	return c
}

// sortRunningFirst puts building and queued builds first, then by newest queue time.
func sortRunningFirst(b []gen.ImageBuild) {
	rank := func(x gen.ImageBuild) int {
		if x.State == StateBuilding || x.State == StateQueued {
			return 0
		}
		return 1
	}
	sort.SliceStable(b, func(i, j int) bool {
		if ri, rj := rank(b[i]), rank(b[j]); ri != rj {
			return ri < rj
		}
		return b[i].QueuedAt.After(b[j].QueuedAt)
	})
}
