package repos

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/internal/auth"
	"github.com/luis-mc/ci-runners-shared-pool/internal/github"
	"github.com/luis-mc/ci-runners-shared-pool/internal/runners"
)

// variableOrder is the fixed order of operating systems and their variable.
var variableOrder = []struct{ OS, Var string }{
	{"linux", "RUNS_ON_LINUX"},
	{"windows", "RUNS_ON_WINDOWS"},
	{"macos", "RUNS_ON_MACOS"},
}

// legacyVariableNames are the names older versions wrote (CRSP25). They are
// deleted on upgrade, only where the store knows the controller created them.
var legacyVariableNames = []string{"CRSP_RUNS_ON_LINUX", "CRSP_RUNS_ON_WINDOWS", "CRSP_RUNS_ON_MACOS"}

// legacyAlias maps a managed variable to the old dev-standards name written for
// it while a repository's legacy_vars setting is on (R5.39). NIGHTLY_RUNS_ON is
// never written; the owner sets it by hand.
var legacyAlias = []struct{ Var, Old string }{
	{"RUNS_ON_LINUX", "CI_RUNS_ON"},
	{"RUNS_ON_WINDOWS", "CI_RUNS_ON_WINDOWS"},
}

// LegacyAliasNames lists the old names legacy_vars writes.
func LegacyAliasNames() []string {
	out := make([]string, 0, len(legacyAlias))
	for _, a := range legacyAlias {
		out = append(out, a.Old)
	}
	return out
}

func isAlias(name string) bool {
	for _, a := range legacyAlias {
		if a.Old == name {
			return true
		}
	}
	return false
}

// keptNames is every variable the controller may write: the three managed ones,
// then the old aliases.
func keptNames() []string { return append(VariableNames(), LegacyAliasNames()...) }

// mayDelete says whether the controller may delete name from r. An old alias is
// deleted only when the store lists it as written by the controller: the old
// pool may have written CI_RUNS_ON, and that one is not ours to touch (R5.39).
func (s *Service) mayDelete(ctx context.Context, r Repository, name string) (bool, error) {
	if !isAlias(name) && !isSizeVar(name) {
		return true, nil
	}
	rows, err := s.cfg.Store.ListManagedVariables(ctx, r.ID)
	if err != nil {
		return false, err
	}
	for _, row := range rows {
		if row.Name == name {
			return true, nil
		}
	}
	return false, nil
}

// namesToCheck is keptNames plus every size variable that is wanted or that
// exists; a size variable that exists is deleted only if the store lists it as
// written by the controller (mayDelete).
func namesToCheck(want, cur map[string]string) []string {
	out := keptNames()
	var extra []string
	for _, m := range []map[string]string{want, cur} {
		for n := range m {
			if isSizeVar(n) {
				extra = append(extra, n)
			}
		}
	}
	sort.Strings(extra)
	for i, n := range extra {
		if i == 0 || extra[i-1] != n {
			out = append(out, n)
		}
	}
	return out
}

// VariableNames lists the three managed variable names.
func VariableNames() []string {
	out := make([]string, 0, len(variableOrder))
	for _, v := range variableOrder {
		out = append(out, v.Var)
	}
	return out
}

// labelList builds the JSON list for one OS: self-hosted, the OS, then the
// machines' labels, then the policy's labels, without repeats. Labels are
// case-insensitive, so every one is written lowercase (R5.28).
func labelList(os string, machine, policy []string) []string {
	os = strings.ToLower(os)
	out := []string{"self-hosted", os}
	seen := map[string]bool{"self-hosted": true, os: true}
	for _, set := range [][]string{machine, policy} {
		for _, l := range set {
			k := strings.ToLower(l)
			if seen[k] || osName(k) != "" {
				continue
			}
			seen[k] = true
			out = append(out, k)
		}
	}
	return out
}

