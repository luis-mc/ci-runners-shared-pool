package runners

import (
	"context"
	"strings"
)

// ServiceStarter starts the service of a persistent runner on its machine. The
// agent answers with the same listener command the gate uses; it does nothing
// when the listener already runs (runners R5.135, R5.157).
type ServiceStarter interface {
	StartService(ctx context.Context, machineID, serviceName string) error
}

// CanStart says whether the Runners page offers Start on a runner with this
// record and shown state: a persistent runner CRSP made whose service is
// stopped or not on the machine's service list (runners R5.157).
func CanStart(r Record, shown State) bool {
	return r.Managed && r.Kind == "persistent" && shown == Stopped
}

// StartRunner starts the service of a persistent runner on the owner's request
// (the Start button and `crsp runner start`). A pool runner starts the service
// of each of its registrations. The agent tells the owner when a service starts
// and stops again at once, and why (runners R5.156). A runner CRSP did not make,
// an ephemeral runner and a runner in a container are refused.
func (s *Service) StartRunner(ctx context.Context, id, actor string) (View, error) {
	r, err := s.find(ctx, id)
	if err != nil {
		return View{}, err
	}
	if !r.Managed {
		return View{}, refuse("runner %s was not made by CRSP; crsp does not start it", r.Name)
	}
	if r.Kind != "persistent" {
		return View{}, refuse("runner %s is made for one job; a container starts for each job", r.Name)
	}
	st, ok := s.Agent.(ServiceStarter)
	if !ok || s.Agent == nil {
		return View{}, refuse("this machine cannot start the service of runner %s from here", r.Name)
	}
	ms, err := s.members(ctx, r)
	if err != nil {
		return View{}, err
	}
	did := 0
	var errs []string
	for _, k := range ms {
		if IsPoolRunner(k) || k.ServiceName == "" || isContainerRef(k.ServiceName) {
			continue
		}
		if err := st.StartService(ctx, k.MachineID, k.ServiceName); err != nil {
			errs = append(errs, k.Name+": "+err.Error())
			continue
		}
		did++
		s.audit(ctx, actor, "runner.started", k.MachineID, k.ID, k.Name+": started by request")
	}
	switch {
	case did == 0 && len(errs) > 0:
		return View{}, refuse("%s", strings.Join(errs, "; "))
	case did == 0:
		return View{}, refuse("runner %s has no service of its own to start; a container runner is kept by the pool", r.Name)
	case len(errs) > 0:
		return View{}, refuse("started %d, but %s", did, strings.Join(errs, "; "))
	}
	return s.GetView(ctx, id)
}
