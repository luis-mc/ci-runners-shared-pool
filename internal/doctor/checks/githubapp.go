package checks

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/luis-mc/ci-runners-shared-pool/internal/doctor"
	"github.com/luis-mc/ci-runners-shared-pool/internal/github"
)

// RequiredPermissions is what the App needs (github-integration R5.23), keyed
// by GitHub's permission name. Organization permissions apply only on an
// organization account.
var RequiredPermissions = map[string]string{
	"administration":    "write",
	"actions":           "read",
	"actions_variables": "write",
	"contents":          "read",
	"metadata":          "read",
}

// OrgPermissions are needed in addition on organization installations.
var OrgPermissions = map[string]string{"organization_self_hosted_runners": "write"}

func level(v string) int {
	switch v {
	case "read":
		return 1
	case "write":
		return 2
	case "admin":
		return 3
	}
	return 0
}

type installInfo struct {
	id    int64
	owner string
}

// installations lists the installations of the pool repositories in scope.
func installations(ctx context.Context, env doctor.Env, check string) ([]installInfo, []doctor.Finding) {
	repos, err := env.Fleet.Repositories(ctx)
	if err != nil {
		return nil, []doctor.Finding{skipFor(check, controllerSubject, "The repository list", err)}
	}
	seen := map[int64]string{}
	for _, r := range repos {
		if env.InScopeRepository(r) {
			seen[r.InstallationID] = r.Owner
		}
	}
	var out []installInfo
	for id, o := range seen {
		out = append(out, installInfo{id, o})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].id < out[j].id })
	return out, nil
}

func installation(ctx context.Context, env doctor.Env, id int64) (github.Installation, error) {
	v, err := env.Memo(fmt.Sprintf("installation:%d", id), func() (any, error) { return env.GitHub.GetInstallation(ctx, id) })
	if err != nil {
		return github.Installation{}, err
	}
	return v.(github.Installation), nil
}

func installSubject(i installInfo) doctor.Subject {
	return doctor.Subject{Kind: "installation", ID: fmt.Sprint(i.id), Name: i.owner}
}

const appLink = "/settings/github"

// appInstallation: the App is not installed, or is suspended, on an account
// with a pool repository. New check (R5.16).
func appInstallation(ctx context.Context, env doctor.Env) []doctor.Finding {
	insts, out := installations(ctx, env, "github.app.installation")
	if out != nil || len(insts) == 0 {
		return out
	}
	if env.GitHub == nil {
		return []doctor.Finding{skip("github.app.installation", controllerSubject, "GitHub is not connected", "The GitHub App is not set up yet.")}
	}
	for _, i := range insts {
		s := installSubject(i)
		inst, err := installation(ctx, env, i.id)
		f := doctor.Finding{Check: "github.app.installation", Subject: s}
		switch {
		case errors.Is(err, github.ErrNotFound):
			f.Severity, f.Title = doctor.Fail, "The App is not installed on "+i.owner
			f.Detail = "GitHub has no installation " + s.ID + ". It was removed, or the repositories are no longer shared with the App."
			f.Fix = doctor.Fix{Text: "Install the App on " + i.owner + " again and select the pool repositories.", Link: appLink}
		case err != nil:
			out = append(out, skipFor("github.app.installation", s, "The installation on "+i.owner, err))
			continue
		case inst.Suspended:
			f.Severity, f.Title = doctor.Fail, "The App is suspended on "+i.owner
			f.Detail = "GitHub refuses every call for a suspended installation."
			f.Fix = doctor.Fix{Text: "Unsuspend the App in the GitHub settings of " + i.owner + ".", Link: appLink}
		default:
			f.Severity, f.Title = doctor.OK, "The App is installed on "+i.owner
		}
		out = append(out, f)
	}
	return out
}

// appPermissions: a needed permission is missing or too low (R5.16, github-integration R5.23, R5.24).
func appPermissions(ctx context.Context, env doctor.Env) []doctor.Finding {
	insts, out := installations(ctx, env, "github.app.permissions")
	if out != nil || len(insts) == 0 {
		return out
	}
	if env.GitHub == nil {
		return []doctor.Finding{skip("github.app.permissions", controllerSubject, "GitHub is not connected", "The GitHub App is not set up yet.")}
	}
	for _, i := range insts {
		s := installSubject(i)
		inst, err := installation(ctx, env, i.id)
		if err != nil {
			if errors.Is(err, github.ErrNotFound) {
				out = append(out, skip("github.app.permissions", s, "Permissions of "+i.owner+" were not checked", "The App is not installed there; see github.app.installation."))
			} else {
				out = append(out, skipFor("github.app.permissions", s, "The installation on "+i.owner, err))
			}
			continue
		}
		need := map[string]string{}
		for k, v := range RequiredPermissions {
			need[k] = v
		}
		if inst.AccountType == "Organization" {
			for k, v := range OrgPermissions {
				need[k] = v
			}
		}
		var missing []string
		for name, want := range need {
			if level(inst.Permissions[name]) < level(want) {
				got := inst.Permissions[name]
				if got == "" {
					got = "none"
				}
				missing = append(missing, fmt.Sprintf("%s (needs %s, has %s)", name, want, got))
			}
		}
		sort.Strings(missing)
		f := doctor.Finding{Check: "github.app.permissions", Subject: s}
		if len(missing) == 0 {
			f.Severity, f.Title = doctor.OK, "The App has every permission it needs on "+i.owner
		} else {
			f.Severity, f.Title = doctor.Fail, "The App lacks permissions on "+i.owner
			f.Detail = "Missing: " + strings.Join(missing, ", ") + "."
			f.Fix = doctor.Fix{Text: "Accept the permission update for the App in the GitHub settings of " + i.owner + ".", Link: appLink}
		}
		out = append(out, f)
	}
	return out
}
