package repos

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/internal/github"
)

// Hosted fallback (github-integration R5.42 to R5.46). It is off by default: a
// job waits for a crsp machine, which costs nothing. A repository that opted
// in runs on GitHub-hosted runners while no crsp machine can take its jobs,
// knowing GitHub may bill those minutes for a private repository.

// DefaultFallbackGrace is how long no machine of an OS must have been up
// before an opted-in repository falls back.
const DefaultFallbackGrace = 3 * time.Minute

// DefaultHostedLabels are the hosted labels used when a policy sets none.
var DefaultHostedLabels = map[string]string{"linux": "ubuntu-latest", "windows": "windows-latest", "macos": "macos-latest"}

// Where the jobs of a repository go now.
const (
	WhereCrsp   = "crsp"
	WhereHosted = "hosted"
	WhereWaits  = "waiting"
)

// Why codes for Routing.Why; the console turns them into words.
const (
	WhyMachineUp   = "machine_up"        // a crsp machine is up
	WhyFallbackOn  = "fallback_on"       // no machine up past the grace; on hosted
	WhyFallbackOff = "fallback_off"      // no machine up; fallback is off, jobs wait
	WhyGrace       = "grace"             // no machine up yet, within the grace time
	WhyCap         = "cap_reached"       // no machine up; the monthly cap is used
	WhyPublic      = "public_repository" // no machine up; a public repository never falls back
)

// Routing is where a repository's jobs go now, and why (R5.44).
type Routing struct {
	Where string
	Why   string
	// OSes are the operating systems the reason is about ("linux", ...).
	OSes []string
	// UsedMinutes are the fallback minutes counted this month.
	UsedMinutes int
}

type fallbackState struct {
	mu        sync.Mutex
	downSince map[string]time.Time      // os -> first time seen with no machine up
	up        map[string]bool           // os -> up at the last evaluation; absent = no machine
	on        map[int64]map[string]bool // repository -> os -> on hosted
	seen      map[int64]map[int64]time.Time
	evaluated bool
}

func newFallbackState() fallbackState {
	return fallbackState{downSince: map[string]time.Time{}, up: map[string]bool{}, on: map[int64]map[string]bool{},
		seen: map[int64]map[int64]time.Time{}}
}

func (s *Service) grace() time.Duration {
	if s.cfg.FallbackGrace != nil {
		if g := s.cfg.FallbackGrace(); g > 0 {
			return g
		}
	}
	return DefaultFallbackGrace
}

// hostedLabel is the hosted label for an OS under a policy.
func hostedLabel(pol Policy, os string) string {
	if l := strings.TrimSpace(pol.HostedLabels[os]); l != "" {
		return l
	}
	return DefaultHostedLabels[os]
}

// canFallBack says whether a repository may ever fall back: crsp serves
// private repositories only, so a public one never does.
func canFallBack(r Repository, pol Policy) bool {
	return pol.HostedFallback && r.Visibility != "public"
}

// hostedNow is the OS to hosted label map of a repository that is on
// fallback now; nil for one that opted out or never fell back.
func (s *Service) hostedNow(r Repository, pol Policy) map[string]string {
	if !canFallBack(r, pol) {
		return nil
	}
	s.fb.mu.Lock()
	defer s.fb.mu.Unlock()
	var out map[string]string
	for os := range s.fb.on[r.ID] {
		if out == nil {
			out = map[string]string{}
		}
		out[os] = hostedLabel(pol, os)
	}
	return out
}

func monthKey(t time.Time) string { return t.UTC().Format("2006-01") }

// FallbackUsedMinutes is the fallback minutes counted for a repository in the
// month of at, rounded up.
func (s *Service) FallbackUsedMinutes(ctx context.Context, repoID int64, at time.Time) (int, error) {
	sec, err := s.cfg.Store.FallbackSeconds(ctx, repoID, monthKey(at))
	return int((sec + 59) / 60), err
}

func (s *Service) capReached(ctx context.Context, r Repository, pol Policy) (bool, error) {
	if pol.FallbackCapMinutes <= 0 {
		return false, nil
	}
	sec, err := s.cfg.Store.FallbackSeconds(ctx, r.ID, monthKey(s.cfg.Now()))
	return sec >= int64(pol.FallbackCapMinutes)*60, err
}

// fallbackOSes lists the OSes a repository's variables cover (a variable is
// written for each), in the fixed order.
func (s *Service) fallbackOSes(ctx context.Context, pol Policy) ([]string, error) {
	want, err := s.desired(ctx, pol, nil, false)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, v := range variableOrder {
		if _, ok := want[v.Var]; ok {
			out = append(out, v.OS)
		}
	}
	return out, nil
}

