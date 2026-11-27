package runners

import (
	"context"
	"fmt"
	"strconv"
)

// Peak is the most runners one repository can use on a machine: the pool
// runners (which serve every repository) plus the runners made for that
// repository alone (scheduling R5.73). Hidden registrations are not runners
// and failed runners take no job, so neither counts. The second result is the
// scope key of the busiest repository, empty when only pool runners exist.
func Peak(rs []Record, machineID string) (int, string) {
	pool := 0
	scoped := map[string]int{}
	for _, r := range rs {
		if r.MachineID != machineID || IsRegistration(r) || r.State == Failed {
			continue
		}
		if IsPoolRunner(r) {
			pool++
			continue
		}
		scoped[scopeKey(r.Scope)]++
	}
	best, key := 0, ""
	for k, n := range scoped {
		if n > best || (n == best && k < key) {
			best, key = n, k
		}
	}
	return pool + best, key
}

func scopeKey(s Scope) string {
	if s.Organization != "" {
		return "org:" + s.Organization
	}
	return "repo:" + strconv.FormatInt(s.RepositoryID, 10)
}

// checkSlotCap refuses a plan that would give one repository more runners on a
// machine than the machine has job slots. A runner the machine has no slot for
// would take a job from GitHub and then hold it in the job-started hook, and
// that hold counts against the job's timeout-minutes (scheduling R5.73). A
// machine whose slots are not known (0) is not checked.
func (s *Service) checkSlotCap(ctx context.Context, m Machine, req AddRequest) error {
	if m.JobSlots <= 0 {
		return nil
	}
	rs, err := s.Store.ListRunners(ctx, Filter{MachineID: m.ID})
	if err != nil {
		return err
	}
	// Add the new runners as records of the request's scope and count again.
	sc := req.Scope
	if req.PoolLevel {
		sc = Scope{Kind: ScopePool}
	}
	for i := 0; i < req.Count; i++ {
		rs = append(rs, Record{MachineID: m.ID, Scope: sc})
	}
	if n, _ := Peak(rs, m.ID); n > m.JobSlots {
		return refuse("machine %s has %d job slots, so one repository can have at most %d runners there and this would make %d; "+
			"a runner without a free slot takes a job and then holds it, and that wait counts against the job's timeout. "+
			"Add fewer runners, or raise Job slots on the machine page", m.Name, m.JobSlots, m.JobSlots, n)
	}
	return nil
}

// SlotCapText is the doctor's wording for a machine over its slots.
func SlotCapText(machine string, runners, slots int) string {
	return fmt.Sprintf("%s has %d runners for one repository and only %d job slots", machine, runners, slots)
}
