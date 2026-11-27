package checks

import (
	"context"
	"sort"
	"strings"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/doctor"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/runners"
)

// runnerPostgresLogins: a Postgres login that no runner uses (a leftover of a
// runner removed while the server was down, or of an older build). It only
// reads and offers the removal as a next step; it never removes (R5.7,
// R5.59). New check, OPS53.
func runnerPostgresLogins(ctx context.Context, env doctor.Env) []doctor.Finding {
	if env.Postgres == nil {
		return []doctor.Finding{skip("runner.postgres_logins", controllerSubject, "Postgres is not read", "No Postgres server was reported, so its logins were not checked.")}
	}
	logins, err := env.Postgres.PostgresLogins(ctx)
	if err != nil {
		return []doctor.Finding{skipFor("runner.postgres_logins", controllerSubject, "The Postgres logins", err)}
	}
	rs, err := env.Fleet.Runners(ctx)
	if err != nil {
		return []doctor.Finding{skipFor("runner.postgres_logins", controllerSubject, "The runner list", err)}
	}
	used := map[string]bool{}
	for _, r := range rs {
		used[runners.LoginName(r.ID)] = true
	}
	var out []doctor.Finding
	out = append(out, loginSettingFindings(logins)...)
	for _, l := range logins {
		if used[l.Name] {
			continue
		}
		why := "It belongs to no shared owner role, so an older build made it."
		if l.OwnerRole != "" {
			why = "It is a member of " + l.OwnerRole + "."
		}
		out = append(out, doctor.Finding{Check: "runner.postgres_logins",
			Subject: doctor.Subject{Kind: "runner", ID: l.Name, Name: l.Name}, Severity: doctor.Warn,
			Title:  "The Postgres login " + l.Name + " is used by no runner",
			Detail: "No runner has this login, so its database and password are left over. " + why + " Nothing was removed.",
			Fix:    doctor.Fix{Text: "Remove the login " + l.Name + " from the Doctor screen when you are sure no job needs it. The doctor never removes it on its own.", Link: "/doctor"}})
	}
	if len(out) == 0 {
		return []doctor.Finding{{Check: "runner.postgres_logins", Subject: controllerSubject, Severity: doctor.OK,
			Title: "Every Postgres login belongs to a runner and has its siblings' settings"}}
	}
	return out
}

func loginSignature(l doctor.PostgresLogin) string {
	return l.Attrs + " | " + strings.Join(l.Members, ",")
}

// loginSettingFindings warns when a login lacks createrole_self_grant 'set,
// inherit' (a second runner of a repository then cannot SET ROLE to a role it
// made, SQLSTATE 42501), or differs in attributes or memberships from the
// other logins of its owner role; the most common set is the reference and a
// tie goes to the first by text. It only reads (runners R5.97, OPS54).
func loginSettingFindings(logins []doctor.PostgresLogin) []doctor.Finding {
	var out []doctor.Finding
	byOwner := map[string][]doctor.PostgresLogin{}
	for _, l := range logins {
		if !l.SelfGrant {
			out = append(out, doctor.Finding{Check: "runner.postgres_logins",
				Subject: doctor.Subject{Kind: "runner", ID: l.Name, Name: l.Name}, Severity: doctor.Warn,
				Title:  "The Postgres login " + l.Name + " lacks createrole_self_grant",
				Detail: "The login does not have createrole_self_grant = 'set, inherit', so a job that makes a role cannot SET ROLE to it (error 42501). The setting is added the next time the runner is provisioned.",
				Fix:    doctor.Fix{Text: "Restart the runner so its login is provisioned again; nothing is changed by the doctor.", Link: "/doctor"}})
		}
		if l.OwnerRole != "" {
			byOwner[l.OwnerRole] = append(byOwner[l.OwnerRole], l)
		}
	}
	owners := make([]string, 0, len(byOwner))
	for o := range byOwner {
		owners = append(owners, o)
	}
	sort.Strings(owners)
	for _, o := range owners {
		group := byOwner[o]
		if len(group) < 2 {
			continue
		}
		count := map[string]int{}
		for _, l := range group {
			count[loginSignature(l)]++
		}
		ref := ""
		for sig, n := range count {
			if ref == "" || n > count[ref] || (n == count[ref] && sig < ref) {
				ref = sig
			}
		}
		for _, l := range group {
			if loginSignature(l) == ref {
				continue
			}
			out = append(out, doctor.Finding{Check: "runner.postgres_logins",
				Subject: doctor.Subject{Kind: "runner", ID: l.Name, Name: l.Name}, Severity: doctor.Warn,
				Title:  "The Postgres login " + l.Name + " differs from the other logins of " + o,
				Detail: "Its attributes or role memberships are not those of its siblings (" + loginSignature(l) + " against " + ref + "), so a job may work on one runner and fail on another. Nothing was changed.",
				Fix:    doctor.Fix{Text: "Remove the login and let the runner provision it again; a new login copies its sibling. The doctor changes nothing on its own.", Link: "/doctor"}})
		}
	}
	return out
}