// observeUp reads which OSes have a machine up and keeps the time each OS went
// without one.
func (s *Service) observeUp(ctx context.Context) error {
	raw, err := s.cfg.Up.UpOS(ctx)
	if err != nil {
		return err
	}
	now := s.cfg.Now()
	s.fb.mu.Lock()
	defer s.fb.mu.Unlock()
	s.fb.up = map[string]bool{}
	for k, v := range raw {
		s.fb.up[strings.ToLower(k)] = v
	}
	for _, v := range variableOrder {
		if up, served := s.fb.up[v.OS]; !served || up {
			delete(s.fb.downSince, v.OS)
		} else if _, ok := s.fb.downSince[v.OS]; !ok {
			s.fb.downSince[v.OS] = now
		}
	}
	s.fb.evaluated = true
	return nil
}

// osDown reports whether an OS has a machine but none is up, and for how
// long it has been so.
func (s *Service) osDown(os string, now time.Time) (down bool, for_ time.Duration) {
	s.fb.mu.Lock()
	defer s.fb.mu.Unlock()
	since, ok := s.fb.downSince[os]
	if !ok {
		return false, 0
	}
	return true, now.Sub(since)
}

// EvaluateFallback is the controller's fallback pass. It reads which OSes have
// a machine up, and for each repository that opted in it rewrites CRSP_RUNS_ON_<OS>
// to the hosted label once no machine of the OS has been up for the grace
// time, and back as soon as one is up (R5.43). It counts the fallback minutes
// and stops falling back at the cap (R5.45). A repository that did not opt in
// is never read or written here. Every switch goes to the audit log with its
// reason.
func (s *Service) EvaluateFallback(ctx context.Context) error {
	if s.cfg.Up == nil {
		return nil
	}
	if err := s.observeUp(ctx); err != nil {
		return err
	}
	rs, err := s.cfg.Store.ListRepositories(ctx, ListFilter{})
	if err != nil {
		return err
	}
	var errs []error
	for _, r := range rs {
		if !r.PoolOn {
			s.dropFallback(r.ID)
			continue
		}
		pol, err := s.policyOrDefault(ctx, r.ID)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		if !canFallBack(r, pol) {
			if s.dropFallback(r.ID) {
				// The repository opted out, or is public: put the crsp labels back.
				errs = append(errs, s.restoreAfterFallback(ctx, r, pol, "fallback is off for this repository"))
			}
			continue
		}
		if err := s.evaluateRepo(ctx, r, pol); err != nil {
			if isRate(err) {
				return err
			}
			errs = append(errs, err)
		}
	}
	return joinErrs(errs)
}

func isRate(err error) bool { return errors.Is(err, github.ErrRateLimited) }

func joinErrs(errs []error) error {
	var out []string
	for _, e := range errs {
		if e != nil {
			out = append(out, e.Error())
		}
	}
	if len(out) == 0 {
		return nil
	}
	return fmt.Errorf("%s", strings.Join(out, "; "))
}

// dropFallback forgets a repository's fallback state; it reports whether the
// repository was on fallback.
func (s *Service) dropFallback(id int64) bool {
	s.fb.mu.Lock()
	defer s.fb.mu.Unlock()
	was := len(s.fb.on[id]) > 0
	delete(s.fb.on, id)
	return was
}

func (s *Service) restoreAfterFallback(ctx context.Context, r Repository, pol Policy, reason string) error {
	s.cfg.Audit.Audit(ctx, System, "fallback.ended", "repository", r.FullName, reason)
	return s.syncVariables(github.Background(ctx), System, r, pol)
}

func (s *Service) evaluateRepo(ctx context.Context, r Repository, pol Policy) error {
	oses, err := s.fallbackOSes(ctx, pol)
	if err != nil {
		return err
	}
	if err := s.countMinutes(ctx, r, pol); err != nil {
		return err
	}
	capped, err := s.capReached(ctx, r, pol)
	if err != nil {
		return err
	}
	now := s.cfg.Now()
	grace := s.grace()
	changed := false
	for _, os := range oses {
		down, long := s.osDown(os, now)
		s.fb.mu.Lock()
		on := s.fb.on[r.ID][os]
		s.fb.mu.Unlock()
		var want bool
		var reason string
		switch {
		case !down:
			reason = "a crsp machine for " + os + " is up again"
		case capped:
			reason = "the monthly cap of " + fmt.Sprint(pol.FallbackCapMinutes) + " minutes is reached"
		case long >= grace:
			want = true
			reason = "no crsp machine for " + os + " has been up for " + long.Round(time.Second).String() + " (offline, asleep or paused)"
		}
		if want == on {
			continue
		}
		s.fb.mu.Lock()
		if want {
			if s.fb.on[r.ID] == nil {
				s.fb.on[r.ID] = map[string]bool{}
			}
			s.fb.on[r.ID][os] = true
		} else {
			delete(s.fb.on[r.ID], os)
			if len(s.fb.on[r.ID]) == 0 {
				delete(s.fb.on, r.ID)
			}
		}
		s.fb.mu.Unlock()
		verb, action := "switched back to crsp machines", "fallback.ended"
		if want {
			verb, action = "switched to "+hostedLabel(pol, os), "fallback.started"
		}
		s.cfg.Audit.Audit(ctx, System, action, "repository", r.FullName, os+": "+verb+": "+reason)
		changed = true
	}
	if !changed {
		return nil
	}
	return s.syncVariables(github.Background(ctx), System, r, pol)
}

