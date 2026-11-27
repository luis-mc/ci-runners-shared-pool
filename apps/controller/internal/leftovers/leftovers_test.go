package leftovers_test

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/leftovers"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/runners"
)

const root = `C:\ProgramData\crsp\runners`

var (
	now = time.Date(2026, 11, 18, 12, 0, 0, 0, time.UTC)
	// The service names of the real case: 84 characters, made by an old test controller.
	oldSvc1 = "actions.runner.luis-mc-home-automation.crsp-light-home-automation-abcdef1234-windows"
	oldSvc2 = "actions.runner.luis-mc-home-automation.crsp-light-home-automation-1234567890-windows"
	mySvc   = "actions.runner.luis-mc-shop.mine-windows"
)

// fakeOS is an in-memory Windows. Every mutating call is recorded in calls.
type fakeOS struct {
	mu       sync.Mutex
	admin    bool
	svcs     []leftovers.OSService
	dirs     []leftovers.Dir
	files    []leftovers.File
	accts    []leftovers.OSAccount
	sizes    map[string]int64
	links    map[string]string // path -> resolved path
	failDel  map[string]error  // service name -> error on DeleteService
	failStop map[string]error
	calls    []string
}

func (f *fakeOS) GOOS() string { return "windows" }
func (f *fakeOS) Root() string { return root }
func (f *fakeOS) Admin() bool  { return f.admin }
func (f *fakeOS) Services(context.Context) ([]leftovers.OSService, error) {
	return append([]leftovers.OSService(nil), f.svcs...), nil
}
func (f *fakeOS) Dirs(context.Context, string) ([]leftovers.Dir, error) {
	return append([]leftovers.Dir(nil), f.dirs...), nil
}
func (f *fakeOS) Files(context.Context, string) ([]leftovers.File, error) {
	return append([]leftovers.File(nil), f.files...), nil
}
func (f *fakeOS) DirSize(_ context.Context, p string) (int64, error) { return f.sizes[p], nil }
func (f *fakeOS) Accounts(context.Context) ([]leftovers.OSAccount, error) {
	return append([]leftovers.OSAccount(nil), f.accts...), nil
}
func (f *fakeOS) Resolve(p string) (string, error) {
	if r, ok := f.links[p]; ok {
		return r, nil
	}
	return p, nil
}
func (f *fakeOS) rec(s string) { f.mu.Lock(); f.calls = append(f.calls, s); f.mu.Unlock() }

// StopService and DeleteService find the service by its exact name only.
func (f *fakeOS) hasSvc(name string) bool {
	for _, s := range f.svcs {
		if s.Name == name {
			return true
		}
	}
	return false
}
func (f *fakeOS) StopService(_ context.Context, name string) error {
	f.rec("stop " + name)
	if !f.hasSvc(name) {
		return fmt.Errorf("open: %w", leftovers.ErrNotExist)
	}
	return f.failStop[name]
}
func (f *fakeOS) DeleteService(_ context.Context, name string) error {
	f.rec("delete-service " + name)
	if !f.hasSvc(name) {
		return fmt.Errorf("open: %w", leftovers.ErrNotExist)
	}
	return f.failDel[name]
}
func (f *fakeOS) RemoveAll(p string) error { f.rec("remove-all " + p); return nil }
func (f *fakeOS) DeleteAccount(_ context.Context, n string) error {
	f.rec("delete-account " + n)
	for _, a := range f.accts {
		if a.Name == n {
			return nil
		}
	}
	return fs.ErrNotExist
}

