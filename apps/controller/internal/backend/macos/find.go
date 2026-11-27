package macos

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/backend"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/backend/persistent"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/backend/sys"
)

type plistJSON struct {
	UserName         string `json:"UserName"`
	WorkingDirectory string `json:"WorkingDirectory"`
	ProgramArguments []string
}

// Find lists runners from launchd: jobs named `actions.runner.*` (runners
// R5.5). Names come from `launchctl list` and from the daemons folder; the
// folder and account come from the plist. A job whose plist the agent cannot
// read is still reported, with the folder marked unreadable.
func (b *Backend) Find(ctx context.Context) ([]backend.Found, error) {
	list, err := b.run(ctx, "list jobs", sys.Command{Name: "launchctl", Args: []string{"list"}})
	if err != nil {
		return nil, err
	}
	running := map[string]bool{}
	var labels []string
	seen := map[string]bool{}
	for _, l := range strings.Split(list.Stdout, "\n") {
		f := strings.Fields(l)
		if len(f) < 3 || !strings.HasPrefix(f[2], "actions.runner.") {
			continue
		}
		running[f[2]] = f[0] != "-"
		if !seen[f[2]] {
			seen[f[2]], labels = true, append(labels, f[2])
		}
	}
	ls, err := b.d.Cmd.Run(ctx, sys.Command{Name: "ls", Args: []string{"-1", b.cfg.DaemonsDir}})
	if err == nil && ls.ExitCode == 0 {
		for _, n := range strings.Split(ls.Stdout, "\n") {
			n = strings.TrimSuffix(strings.TrimSpace(n), ".plist")
			if strings.HasPrefix(n, "actions.runner.") && !seen[n] {
				seen[n], labels = true, append(labels, n)
			}
		}
	}
	var found []backend.Found
	for _, label := range labels {
		found = append(found, b.describe(ctx, label, running[label]))
	}
	return found, nil
}

func (b *Backend) describe(ctx context.Context, label string, running bool) backend.Found {
	f := backend.Found{ServiceName: label, Running: running, Name: label[strings.LastIndex(label, ".")+1:]}
	res, err := b.d.Cmd.Run(ctx, sys.Command{Name: "plutil", Args: []string{"-convert", "json", "-o", "-", b.plistPath(label)}})
	if err != nil || res.ExitCode != 0 {
		f.FolderUnreadable = true
		return f
	}
	var p plistJSON
	if err := json.Unmarshal([]byte(res.Stdout), &p); err != nil {
		f.FolderUnreadable = true
		return f
	}
	f.Account = p.UserName
	f.Folder = p.WorkingDirectory
	meta, err := persistent.ReadMeta(b.d.FS, f.Folder+"/"+persistent.MetaFile)
	switch {
	case err != nil:
		f.FolderUnreadable = true
	case meta != nil:
		f.Managed = true
		f.Name = meta.Name
		f.Labels = meta.Labels
		f.RunnerVersion = meta.RunnerVersion
	}
	return f
}

func (b *Backend) lookup(ctx context.Context, label string) (backend.Found, bool, error) {
	all, err := b.Find(ctx)
	if err != nil {
		return backend.Found{}, false, fmt.Errorf("find runner: %w", err)
	}
	for _, f := range all {
		if f.ServiceName == label {
			return f, true, nil
		}
	}
	return backend.Found{}, false, nil
}
