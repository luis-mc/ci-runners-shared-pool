package leftovers

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/runners"
)

// DownloadPrefix starts the name of a runner download under the runners folder.
const DownloadPrefix = "_download-"

// RecentDownload is how long a download file is left alone: a runner of
// another controller (or of this one) may be fetching it right now.
const RecentDownload = 10 * time.Minute

// snapshot is one read of the machine plus what this controller owns.
type snapshot struct {
	st    style
	root  string
	svcs  []OSService
	dirs  []Dir
	files []File
	accts []OSAccount

	ownedNames    map[string]bool // runner names
	ownedServices map[string]bool // service names
	ownedAccounts map[string]bool // account names
}

func take(ctx context.Context, o OS, owned []Owned) (*snapshot, error) {
	sn := &snapshot{st: styleOf(o.GOOS()), root: o.Root()}
	var err error
	if sn.svcs, err = o.Services(ctx); err != nil {
		return nil, fmt.Errorf("list services: %w", err)
	}
	if sn.dirs, err = o.Dirs(ctx, sn.root); err != nil {
		return nil, fmt.Errorf("list runner folders: %w", err)
	}
	if sn.files, err = o.Files(ctx, sn.root); err != nil {
		return nil, fmt.Errorf("list runner downloads: %w", err)
	}
	if sn.accts, err = o.Accounts(ctx); err != nil {
		return nil, fmt.Errorf("list accounts: %w", err)
	}
	sn.ownedNames, sn.ownedServices, sn.ownedAccounts = map[string]bool{}, map[string]bool{}, map[string]bool{}
	for _, ow := range owned {
		sn.ownedNames[sn.st.key(ow.Name)] = true
		sn.ownedAccounts[sn.st.key(runners.AccountName(ow.Name, nil))] = true
		if ow.ServiceName != "" {
			sn.ownedServices[sn.st.key(ow.ServiceName)] = true
		}
	}
	// The account of an owned service is owned too, whatever its name.
	for _, s := range sn.svcs {
		if sn.serviceOwned(s) {
			sn.ownedAccounts[sn.st.key(accountBase(s.Account))] = true
		}
	}
	return sn, nil
}

// serviceFolder is the folder of the service program: the runner folder when
// the program lies under the runners folder, else its own folder.
func (sn *snapshot) serviceFolder(s OSService) (folder, runnerDir string, underRoot bool) {
	bin := binaryPath(s.Binary)
	if rel, ok := sn.st.under(sn.root, bin); ok {
		rs, _ := sn.st.split(sn.root)
		return sn.st.join(append(rs, rel[0])), rel[0], true
	}
	segs, ok := sn.st.split(bin)
	if !ok || len(segs) == 0 {
		return "", "", false
	}
	return sn.st.join(segs[:len(segs)-1]), "", false
}

func (sn *snapshot) serviceOwned(s OSService) bool {
	if sn.ownedServices[sn.st.key(s.Name)] {
		return true
	}
	_, dir, under := sn.serviceFolder(s)
	return under && sn.ownedNames[sn.st.key(dir)]
}

// crspAccount reports whether the local account is a crsp runner account:
// the crsp- prefix and the crsp description, never the runners group.
func (sn *snapshot) crspAccount(name string) (OSAccount, bool) {
	if !crspName(name) {
		return OSAccount{}, false
	}
	for _, a := range sn.accts {
		if sn.st.key(a.Name) == sn.st.key(name) {
			return a, strings.TrimSpace(a.Description) == AccountDescription
		}
	}
	return OSAccount{}, false
}

func crspName(name string) bool {
	return strings.HasPrefix(name, runners.AccountPrefix) && name != runners.RunnersGroup
}

// downloadRunner is the runner name of a download file name.
func downloadRunner(file string) string {
	n := strings.TrimPrefix(file, DownloadPrefix)
	for _, ext := range []string{".tar.gz", ".tgz", ".zip", ".tar"} {
		if strings.HasSuffix(strings.ToLower(n), ext) {
			return n[:len(n)-len(ext)]
		}
	}
	if i := strings.LastIndex(n, "."); i > 0 {
		return n[:i]
	}
	return n
}

// items is the leftover list of the snapshot, sorted.
func (sn *snapshot) items(ctx context.Context, o OS, now time.Time) []Item {
	var out []Item
	for _, s := range sn.svcs {
		if !strings.HasPrefix(s.Name, ServicePrefix) || sn.serviceOwned(s) {
			continue
		}
		folder, _, under := sn.serviceFolder(s)
		_, crspAcc := sn.crspAccount(accountBase(s.Account))
		if !under && !crspAcc {
			continue
		}
		why := "No runner of this controller has this service. It was made by another controller."
		if !under {
			why = "It runs as a crsp runner account that no runner of this controller uses. It was made by another controller."
		}
		out = append(out, Item{Kind: KindService, Name: s.Name, Path: folder, Account: s.Account, Why: why})
	}
	for _, d := range sn.dirs {
		if strings.HasPrefix(d.Name, "_") || sn.ownedNames[sn.st.key(d.Name)] {
			continue
		}
		size, err := o.DirSize(ctx, d.Path)
		if err != nil {
			size = 0
		}
		out = append(out, Item{Kind: KindFolder, Name: d.Path, Path: d.Path, SizeBytes: size,
			Why: "No runner of this controller has this name. It was made by another controller."})
	}
	for _, a := range sn.accts {
		if !crspName(a.Name) || strings.TrimSpace(a.Description) != AccountDescription || sn.ownedAccounts[sn.st.key(a.Name)] {
			continue
		}
		out = append(out, Item{Kind: KindAccount, Name: a.Name, Account: a.Name,
			Why: "No runner of this controller uses this account. It was made by another controller."})
	}
	for _, f := range sn.files {
		if !strings.HasPrefix(f.Name, DownloadPrefix) || sn.ownedNames[sn.st.key(downloadRunner(f.Name))] {
			continue
		}
		if now.Sub(f.ModTime) < RecentDownload {
			continue
		}
		out = append(out, Item{Kind: KindDownload, Name: f.Path, Path: f.Path, SizeBytes: f.Size,
			Why: "No runner of this controller has this name. It is a runner download that was left behind."})
	}
	order := map[Kind]int{KindService: 0, KindFolder: 1, KindAccount: 2, KindDownload: 3}
	sort.SliceStable(out, func(i, j int) bool {
		if order[out[i].Kind] != order[out[j].Kind] {
			return order[out[i].Kind] < order[out[j].Kind]
		}
		return out[i].Name < out[j].Name
	})
	return out
}

// Find lists what no runner of owned owns: services, folders, accounts and
// downloads of another controller. It only reads; it never changes anything.
func Find(ctx context.Context, o OS, owned []Owned, now time.Time) (Scan, error) {
	sn, err := take(ctx, o, owned)
	if err != nil {
		return Scan{}, err
	}
	return Scan{Items: sn.items(ctx, o, now), Admin: o.Admin(), At: now}, nil
}
