package runners

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/internal/backend"
	"github.com/luis-mc/ci-runners-shared-pool/internal/github"
)

// SessionWaitReason is the words shown on a runner while GitHub holds its old
// session. It is a wait, never an error (runners R5.95).
const SessionWaitReason = "Waiting for GitHub to release the old session"

// SessionWaitText is SessionWaitReason with the end of the wait window.
func SessionWaitText(until time.Time) string {
	if until.IsZero() {
		return SessionWaitReason
	}
	return SessionWaitReason + ", until " + until.UTC().Format("15:04:05Z")
}

// Facts is what GitHub and the machine's agent say about one runner right now.
// A fact that could not be read is not Known; it never counts as "fine".
type Facts struct {
	GitHubKnown  bool
	GitHub       *github.Runner // nil: GitHub does not list the runner
	ServiceKnown bool
	Service      *backend.Found // nil: the agent does not see the service
}

// Truth is the state a runner shows and the reason for it (web-console UX-19).
// A runner is idle only when GitHub reports it online and not busy and its
// service runs; anything else shows what is real.
func Truth(r Record, f Facts) (State, string) {
	switch r.State {
	case Creating, Draining, Updating, Removing:
		if r.Reason != "" {
			return r.State, r.Reason
		}
		return r.State, map[State]string{Creating: "it is being set up", Draining: "it ends its job, then it is removed",
			Updating: "its files are being replaced", Removing: "it is being removed"}[r.State]
	case Failed:
		if r.Reason == "" {
			return Failed, "the runner did not start"
		}
		return Failed, r.Reason
	}
	if f.ServiceKnown {
		switch {
		case f.Service == nil:
			return Stopped, "its service is not on the machine"
		case !f.Service.Running:
			return Stopped, "its service is stopped"
		}
	}
	switch {
	case f.Service != nil && f.Service.SessionRefused && !f.Service.SessionWaitUntil.IsZero():
		// A wait, never an error: GitHub still holds the old session after a
		// restart and drops it on its own (runners R5.95).
		return Offline, SessionWaitText(f.Service.SessionWaitUntil)
	case f.Service != nil && f.Service.SessionRefused:
		return Offline, "GitHub still holds the old session of this runner after the wait window; look for a second listener or another machine that uses this runner name"
	case !f.GitHubKnown:
		return Unknown, "GitHub could not be read, so the state is not known"
	case f.GitHub == nil:
		return NotRegistered, "GitHub does not list a runner with this name"
	case f.GitHub.Status != "online":
		return Offline, "GitHub reports it offline"
	case f.GitHub.Busy:
		return Busy, ""
	}
	return Idle, ""
}

// View is a runner record with its true state and its machine's name.
type View struct {
	Record
	// State is what to show; Record.State is what was stored.
	State       State
	Reason      string
	MachineName string
	// Serving is the repository of the job a pool runner runs now, and
	// Registrations is how many hidden registrations it holds (runners R5.88).
	// Both are empty on a runner that is not a pool runner.
	Serving       string
	Registrations int
}

// ListView lists runners with the state each one really has, from GitHub's
// runner list and the agent's service list (UX-19).
func (s *Service) ListView(ctx context.Context, f Filter) ([]View, error) {
	recs, err := s.Store.ListRunners(ctx, f)
	if err != nil {
		return nil, err
	}
	return s.withPool(ctx, recs, true), nil
}

// GetView is ListView for one runner.
func (s *Service) GetView(ctx context.Context, id string) (View, error) {
	r, err := s.find(ctx, id)
	if err != nil {
		return View{}, err
	}
	return s.withPool(ctx, []Record{r}, false)[0], nil
}