// osName returns the lowercase OS for a lowercase label, "" for any other.
func osName(lower string) string {
	switch lower {
	case "linux", "windows", "macos":
		return lower
	}
	return ""
}

// sameLabels reports whether two JSON label lists hold the same labels in the
// same order, ignoring case (labels are case-insensitive). A value that is not
// a JSON list never matches.
func sameLabels(a, b string) bool {
	var x, y []string
	if json.Unmarshal([]byte(a), &x) != nil || json.Unmarshal([]byte(b), &y) != nil || len(x) != len(y) {
		return false
	}
	for i := range x {
		if !strings.EqualFold(x[i], y[i]) {
			return false
		}
	}
	return true
}

// Desired is the variables the controller keeps for a repository: one for each
// OS the policy enables and some runner can serve (R5.28). A policy whose
// labels name one or more operating systems enables only those; otherwise it
// enables every OS that a runner serves. An OS not in the result has no
// variable. Each value is a JSON list such as ["self-hosted","linux","crsp-home"].
func (s *Service) Desired(ctx context.Context, pol Policy) (map[string]string, error) {
	return s.desired(ctx, pol, nil, pol.RunnerKinds == RunnerKindsEphemeralOnly)
}

// desiredFor is Desired for one repository: an OS the repository is on
// hosted fallback for gets the hosted label instead of the crsp labels
// (R5.43). Only a repository that opted in is ever changed.
func (s *Service) desiredFor(ctx context.Context, r Repository, pol Policy) (map[string]string, error) {
	return s.desired(ctx, pol, s.hostedNow(r, pol), pol.EphemeralOnly(r.Visibility))
}

// desired builds the variables; hosted maps an OS to the hosted label that
// replaces its crsp labels.
func (s *Service) desired(ctx context.Context, pol Policy, hosted map[string]string, ephemeral bool) (map[string]string, error) {
	capy, err := s.osCapacity(ctx)
	if err != nil {
		return nil, err
	}
	enabled := map[string]bool{}
	for _, l := range pol.Labels {
		if n := osName(strings.ToLower(l)); n != "" {
			enabled[n] = true
		}
	}
	out := map[string]string{}
	base := map[string]bool{}
	for _, v := range variableOrder {
		labels, served := capy[v.OS]
		if !served || (len(enabled) > 0 && !enabled[v.OS]) {
			continue
		}
		base[v.OS] = true
		list := withEphemeral(labelList(v.OS, labels, pol.Labels), ephemeral && hosted[v.OS] == "")
		if h := hosted[v.OS]; h != "" {
			list = []string{h}
		}
		b, _ := json.Marshal(list)
		out[v.Var] = string(b)
	}
	// Size variables beside the plain ones (R5.67); the plain ones are unchanged.
	sized, err := s.sizeVariables(ctx, pol, base, hosted, ephemeral)
	if err != nil {
		return nil, err
	}
	for name, list := range sized {
		b, _ := json.Marshal(list)
		out[name] = string(b)
	}
	if pol.LegacyVars {
		for _, a := range legacyAlias {
			if val, ok := out[a.Var]; ok {
				out[a.Old] = val
			}
		}
	}
	return out, nil
}

// osCapacity is Capacity.OSLabels with the OS keys lowercased, so "Linux" and
// "linux" mean the same OS.
func (s *Service) osCapacity(ctx context.Context) (map[string][]string, error) {
	capy, err := s.cfg.Capacity.OSLabels(ctx)
	if err != nil {
		return nil, err
	}
	out := make(map[string][]string, len(capy))
	for k, v := range capy {
		out[strings.ToLower(k)] = v
	}
	return out, nil
}

