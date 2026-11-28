package windows

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/backend"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/backend/persistent"
)

type serviceRow struct {
	Name      string `json:"Name"`
	State     string `json:"State"`
	PathName  string `json:"PathName"`
	StartName string `json:"StartName"`
}

// Find lists runners from the Win32_Service list: entries named
// `actions.runner.*`, the path giving the folder and StartName the account
// (runners R5.5; port of dev-standards test_dl318_doctor_locked_runner.py). A
// runner whose folder the agent cannot read is still reported.
func (b *Backend) Find(ctx context.Context) ([]backend.Found, error) {
	res, err := b.ps(ctx, scriptFind, nil, nil)
	if err != nil {
		return nil, fmt.Errorf("list services: %w", err)
	}
	if res.ExitCode != 0 {
		return nil, fmt.Errorf("list services: powershell exited %d: %s", res.ExitCode, b.redact(plainError(res.Stderr, res.Stdout)))
	}
	return b.parseFind(res.Stdout)
}

func (b *Backend) parseFind(out string) ([]backend.Found, error) {
	out = strings.TrimSpace(out)
	if out == "" || out == "null" {
		return nil, nil
	}
	var rows []serviceRow
	if strings.HasPrefix(out, "{") { // one row: PowerShell may print an object
		var one serviceRow
		if err := json.Unmarshal([]byte(out), &one); err != nil {
			return nil, err
		}
		rows = []serviceRow{one}
	} else if err := json.Unmarshal([]byte(out), &rows); err != nil {
		return nil, fmt.Errorf("read service list: %w", err)
	}
	var found []backend.Found
	for _, r := range rows {
		if !strings.HasPrefix(r.Name, "actions.runner.") {
			continue
		}
		f := backend.Found{
			ServiceName: r.Name,
			Folder:      folderFromPath(r.PathName),
			Account:     r.StartName,
			Running:     strings.EqualFold(r.State, "Running"),
			Name:        r.Name[strings.LastIndex(r.Name, ".")+1:],
		}
		f.SessionRefused, f.SessionWaitUntil = persistent.SessionWait(f.Folder, time.Now(), 0)
		f.RegistrationLost = persistent.RegistrationLostIn(b.d.FS, f.Folder, Join)
		meta, err := persistent.ReadMeta(b.d.FS, Join(f.Folder, persistent.MetaFile))
		switch {
		case err != nil:
			f.FolderUnreadable = true
		case meta != nil:
			f.Managed = true
			f.Name = meta.Name
			f.Labels = meta.Labels
			f.RunnerVersion = meta.RunnerVersion
		}
		found = append(found, f)
	}
	return found, nil
}

// folderFromPath turns `"C:\x\runner\bin\RunnerService.exe"` into `C:\x\runner`.
func folderFromPath(p string) string {
	p = strings.TrimSpace(p)
	if strings.HasPrefix(p, `"`) {
		if i := strings.Index(p[1:], `"`); i >= 0 {
			p = p[1 : 1+i]
		}
	} else if i := strings.Index(strings.ToLower(p), ".exe"); i >= 0 {
		p = p[:i+4]
	}
	p = strings.TrimRight(p, `\`)
	if i := strings.LastIndex(p, `\`); i >= 0 {
		p = p[:i] // drop RunnerService.exe
	}
	p = strings.TrimSuffix(strings.TrimRight(p, `\`), `\bin`)
	return p
}

func (b *Backend) lookup(ctx context.Context, service string) (backend.Found, bool, error) {
	all, err := b.Find(ctx)
	if err != nil {
		return backend.Found{}, false, err
	}
	for _, f := range all {
		if strings.EqualFold(f.ServiceName, service) {
			return f, true, nil
		}
	}
	return backend.Found{}, false, nil
}