// countMinutes adds the time hosted jobs ran since the last pass to the month's
// count, best effort from the open jobs the poller reads (R5.45): a job in
// progress whose labels carry a hosted label counts from when it started, or
// from when it was first seen. It reads jobs only while the repository is on
// fallback or still has a hosted job it counts.
func (s *Service) countMinutes(ctx context.Context, r Repository, pol Policy) error {
	s.fb.mu.Lock()
	active := len(s.fb.on[r.ID]) > 0 || len(s.fb.seen[r.ID]) > 0
	s.fb.mu.Unlock()
	if !active {
		return nil
	}
	jobs, err := s.cfg.GitHub.ListOpenJobs(github.Background(ctx), r.InstallationID, r.Owner, r.Name)
	if err != nil {
		return err
	}
	hosted := map[string]bool{}
	for _, os := range []string{"linux", "windows", "macos"} {
		hosted[strings.ToLower(hostedLabel(pol, os))] = true
	}
	now := s.cfg.Now()
	var add int64
	s.fb.mu.Lock()
	prev := s.fb.seen[r.ID]
	next := map[int64]time.Time{}
	for _, j := range jobs {
		if j.Status != "in_progress" || !hasHostedLabel(j.Labels, hosted) {
			continue
		}
		last, known := prev[j.ID]
		if !known {
			last = now
			if !j.StartedAt.IsZero() && j.StartedAt.Before(now) {
				last = j.StartedAt
			}
		}
		if d := now.Sub(last); d > 0 {
			add += int64(d.Seconds())
		}
		next[j.ID] = now
	}
	if len(next) == 0 {
		delete(s.fb.seen, r.ID)
	} else {
		s.fb.seen[r.ID] = next
	}
	s.fb.mu.Unlock()
	if add == 0 {
		return nil
	}
	return s.cfg.Store.AddFallbackSeconds(ctx, r.ID, monthKey(now), add)
}

func hasHostedLabel(labels []string, hosted map[string]bool) bool {
	for _, l := range labels {
		if hosted[strings.ToLower(l)] {
			return true
		}
	}
	return false
}

// routing says where the jobs of a repository go now and why (R5.44). The pool
// must be on; a repository with the pool off has no routing.
func (s *Service) routing(ctx context.Context, r Repository, pol Policy) (Routing, error) {
	if !r.PoolOn {
		return Routing{}, nil
	}
	if s.cfg.Up != nil {
		s.fb.mu.Lock()
		seen := s.fb.evaluated
		s.fb.mu.Unlock()
		if !seen {
			if err := s.observeUp(ctx); err != nil {
				return Routing{}, err
			}
		}
	}
	oses, err := s.fallbackOSes(ctx, pol)
	if err != nil {
		return Routing{}, err
	}
	used, _ := s.FallbackUsedMinutes(ctx, r.ID, s.cfg.Now())
	out := Routing{Where: WhereCrsp, Why: WhyMachineUp, UsedMinutes: used}
	if s.cfg.Up == nil {
		return out, nil
	}
	now := s.cfg.Now()
	var hostedOS, waitOS []string
	s.fb.mu.Lock()
	for _, os := range oses {
		if s.fb.on[r.ID][os] && canFallBack(r, pol) {
			hostedOS = append(hostedOS, os)
		}
	}
	s.fb.mu.Unlock()
	for _, os := range oses {
		if down, _ := s.osDown(os, now); down && !inList(hostedOS, os) {
			waitOS = append(waitOS, os)
		}
	}
	sort.Strings(hostedOS)
	if len(hostedOS) > 0 {
		out.Where, out.Why, out.OSes = WhereHosted, WhyFallbackOn, hostedOS
		return out, nil
	}
	if len(waitOS) == 0 {
		return out, nil
	}
	out.Where, out.OSes = WhereWaits, waitOS
	switch capped, _ := s.capReached(ctx, r, pol); {
	case r.Visibility == "public" && pol.HostedFallback:
		out.Why = WhyPublic
	case !pol.HostedFallback:
		out.Why = WhyFallbackOff
	case capped:
		out.Why = WhyCap
	default:
		out.Why = WhyGrace
	}
	return out, nil
}

func inList(s []string, v string) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}

// RoutingOf is the routing of one repository (R5.44).
func (s *Service) RoutingOf(ctx context.Context, id int64) (Routing, error) {
	r, err := s.cfg.Store.GetRepository(ctx, id)
	if err != nil {
		return Routing{}, err
	}
	pol, err := s.policyOrDefault(ctx, id)
	if err != nil {
		return Routing{}, err
	}
	return s.routing(ctx, r, pol)
}

// DesiredNow is the variables the controller keeps for a repository right
// now, with the hosted label for an OS on fallback (R5.43). The doctor reads
// it so that a repository on fallback is not reported as drifted.
func (s *Service) DesiredNow(ctx context.Context, r Repository) (map[string]string, error) {
	pol, err := s.policyOrDefault(ctx, r.ID)
	if err != nil {
		return nil, err
	}
	return s.desiredFor(ctx, r, pol)
}