// withPool shows the hidden registrations of a pool runner as that runner: a
// list does not show them by themselves, a lookup by id does.
func (s *Service) withPool(ctx context.Context, recs []Record, hideKids bool) []View {
	var plain, parents []Record
	for _, r := range recs {
		switch {
		case IsPoolRunner(r):
			parents = append(parents, r)
		case IsRegistration(r) && hideKids:
		default:
			plain = append(plain, r)
		}
	}
	out := s.views(ctx, plain)
	if len(parents) == 0 {
		return out
	}
	all, err := s.Store.ListRunners(ctx, Filter{})
	if err != nil {
		all = recs
	}
	kids := map[string][]Record{}
	for _, r := range all {
		if r.PoolRunnerID != "" {
			kids[r.PoolRunnerID] = append(kids[r.PoolRunnerID], r)
		}
	}
	names := map[int64]string{}
	if s.PoolRepos != nil {
		if rs, err := s.PoolRepos(ctx); err == nil {
			for _, pr := range rs {
				names[pr.ID] = pr.FullName
			}
		}
	}
	for _, p := range parents {
		machine := p.MachineID
		if s.Machines != nil {
			if m, err := s.Machines.GetMachine(ctx, p.MachineID); err == nil && m.Name != "" {
				machine = m.Name
			}
		}
		out = append(out, poolView(p, s.views(ctx, kids[p.ID]), names, machine))
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

type ghList struct {
	ok   bool
	list []github.Runner
}

func (s *Service) views(ctx context.Context, recs []Record) []View {
	gh := map[string]ghList{}
	svc := map[string]map[string]backend.Found{}
	names := map[string]string{}
	out := make([]View, 0, len(recs))
	for _, r := range recs {
		if _, ok := names[r.MachineID]; !ok {
			names[r.MachineID] = r.MachineID
			if s.Machines != nil {
				if m, err := s.Machines.GetMachine(ctx, r.MachineID); err == nil && m.Name != "" {
					names[r.MachineID] = m.Name
				}
			}
		}
		var f Facts
		if r.State == Idle || r.State == Busy || r.State == Offline {
			key := fmt.Sprintf("%s|%d|%s|%s", r.Scope.Kind, r.Scope.RepositoryID, r.Scope.Organization, r.Scope.Group)
			l, seen := gh[key]
			if !seen {
				l = s.githubList(ctx, r.Scope)
				gh[key] = l
			}
			f.GitHubKnown = l.ok
			for i := range l.list {
				if l.list[i].Name == r.Name || (r.GitHubRunnerID != 0 && l.list[i].ID == r.GitHubRunnerID) {
					g := l.list[i]
					f.GitHub = &g
					break
				}
			}
			if r.Kind == "persistent" && s.Services != nil {
				m, seen := svc[r.MachineID]
				if !seen {
					if found, err := s.Services(ctx, r.MachineID); err == nil {
						m = map[string]backend.Found{}
						for _, x := range found {
							m[x.ServiceName] = x
						}
						// A container runner is also found by name: the folder may
						// carry a -r<repo id> suffix the record's ref does not (OPS70).
						for _, x := range found {
							if _, dup := m[nameKey(x.Name)]; !dup && x.Name != "" {
								m[nameKey(x.Name)] = x
							}
						}
					}
					svc[r.MachineID] = m
				}
				if m != nil {
					f.ServiceKnown = true
					if x, ok := m[r.ServiceName]; ok && r.ServiceName != "" {
						f.Service = &x
					} else if x, ok := m[nameKey(r.Name)]; ok && isContainerRef(r.ServiceName) {
						f.Service = &x
					}
				}
			}
		}
		state, why := Truth(r, f)
		if r.UncleanReason != "" && state != Failed {
			// An unclean runner takes no new job until its clean-up works (R5.91).
			why = "needs clean-up, takes no new job: " + r.UncleanReason
		}
		out = append(out, View{Record: r, State: state, Reason: why, MachineName: names[r.MachineID]})
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

func (s *Service) githubList(ctx context.Context, sc Scope) ghList {
	if s.Resolver == nil || s.GitHub == nil {
		return ghList{}
	}
	t, err := s.Resolver.Resolve(ctx, sc)
	if err != nil {
		return ghList{}
	}
	list, err := s.GitHub.ListRunners(ctx, t.InstallationID, t.Scope)
	if err != nil {
		return ghList{}
	}
	return ghList{ok: true, list: list}
}

// nameKey is the key of a runner's name in the service map; it never collides
// with a service name or a container ref.
func nameKey(name string) string { return "name\x00" + name }