func rf(name string) string { return root + `\` + name }

// machine is the Windows PC of the real case plus what this controller owns.
func machine() *fakeOS {
	old := now.Add(-48 * time.Hour)
	f := &fakeOS{
		admin: true,
		svcs: []leftovers.OSService{
			{Name: oldSvc1, Binary: `"` + rf("crsp-light-home-automation-abcdef1234-windows") + `\bin\RunnerService.exe"`, Account: `.\crsp-crsp-light-0dfd`},
			{Name: oldSvc2, Binary: rf("crsp-light-home-automation-1234567890-windows") + `\bin\RunnerService.exe`, Account: `.\crsp-crsp-light-25fb`},
			{Name: mySvc, Binary: rf("mine") + `\bin\RunnerService.exe`, Account: `.\crsp-mine`},
			{Name: "actions.runner.other.elsewhere", Binary: `D:\other\RunnerService.exe`, Account: `.\bob`},
			{Name: "Spooler", Binary: `C:\Windows\spooler.exe`, Account: "LocalSystem"},
		},
		dirs: []leftovers.Dir{
			{Name: "crsp-light-home-automation-abcdef1234-windows", Path: rf("crsp-light-home-automation-abcdef1234-windows"), ModTime: old},
			{Name: "crsp-light-home-automation-1234567890-windows", Path: rf("crsp-light-home-automation-1234567890-windows"), ModTime: old},
			{Name: "mine", Path: rf("mine"), ModTime: old},
			{Name: "_tools", Path: rf("_tools"), ModTime: old},
		},
		files: []leftovers.File{
			{Name: "_download-crsp-light-comics-organizer-windows.zip", Path: rf("_download-crsp-light-comics-organizer-windows.zip"), Size: 99 << 20, ModTime: old},
			{Name: "_download-fresh.zip", Path: rf("_download-fresh.zip"), Size: 5, ModTime: now.Add(-3 * time.Minute)},
			{Name: "_download-mine.zip", Path: rf("_download-mine.zip"), Size: 7, ModTime: old},
			{Name: "notes.txt", Path: rf("notes.txt"), Size: 1, ModTime: old},
		},
		accts: []leftovers.OSAccount{
			{Name: "crsp-crsp-light-0dfd", Description: leftovers.AccountDescription},
			{Name: "crsp-crsp-light-25fb", Description: leftovers.AccountDescription},
			{Name: "crsp-mine", Description: leftovers.AccountDescription},
			{Name: runners.RunnersGroup, Description: leftovers.AccountDescription},
			{Name: "crsp-nodesc", Description: ""},
			{Name: "alice", Description: leftovers.AccountDescription},
			{Name: "bob", Description: "a person"},
		},
		sizes:    map[string]int64{rf("crsp-light-home-automation-abcdef1234-windows"): 1000, rf("crsp-light-home-automation-1234567890-windows"): 2000},
		links:    map[string]string{},
		failDel:  map[string]error{},
		failStop: map[string]error{},
	}
	return f
}

var mine = []leftovers.Owned{{Name: "mine", ServiceName: mySvc}}

func ids(sc leftovers.Scan) []string {
	var out []string
	for _, it := range sc.Items {
		out = append(out, it.ID())
	}
	return out
}

func TestScanFindsTheRealCaseAndNothingOwned(t *testing.T) {
	f := machine()
	sc, err := leftovers.Find(context.Background(), f, mine, now)
	if err != nil {
		t.Fatal(err)
	}
	if !sc.Admin || !sc.At.Equal(now) {
		t.Fatalf("scan %+v", sc)
	}
	want := []string{
		"service:" + oldSvc2, "service:" + oldSvc1, // sorted by name
		"folder:" + rf("crsp-light-home-automation-1234567890-windows"), "folder:" + rf("crsp-light-home-automation-abcdef1234-windows"),
		"account:crsp-crsp-light-0dfd", "account:crsp-crsp-light-25fb",
		"download:" + rf("_download-crsp-light-comics-organizer-windows.zip"),
	}
	if got := ids(sc); strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("items\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	if len(oldSvc1) != 84 {
		t.Fatalf("the test name is %d characters", len(oldSvc1))
	}
	svc := sc.Items[1]
	if svc.Path != rf("crsp-light-home-automation-abcdef1234-windows") || svc.Account != `.\crsp-crsp-light-0dfd` || svc.Why == "" {
		t.Fatalf("service item %+v", svc)
	}
	if sc.Items[3].SizeBytes != 1000 || sc.Items[6].SizeBytes != 99<<20 {
		t.Fatalf("sizes %+v %+v", sc.Items[3], sc.Items[6])
	}
}

func TestScanNeverMutates(t *testing.T) {
	f := machine()
	if _, err := leftovers.Find(context.Background(), f, nil, now); err != nil {
		t.Fatal(err)
	}
	if len(f.calls) != 0 {
		t.Fatalf("a scan changed things: %v", f.calls)
	}
}

func TestOwnedByNameServiceNameAndBinaryFolder(t *testing.T) {
	f := machine()
	// Owned by runner name only: the folder, the account, the download and the
	// service (binary in that runner's folder) are all owned.
	owned := []leftovers.Owned{{Name: "crsp-light-home-automation-abcdef1234-windows"}}
	sc, _ := leftovers.Find(context.Background(), f, owned, now)
	for _, id := range ids(sc) {
		if strings.Contains(id, "abcdef1234") {
			t.Fatalf("owned by folder still listed: %s", id)
		}
	}
	// Owned by service name only: the service, and the account it runs as, are owned.
	owned = []leftovers.Owned{{Name: "elsewhere", ServiceName: oldSvc2}}
	sc, _ = leftovers.Find(context.Background(), f, owned, now)
	got := strings.Join(ids(sc), "|")
	if strings.Contains(got, "service:"+oldSvc2) || strings.Contains(got, "account:crsp-crsp-light-25fb") {
		t.Fatalf("owned by service name still listed: %s", got)
	}
	if !strings.Contains(got, "service:"+oldSvc1) {
		t.Fatalf("other service lost: %s", got)
	}
	// Names are compared without case on Windows.
	sc, _ = leftovers.Find(context.Background(), f, []leftovers.Owned{{Name: "MINE"}, {Name: "x", ServiceName: strings.ToUpper(oldSvc1)}}, now)
	if strings.Contains(strings.Join(ids(sc), "|"), oldSvc1) {
		t.Fatal("case differences must not hide ownership")
	}
}

func TestServiceNeedsRootOrCrspAccount(t *testing.T) {
	f := machine()
	// Outside the root and not a crsp account: never a candidate.
	sc, _ := leftovers.Find(context.Background(), f, mine, now)
	if strings.Contains(strings.Join(ids(sc), "|"), "elsewhere") {
		t.Fatal("a foreign runner service was listed")
	}
	// Outside the root but runs as a crsp runner account: a candidate.
	f.svcs = append(f.svcs, leftovers.OSService{Name: "actions.runner.x.y", Binary: `D:\old\RunnerService.exe`, Account: `.\crsp-crsp-light-0dfd`})
	sc, _ = leftovers.Find(context.Background(), f, mine, now)
	if !strings.Contains(strings.Join(ids(sc), "|"), "service:actions.runner.x.y") {
		t.Fatal("a service of a crsp account must be listed")
	}
	// The same account name without the description does not count.
	f.svcs[len(f.svcs)-1].Account = `.\crsp-nodesc`
	sc, _ = leftovers.Find(context.Background(), f, mine, now)
	if strings.Contains(strings.Join(ids(sc), "|"), "actions.runner.x.y") {
		t.Fatal("an account without the description is not a crsp account")
	}
}

func TestAccountsNeedPrefixAndDescription(t *testing.T) {
	sc, _ := leftovers.Find(context.Background(), machine(), mine, now)
	got := strings.Join(ids(sc), "|")
	for _, no := range []string{"account:alice", "account:bob", "account:crsp-nodesc", "account:" + runners.RunnersGroup, "account:crsp-mine"} {
		if strings.Contains(got, no) {
			t.Fatalf("%s must not be listed: %s", no, got)
		}
	}
}

func TestDownloadsNewerThanTenMinutesAreLeftAlone(t *testing.T) {
	sc, _ := leftovers.Find(context.Background(), machine(), mine, now)
	got := strings.Join(ids(sc), "|")
	if strings.Contains(got, "_download-fresh") || strings.Contains(got, "_download-mine") || strings.Contains(got, "notes.txt") {
		t.Fatalf("listed: %s", got)
	}
	// Ten minutes later the fresh one is a leftover.
	sc, _ = leftovers.Find(context.Background(), machine(), mine, now.Add(8*time.Minute))
	if !strings.Contains(strings.Join(ids(sc), "|"), "_download-fresh.zip") {
		t.Fatal("an old enough download must be listed")
	}
}

func remove(t *testing.T, f *fakeOS, owned []leftovers.Owned, refs ...leftovers.Ref) []leftovers.Result {
	t.Helper()
	var logs []string
	res, err := leftovers.RemoveAt(context.Background(), f, owned, refs, now, func(s string) { logs = append(logs, s) })
	if err != nil {
		t.Fatal(err)
	}
	if len(res) != len(refs) {
		t.Fatalf("%d results for %d refs", len(res), len(refs))
	}
	if len(logs) == 0 {
		t.Fatal("nothing was logged")
	}
	return res
}

func TestRemoveEverythingOfTheRealCaseInOrder(t *testing.T) {
	f := machine()
	refs := []leftovers.Ref{
		{Kind: leftovers.KindDownload, Name: rf("_download-crsp-light-comics-organizer-windows.zip")},
		{Kind: leftovers.KindAccount, Name: "crsp-crsp-light-0dfd"},
		{Kind: leftovers.KindFolder, Name: rf("crsp-light-home-automation-abcdef1234-windows")},
		{Kind: leftovers.KindService, Name: oldSvc1},
	}
	res := remove(t, f, mine, refs...)
	for i, r := range res {
		if r.Outcome != leftovers.Removed || r.Error != "" || len(r.Steps) == 0 || r.Name != refs[i].Name {
			t.Fatalf("result %d: %+v", i, r)
		}
	}
	want := []string{"stop " + oldSvc1, "delete-service " + oldSvc1, "remove-all " + rf("crsp-light-home-automation-abcdef1234-windows"),
		"delete-account crsp-crsp-light-0dfd", "remove-all " + rf("_download-crsp-light-comics-organizer-windows.zip")}
	if strings.Join(f.calls, "\n") != strings.Join(want, "\n") {
		t.Fatalf("calls\n%s", strings.Join(f.calls, "\n"))
	}
	if !strings.Contains(strings.Join(res[3].Steps, "|"), "deleted service "+oldSvc1) ||
		!strings.Contains(strings.Join(res[2].Steps, "|"), "(1000 bytes)") ||
		!strings.Contains(strings.Join(res[1].Steps, "|"), "deleted account crsp-crsp-light-0dfd") {
		t.Fatalf("steps %+v", res)
	}
}

func TestRemoveRefusesPathsOutsideTheCrspFolder(t *testing.T) {
	f := machine()
	for _, p := range []string{
		root + `\..\Windows`,
		rf(`..\..\Windows`),
		`C:\ProgramData\crsp\runners-evil\x`,
		`C:\ProgramData\crsp\runners-evil`,
		root,
		root + `\`,
		`C:\Windows\System32`,
		`D:\crsp\runners\x`,
	} {
		for _, k := range []leftovers.Kind{leftovers.KindFolder, leftovers.KindDownload} {
			res := remove(t, f, nil, leftovers.Ref{Kind: k, Name: p})
			if res[0].Outcome != leftovers.Refused || res[0].Error == "" {
				t.Fatalf("%s %q: %+v", k, p, res[0])
			}
		}
	}
	if len(f.calls) != 0 {
		t.Fatalf("calls %v", f.calls)
	}
}

func TestRemoveRefusesAThroughALinkAndAcceptsCaseDifferences(t *testing.T) {
	f := machine()
	old := rf("crsp-light-home-automation-abcdef1234-windows")
	f.links[old] = `C:\Windows`
	res := remove(t, f, mine, leftovers.Ref{Kind: leftovers.KindFolder, Name: old})
	if res[0].Outcome != leftovers.Refused || !strings.Contains(res[0].Error, "link") {
		t.Fatalf("%+v", res[0])
	}
	// The same folder spelled in other case and with forward slashes is the same place.
	f = machine()
	odd := `c:/programdata/CRSP/Runners/CRSP-light-home-automation-1234567890-windows`
	res = remove(t, f, mine, leftovers.Ref{Kind: leftovers.KindFolder, Name: odd})
	if res[0].Outcome != leftovers.Removed {
		t.Fatalf("%+v", res[0])
	}
}

func TestRemoveRefusesWhatIsNotOurs(t *testing.T) {
	f := machine()
	res := remove(t, f, mine,
		leftovers.Ref{Kind: leftovers.KindService, Name: "Spooler"},
		leftovers.Ref{Kind: leftovers.KindService, Name: "actions.runner.other.elsewhere"},
		leftovers.Ref{Kind: leftovers.KindAccount, Name: "alice"},
		leftovers.Ref{Kind: leftovers.KindAccount, Name: "crsp-nodesc"},
		leftovers.Ref{Kind: leftovers.KindAccount, Name: runners.RunnersGroup},
		leftovers.Ref{Kind: leftovers.KindFolder, Name: rf("_tools")},
		leftovers.Ref{Kind: leftovers.KindDownload, Name: rf("_download-fresh.zip")},
		leftovers.Ref{Kind: leftovers.KindDownload, Name: rf("notes.txt")},
		leftovers.Ref{Kind: "bogus", Name: "x"},
	)
	for i, r := range res {
		if r.Outcome != leftovers.Refused || r.Error == "" {
			t.Fatalf("ref %d: %+v", i, r)
		}
	}
	if len(f.calls) != 0 {
		t.Fatalf("calls %v", f.calls)
	}
}

func TestRemoveRefusesOwnedEvenIfItWasLeftoverAtScanTime(t *testing.T) {
	f := machine()
	refs := []leftovers.Ref{
		{Kind: leftovers.KindService, Name: oldSvc1},
		{Kind: leftovers.KindFolder, Name: rf("crsp-light-home-automation-abcdef1234-windows")},
		{Kind: leftovers.KindAccount, Name: "crsp-crsp-light-0dfd"},
		{Kind: leftovers.KindDownload, Name: rf("_download-crsp-light-comics-organizer-windows.zip")},
	}
	// The scan found all four; then the controller adopts a runner with that name and service.
	owned := []leftovers.Owned{
		{Name: "crsp-light-home-automation-abcdef1234-windows", ServiceName: oldSvc1},
		{Name: "crsp-light-comics-organizer-windows"},
	}
	res := remove(t, f, owned, refs...)
	for i, r := range res {
		if r.Outcome != leftovers.Refused || !strings.Contains(r.Error, "belongs") {
			t.Fatalf("ref %d: %+v", i, r)
		}
	}
	if len(f.calls) != 0 {
		t.Fatalf("calls %v", f.calls)
	}
}

func TestRemoveAlreadyGone(t *testing.T) {
	f := machine()
	f.svcs, f.dirs, f.accts, f.files = nil, nil, nil, nil
	res := remove(t, f, mine,
		leftovers.Ref{Kind: leftovers.KindService, Name: oldSvc1},
		leftovers.Ref{Kind: leftovers.KindFolder, Name: rf("crsp-light-home-automation-abcdef1234-windows")},
		leftovers.Ref{Kind: leftovers.KindAccount, Name: "crsp-crsp-light-0dfd"},
		leftovers.Ref{Kind: leftovers.KindDownload, Name: rf("_download-crsp-light-comics-organizer-windows.zip")},
	)
	for i, r := range res {
		if r.Outcome != leftovers.AlreadyGone || r.Error != "" {
			t.Fatalf("ref %d: %+v", i, r)
		}
	}
	if len(f.calls) != 0 {
		t.Fatalf("calls %v", f.calls)
	}
}

func TestServiceGoneBetweenScanAndStopIsAlreadyGone(t *testing.T) {
	f := machine()
	f.failStop[oldSvc1] = fmt.Errorf("closed: %w", leftovers.ErrNotExist)
	res := remove(t, f, mine, leftovers.Ref{Kind: leftovers.KindService, Name: oldSvc1})
	if res[0].Outcome != leftovers.AlreadyGone {
		t.Fatalf("%+v", res[0])
	}
}

func TestPartialFailureKeepsGoing(t *testing.T) {
	f := machine()
	f.failDel[oldSvc1] = errors.New("access denied")
	f.failStop[oldSvc2] = errors.New("did not stop")
	refs := []leftovers.Ref{
		{Kind: leftovers.KindService, Name: oldSvc1},
		{Kind: leftovers.KindService, Name: oldSvc2},
		{Kind: leftovers.KindFolder, Name: rf("crsp-light-home-automation-abcdef1234-windows")},
		{Kind: leftovers.KindAccount, Name: "crsp-crsp-light-0dfd"},
	}
	res := remove(t, f, mine, refs...)
	if res[0].Outcome != leftovers.Failed || !strings.Contains(res[0].Error, "access denied") {
		t.Fatalf("%+v", res[0])
	}
	// A service that cannot stop is still deleted, and says so in its steps.
	if res[1].Outcome != leftovers.Removed || !strings.Contains(strings.Join(res[1].Steps, "|"), "could not stop service") {
		t.Fatalf("%+v", res[1])
	}
	if res[2].Outcome != leftovers.Removed || res[3].Outcome != leftovers.Removed {
		t.Fatalf("folder and account must go on: %+v %+v", res[2], res[3])
	}
}

func TestNotAdmin(t *testing.T) {
	f := machine()
	f.admin = false
	sc, _ := leftovers.Find(context.Background(), f, mine, now)
	if sc.Admin {
		t.Fatal("admin")
	}
	res := remove(t, f, mine,
		leftovers.Ref{Kind: leftovers.KindService, Name: oldSvc1},
		leftovers.Ref{Kind: leftovers.KindAccount, Name: "crsp-crsp-light-0dfd"},
	)
	for _, r := range res {
		if r.Outcome != leftovers.Failed || r.Error != "the agent needs administrator rights to remove this" {
			t.Fatalf("%+v", r)
		}
	}
	if len(f.calls) != 0 {
		t.Fatalf("calls %v", f.calls)
	}
}

// Doctor R5.48: an agent folder in the pool directory that belongs to no known
// runner (no service, no account, no download, no record) is a leftover on its
// own; the scan lists it and removes nothing, and a folder of an owned runner
// is never listed.
func TestUnusedAgentFolderIsFlaggedAlone(t *testing.T) {
	f := machine()
	f.dirs = []leftovers.Dir{
		{Name: "agent-unused", Path: rf("agent-unused"), ModTime: now.Add(-72 * time.Hour)},
		{Name: "mine", Path: rf("mine"), ModTime: now.Add(-72 * time.Hour)},
	}
	f.svcs, f.accts, f.files = nil, nil, nil
	f.sizes = map[string]int64{rf("agent-unused"): 313 << 20}
	sc, err := leftovers.Find(context.Background(), f, mine, now)
	if err != nil {
		t.Fatal(err)
	}
	if got := ids(sc); len(got) != 1 || got[0] != "folder:"+rf("agent-unused") {
		t.Fatalf("items = %v", got)
	}
	if sc.Items[0].SizeBytes != 313<<20 || sc.Items[0].Why == "" {
		t.Errorf("%+v", sc.Items[0])
	}
	if len(f.calls) != 0 {
		t.Fatalf("the scan removed something: %v", f.calls)
	}
}