// syncVariables makes the repository's variables what Desired says (R5.28):
// it writes the ones wanted and deletes the ones not wanted, which are the
// ones for an OS the repository has no runner for or the policy turns off.
// Old CRSP_RUNS_ON_* variables are deleted first.
func (s *Service) syncVariables(ctx context.Context, actor auth.Principal, r Repository, pol Policy) error {
	want, err := s.desiredFor(ctx, r, pol)
	if err != nil {
		return err
	}
	if err := s.migrateLegacy(ctx, actor, r); err != nil {
		return err
	}
	have, err := s.cfg.GitHub.ListVariables(ctx, r.InstallationID, r.Owner, r.Name)
	if err != nil {
		return err
	}
	cur := map[string]string{}
	for _, v := range have {
		cur[v.Name] = v.Value
	}
	for _, name := range namesToCheck(want, cur) {
		val, ok := want[name]
		got, exists := cur[name]
		if ok {
			s.clearAbsent(r, name)
		}
		if !ok {
			if exists {
				if yes, err := s.mayDelete(ctx, r, name); err != nil {
					return err
				} else if !yes {
					continue
				}
				yes, why := s.confirmDelete(ctx, r, pol, name)
				if !yes {
					continue
				}
				if err := s.deleteVariableWhy(ctx, actor, r, name, why); err != nil {
					return err
				}
			}
			continue
		}
		if exists && sameLabels(got, val) {
			if err := s.recordVariable(ctx, r.ID, name, got, VarOK, false); err != nil {
				return err
			}
			continue
		}
		if err := s.cfg.GitHub.SetVariable(ctx, r.InstallationID, r.Owner, r.Name, github.Variable{Name: name, Value: val}); err != nil {
			return err
		}
		if err := s.recordVariable(ctx, r.ID, name, val, VarOK, true); err != nil {
			return err
		}
		s.cfg.Audit.Audit(ctx, actor, "variable.written", "repository", r.FullName, name+" = "+val)
	}
	return nil
}

const (
	// CapacitySettle is how long after a start the controller waits for the
	// machines to report their facts before it trusts an absent OS: two report
	// intervals (agents detect again every 10 minutes).
	CapacitySettle = 20 * time.Minute
	// DeleteHold is how long the capacity of an OS must stay absent, over two
	// syncs at least this far apart, before its RUNS_ON variable is deleted.
	DeleteHold = 15 * time.Minute
)

// varOS is the OS a managed or alias variable or size variable belongs to, "" for any other.
func varOS(name string) string {
	for _, v := range variableOrder {
		if v.Var == name {
			return v.OS
		}
	}
	for _, a := range legacyAlias {
		if a.Old == name {
			return varOS(a.Var)
		}
	}
	if os, _, ok := ParseSizeVariable(name); ok {
		return os
	}
	return ""
}

// capacityKnown says whether an absent OS can be believed: every machine that
// was online has reported facts since this start (FactsReported), or the settle
// period has passed.
func (s *Service) capacityKnown(ctx context.Context) bool {
	if s.cfg.Now().Sub(s.started) >= CapacitySettle {
		return true
	}
	if s.cfg.FactsReported == nil {
		return false
	}
	ok, err := s.cfg.FactsReported(ctx)
	return err == nil && ok
}

// confirmDelete says whether a variable that wants to go may be deleted now,
// and why (R5.28). Capacity absence deletes only when capacity is known and
// has been absent in two syncs at least DeleteHold apart; one missing reading
// never deletes. A policy that turns the OS off deletes at once. reason fills
// the audit entry.
func (s *Service) confirmDelete(ctx context.Context, r Repository, pol Policy, name string) (yes bool, reason string) {
	os := varOS(name)
	if os == "" {
		return true, ""
	}
	if len(osLabelsEnabled(pol)) > 0 && !osLabelsEnabled(pol)[os] {
		return true, ""
	}
	capy, err := s.osCapacity(ctx)
	key := r.FullName + "/" + name
	s.mu.Lock()
	defer s.mu.Unlock()
	if err != nil {
		return false, ""
	}
	if _, served := capy[os]; served {
		delete(s.absent, key)
		return true, ""
	}
	if !s.capacityKnown(ctx) {
		delete(s.absent, key)
		return false, ""
	}
	now := s.cfg.Now()
	first, seen := s.absent[key]
	if !seen {
		s.absent[key] = now
		return false, ""
	}
	if now.Sub(first) < DeleteHold {
		return false, ""
	}
	return true, "no " + strings.ToUpper(os[:1]) + os[1:] + " runner for " + strconv.Itoa(int(now.Sub(first).Minutes())) + " min"
}

