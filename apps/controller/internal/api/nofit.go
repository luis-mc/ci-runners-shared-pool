package api

import (
	"context"
	"strings"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/api/gen"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/runners"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/scheduler"
)

// Queued jobs no runner can take (CRSP391, scheduling R5.48). The scheduler gate names a wait only for a job it
// could place, so a job that asks for a label no runner carries was shown as "Waiting for room". This reads the
// runner list (the fast fact-cache list of CRSP389, online and offline runners alike) and marks such a job
// no_runner_with_labels, with the labels nobody carries in its wait text.

// builtinLabel says whether GitHub itself gives a runner this label: self-hosted, its OS and its architecture.
func builtinLabel(r runners.Record, label string) bool {
	switch label {
	case "self-hosted":
		return true
	case "linux", "windows", "macos":
		return strings.EqualFold(r.OS, label) || (label == "macos" && strings.EqualFold(r.OS, "darwin"))
	case "x64", "arm64", "arm":
		a := strings.ToLower(r.Arch)
		return a == label || (label == "x64" && a == "amd64")
	}
	return false
}

// carries says whether the runner carries the label (case does not count, as on GitHub).
func carries(r runners.Record, label string) bool {
	l := strings.ToLower(strings.TrimSpace(label))
	if l == "" || builtinLabel(r, l) {
		return true
	}
	for _, x := range r.Labels {
		if strings.ToLower(strings.TrimSpace(x)) == l {
			return true
		}
	}
	return false
}

// unplaceable returns the labels of a job that no single runner carries together. It is empty when some runner
// carries them all, or when the job names no labels.
func unplaceable(labels []string, rs []runners.Record) []string {
	if len(labels) == 0 {
		return nil
	}
	for _, r := range rs {
		all := true
		for _, l := range labels {
			if !carries(r, l) {
				all = false
				break
			}
		}
		if all {
			return nil
		}
	}
	// Name the labels no runner has at all; when each is carried by some runner but never all together, name them all.
	var none []string
	for _, l := range labels {
		found := false
		for _, r := range rs {
			if carries(r, l) {
				found = true
				break
			}
		}
		if !found {
			none = append(none, l)
		}
	}
	if len(none) == 0 {
		return append([]string(nil), labels...)
	}
	return none
}

// markNoRunner puts no_runner_with_labels on every queued job that no runner can take. A runner list that cannot
// be read leaves the jobs as they are.
func (s *server) markNoRunner(ctx context.Context, o *gen.Overview) {
	if s.opts.Runners == nil || o.Queued == nil || len(*o.Queued) == 0 {
		return
	}
	views, err := s.opts.Runners.ListView(ctx, runners.Filter{})
	if err != nil {
		s.opts.Logger.Warn("queue: runner labels not read", "err", err)
		return
	}
	rs := make([]runners.Record, 0, len(views))
	for _, v := range views {
		rs = append(rs, v.Record)
	}
	reasons := map[int64]string{}
	for i := range *o.Queued {
		j := &(*o.Queued)[i]
		if j.State != gen.JobStateQueued || (j.WaitReason != nil && *j.WaitReason == gen.WaitReasonReserved) {
			continue
		}
		missing := unplaceable(j.Labels, rs)
		if len(missing) == 0 {
			continue
		}
		r := gen.WaitReasonNoRunnerWithLabels
		j.WaitReason, j.WaitText = &r, ptr(strings.Join(missing, ", "))
		reasons[j.Id] = strings.Join(missing, ", ")
	}
	if o.Waiting == nil {
		return
	}
	for i, w := range *o.Waiting {
		if text, ok := reasons[w.JobId]; ok {
			(*o.Waiting)[i].Reason = gen.WaitReasonNoRunnerWithLabels
			(*o.Waiting)[i].Words = scheduler.WaitWords(scheduler.WaitNoRunnerLabels, text)
		}
	}
}
