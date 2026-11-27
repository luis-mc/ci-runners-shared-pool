package checks

import (
	"context"
	"fmt"
	"strings"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/caches"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/doctor"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/leftovers"
)

// machineRunnerLeftovers: runner services, folders, accounts and downloads on a
// machine that no runner of this controller owns (R5.48, OPS33). The check
// reads only (R5.7): the agent scans, the doctor lists. Removing is the
// button of the finding, run from the console with its own confirm.
func machineRunnerLeftovers(ctx context.Context, env doctor.Env) []doctor.Finding {
	const id = "machine.runner_leftovers"
	ms, out := scopedMachines(ctx, env, id)
	if len(out) > 0 {
		return out
	}
	lf, ok := env.Fleet.(doctor.LeftoverFleet)
	if !ok {
		return []doctor.Finding{skip(id, controllerSubject, "Runner leftovers are not looked for", "This controller cannot ask the agents for runner leftovers.")}
	}
	for _, m := range ms {
		if !isOnline(env, m) {
			out = append(out, offlineSkip(id, m))
			continue
		}
		scan, err := lf.Leftovers(ctx, m.ID)
		if err != nil {
			out = append(out, skipFor(id, machineSubject(m), "The runner leftovers of "+m.Name, err))
			continue
		}
		out = append(out, leftoversFinding(m, scan))
	}
	return out
}

func leftoversFinding(m doctor.Machine, scan leftovers.Scan) doctor.Finding {
	f := doctor.Finding{Check: "machine.runner_leftovers", Subject: machineSubject(m)}
	if len(scan.Items) == 0 {
		f.Severity, f.Title = doctor.OK, "No runner leftovers on "+m.Name
		return f
	}
	f.Severity = doctor.Fail
	n := len(scan.Items)
	if n == 1 {
		f.Title = "1 runner leftover no controller owns on " + m.Name
	} else {
		f.Title = fmt.Sprintf("%d runner leftovers no controller owns on %s", n, m.Name)
	}
	lines := make([]string, 0, n+2)
	lines = append(lines, "No runner of this controller owns these, and they lie in the crsp folders or carry the crsp names:")
	for _, it := range scan.Items {
		f.Items = append(f.Items, doctor.Item{
			Kind: string(it.Kind), Name: it.Name, Path: it.Path, SizeBytes: it.SizeBytes, Account: it.Account, Why: it.Why,
		})
		lines = append(lines, "- "+leftoverWords(it))
	}
	if !scan.Admin {
		f.NeedsAdmin = true
		lines = append(lines, "The agent on "+m.Name+" has no administrator rights, so it cannot remove them. Run the agent as a service with rights.")
	}
	f.Detail = strings.Join(lines, "\n")
	f.Fix = doctor.Fix{
		Text: "Press Remove. The agent on " + m.Name + " stops and deletes the services, deletes the folders and accounts and the download, and keeps every runner of this pool.",
		Link: machineLink(m),
	}
	f.Action = &doctor.Action{ID: doctor.ActionRemoveRunnerLeftovers, Label: "Remove"}
	return f
}

// leftoverWords says one item in words for people.
func leftoverWords(it leftovers.Item) string {
	var b strings.Builder
	b.WriteString(kindWord(it.Kind) + " " + it.Name)
	var parts []string
	if it.SizeBytes > 0 {
		parts = append(parts, caches.FormatBytes(it.SizeBytes))
	}
	if it.Account != "" && it.Account != it.Name {
		parts = append(parts, "account "+it.Account)
	}
	if len(parts) > 0 {
		b.WriteString(" (" + strings.Join(parts, ", ") + ")")
	}
	if it.Why != "" {
		b.WriteString(": " + it.Why)
	}
	return b.String()
}

func kindWord(k leftovers.Kind) string {
	switch k {
	case leftovers.KindService:
		return "Service"
	case leftovers.KindFolder:
		return "Folder"
	case leftovers.KindAccount:
		return "Account"
	case leftovers.KindDownload:
		return "Download"
	}
	return string(k)
}