// clearAbsent forgets that the capacity for name was seen absent.
func (s *Service) clearAbsent(r Repository, name string) {
	s.mu.Lock()
	delete(s.absent, r.FullName+"/"+name)
	s.mu.Unlock()
}

func osLabelsEnabled(pol Policy) map[string]bool {
	enabled := map[string]bool{}
	for _, l := range pol.Labels {
		if n := osName(strings.ToLower(l)); n != "" {
			enabled[n] = true
		}
	}
	return enabled
}

// deleteVariable removes one variable from GitHub and from the store.
func (s *Service) deleteVariable(ctx context.Context, actor auth.Principal, r Repository, name string) error {
	return s.deleteVariableWhy(ctx, actor, r, name, "")
}

// deleteVariableWhy is deleteVariable with the reason in the audit entry.
func (s *Service) deleteVariableWhy(ctx context.Context, actor auth.Principal, r Repository, name, why string) error {
	if err := s.cfg.GitHub.DeleteVariable(ctx, r.InstallationID, r.Owner, r.Name, name); err != nil {
		return err
	}
	if err := s.cfg.Store.DeleteManagedVariable(ctx, r.ID, name); err != nil {
		return err
	}
	s.cfg.Audit.Audit(ctx, actor, "variable.deleted", "repository", r.FullName, strings.TrimSpace(name+" "+why))
	return nil
}

// migrateLegacy deletes the CRSP_RUNS_ON_* variables an older version wrote.
// Only a name the store lists as written by the controller is deleted.
func (s *Service) migrateLegacy(ctx context.Context, actor auth.Principal, r Repository) error {
	rows, err := s.cfg.Store.ListManagedVariables(ctx, r.ID)
	if err != nil {
		return err
	}
	for _, row := range rows {
		if isLegacyVariable(row.Name) {
			if err := s.deleteVariable(ctx, actor, r, row.Name); err != nil {
				return err
			}
		}
	}
	return nil
}

func isLegacyVariable(name string) bool {
	for _, n := range legacyVariableNames {
		if n == name {
			return true
		}
	}
	return false
}

func (s *Service) recordVariable(ctx context.Context, repoID int64, name, value, status string, written bool) error {
	now := s.cfg.Now()
	if written {
		return s.cfg.Store.UpsertManagedVariable(ctx, ManagedVariable{RepositoryID: repoID, Name: name, Value: value, Status: status, WrittenAt: now, CheckedAt: now})
	}
	vars, err := s.cfg.Store.ListManagedVariables(ctx, repoID)
	if err != nil {
		return err
	}
	for _, v := range vars {
		if v.Name == name {
			return s.cfg.Store.SetManagedVariableStatus(ctx, repoID, name, status, now)
		}
	}
	return s.cfg.Store.UpsertManagedVariable(ctx, ManagedVariable{RepositoryID: repoID, Name: name, Value: value, Status: status, WrittenAt: now, CheckedAt: now})
}

