package linux

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/backend"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/backend/persistent"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/backend/sys"
)

// Find lists runners from the systemd unit list: units named
// `actions.runner.*` (runners R5.5). The folder comes from WorkingDirectory or
// the ExecStart path, the account from User. A runner whose folder the agent
// cannot read is still reported.
func (b *Backend) Find(ctx context.Context) ([]backend.Found, error) {
	res, err := b.run(ctx, "list units", sys.Command{Name: "systemctl", Args: []string{
		"list-unit-files", "actions.runner.*", "--type=service", "--no-legend", "--no-pager"}})
	if err != nil {
		return nil, err
	}
	var found []backend.Found
	for _, line := range strings.Split(res.Stdout, "\n") {
		fields := strings.Fields(line)
		if len(fields) == 0 || !strings.HasPrefix(fields[0], "actions.runner.") {
			continue
		}
		svc := strings.TrimSuffix(fields[0], ".service")
		show, err := b.run(ctx, "show unit", sys.Command{Name: "systemctl", Args: []string{
			"show", svc + ".service", "-p", "User", "-p", "WorkingDirectory", "-p", "ExecStart", "-p", "ActiveState"}})
		if err != nil {
			return nil, err
		}
		found = append(found, b.parseShow(svc, show.Stdout))
	}
	return found, nil
}

func (b *Backend) parseShow(svc, out string) backend.Found {
	kv := map[string]string{}
	for _, l := range strings.Split(out, "\n") {
		k, v, ok := strings.Cut(strings.TrimSpace(l), "=")
		if ok {
			kv[k] = v
		}
	}
	folder := strings.TrimPrefix(kv["WorkingDirectory"], "!")
	if folder == "" || folder == "[not set]" {
		folder = folderFromExecStart(kv["ExecStart"])
	}
	f := backend.Found{
		ServiceName: svc, Folder: folder, Account: kv["User"],
		Running: kv["ActiveState"] == "active",
		Name:    svc[strings.LastIndex(svc, ".")+1:],
	}
	f.SessionRefused, f.SessionWaitUntil = persistent.SessionWait(folder, time.Now(), 0)
	f.RegistrationLost = persistent.RegistrationLost(folder)
	meta, err := persistent.ReadMeta(b.d.FS, folder+"/"+persistent.MetaFile)
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

// folderFromExecStart reads `{ path=/x/actions-runner/bin/runsvc.sh ; ... }`.
func folderFromExecStart(s string) string {
	i := strings.Index(s, "path=")
	if i < 0 {
		return ""
	}
	p := strings.Fields(s[i+len("path="):])
	if len(p) == 0 {
		return ""
	}
	path := strings.TrimSuffix(p[0], ";")
	path = path[:strings.LastIndex(path, "/")+0]
	path = strings.TrimSuffix(path, "/bin")
	return path
}

func (b *Backend) lookup(ctx context.Context, service string) (backend.Found, bool, error) {
	all, err := b.Find(ctx)
	if err != nil {
		return backend.Found{}, false, fmt.Errorf("find runner: %w", err)
	}
	for _, f := range all {
		if f.ServiceName == service {
			return f, true, nil
		}
	}
	return backend.Found{}, false, nil
}
