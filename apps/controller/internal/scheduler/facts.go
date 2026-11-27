package scheduler

import (
	"fmt"
	"strings"
	"sync"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/jobsdata"
)

// FactsFunc receives crsp's own facts about a job as the engine learns them:
// the wait reason while it waits, the slot it took, and a job lost for good
// (observability, Jobs section). It is called after the engine's transaction
// ended, with no lock held, and must not block: the receiver queues the work.
// The job is named as the caller named it; the receiver finds the GitHub job.
type FactsFunc func(job JobRef, f jobsdata.Facts)

// SetFacts sets where the engine reports job facts. Nil turns it off.
func (e *Engine) SetFacts(f FactsFunc) {
	e.factsMu.Lock()
	e.facts = f
	e.factsSeen = map[string]string{}
	e.factsMu.Unlock()
}

// maxFactsSeen bounds the repeat filter; past it the filter starts again, which
// at worst repeats one fact.
const maxFactsSeen = 5000

func factsKey(j JobRef) string {
	return fmt.Sprintf("%s/%d/%d/%s/%d", j.Repository, j.RunID, j.RunAttempt, j.Job, j.GitHubJobID)
}

// emit sends the fact unless the same fact of the same kind was sent last.
func (e *Engine) emit(j JobRef, kind, fingerprint string, f jobsdata.Facts) {
	e.factsMu.Lock()
	fn := e.facts
	if fn == nil || (j.GitHubJobID == 0 && (j.Repository == "" || j.RunID == 0)) {
		e.factsMu.Unlock()
		return
	}
	k := factsKey(j) + "|" + kind
	if e.factsSeen[k] == fingerprint {
		e.factsMu.Unlock()
		return
	}
	if len(e.factsSeen) >= maxFactsSeen {
		e.factsSeen = map[string]string{}
	}
	e.factsSeen[k] = fingerprint
	e.factsMu.Unlock()
	fn(j, f)
}

// emitDecision reports what a gate answer says about the job.
func (e *Engine) emitDecision(machineID string, job JobRef, d Decision) {
	switch {
	case len(d.Go) > 0:
		g := d.Go[len(d.Go)-1]
		light := g.Kind == SlotLight
		queued := g.TakenAt.Add(-d.SlotWait)
		e.emit(job, "go", g.TakenAt.String(), jobsdata.Facts{MachineID: machineID, QueuedAt: queued, SlotAt: g.TakenAt, Light: &light})
	case len(d.Wait) > 0:
		reason := string(d.Wait[0])
		e.emit(job, "wait", reason+"|"+d.WaitText, jobsdata.Facts{MachineID: machineID, WaitReason: reason, WaitText: d.WaitText})
	}
}

// emitFreed reports a slot that was freed because the job was lost: a lease or
// age that ran out, or a runner that went. A normal end (ran, completed on
// GitHub) says nothing: GitHub's event carries how the job ended.
func (e *Engine) emitFreed(freed []Freed) {
	for _, f := range freed {
		if f.Number == 0 || !lostReason(f.Reason) {
			continue
		}
		e.emit(f.Job, "lost", f.Reason, jobsdata.Facts{MachineID: f.MachineID, Lost: true})
	}
}

func lostReason(r string) bool {
	return r == "runner-gone" || r == "lease expired" || strings.HasPrefix(r, "older than ")
}

// factsState is the engine's state for the facts hook.
type factsState struct {
	factsMu   sync.Mutex
	facts     FactsFunc
	factsSeen map[string]string
}