// ReconcileVariables checks one enabled repository's variables against what
// they should be, as background work within the rate budget (R5.28, R6.6). A
// value changed outside the product is restored, written to the audit log, and
// shown as drift until the next check finds it correct. A missing variable is
// written back the same way. A variable that should not exist (no runner for
// its OS, or the pool off for it) is deleted. Old CRSP_RUNS_ON_* variables are
// deleted whatever the pool state.
func (s *Service) ReconcileVariables(ctx context.Context, repoID int64) error {
	r, err := s.cfg.Store.GetRepository(ctx, repoID)
	if err != nil {
		return err
	}
	if err := s.migrateLegacy(github.Background(ctx), System, r); err != nil {
		return err
	}
	if !r.PoolOn {
		return nil
	}
	pol, err := s.policyOrDefault(ctx, repoID)
	if err != nil {
		return err
	}
	want, err := s.desiredFor(ctx, r, pol)
	if err != nil {
		return err
	}
	bg := github.Background(ctx)
	have, err := s.cfg.GitHub.ListVariables(bg, r.InstallationID, r.Owner, r.Name)
	if err != nil {
		return err
	}
	cur := map[string]string{}
	for _, v := range have {
		cur[v.Name] = v.Value
	}
	now := s.cfg.Now()
	for _, name := range namesToCheck(want, cur) {
		val, ok := want[name]
		got, exists := cur[name]
		if ok {
			s.clearAbsent(r, name)
		}
		if !ok {
			// No runner for this OS, or the pool is off for it: no variable.
			if exists {
				if yes, err := s.mayDelete(bg, r, name); err != nil {
					return err
				} else if !yes {
					continue
				}
				yes, why := s.confirmDelete(bg, r, pol, name)
				if !yes {
					continue
				}
				if err := s.deleteVariableWhy(bg, System, r, name, why); err != nil {
					return err
				}
			}
			continue
		}
		if exists && sameLabels(got, val) {
			if err := s.recordVariable(ctx, repoID, name, val, VarOK, false); err != nil {
				return err
			}
			continue
		}
		status := VarWrong
		if !exists {
			status = VarMissing
		}
		// Record the drift first, so the row shows it even if the write fails.
		if err := s.cfg.Store.UpsertManagedVariable(ctx, ManagedVariable{RepositoryID: repoID, Name: name, Value: val, Status: status, WrittenAt: now, CheckedAt: now}); err != nil {
			return err
		}
		if err := s.cfg.GitHub.SetVariable(bg, r.InstallationID, r.Owner, r.Name, github.Variable{Name: name, Value: val}); err != nil {
			return err
		}
		s.cfg.Audit.Audit(ctx, System, "variable.restored", "repository", r.FullName,
			name+" was "+driftWas(exists, got)+"; restored to "+val)
		if err := s.cfg.Store.SetManagedVariableStatus(ctx, repoID, name, status, now); err != nil {
			return err
		}
	}
	// A variable an older version wrote for light jobs must not point at the
	// removed light runner label (R5.65).
	return s.migrateLightVariable(bg, System, r, want, have)
}

func driftWas(exists bool, got string) string {
	if !exists {
		return "missing"
	}
	return got
}

// RunnersChanged is called when a runner is added or removed: it brings every
// repository's variables in line at once, so an OS that gained its first
// runner gets its variable and one that lost its last runner loses it (R5.28).
func (s *Service) RunnersChanged(ctx context.Context) error {
	return s.ReconcileAllVariables(ctx)
}

// ReconcileAllVariables checks every known repository. On a rate limit it
// stops and returns the error, which carries the reset time (R6.6).
func (s *Service) ReconcileAllVariables(ctx context.Context) error {
	rs, err := s.cfg.Store.ListRepositories(ctx, ListFilter{})
	if err != nil {
		return err
	}
	var errs []error
	for _, r := range rs {
		if err := s.ReconcileVariables(ctx, r.ID); err != nil {
			if errors.Is(err, github.ErrRateLimited) {
				return err
			}
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// withEphemeral adds the ephemeral label to a RUNS_ON list when the
// repository's jobs go to ephemeral runners only. GitHub offers a job only to a
// runner that has every label of its runs-on, and no persistent runner carries
// the label, so a persistent runner can never take the job (R5.60).
func withEphemeral(list []string, on bool) []string {
	if !on {
		return list
	}
	return append(list, runners.EphemeralLabel)
}
