package dockercompact_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/dockercompact"
)

const vhdx = `C:\Users\ci\AppData\Local\Docker\wsl\disk\docker_data.vhdx`

// fakeHost records every call in order and answers from rules. Nothing real runs.
type fakeHost struct {
	mu       sync.Mutex
	goos     string
	admin    bool
	calls    []string
	kvmSeq   []bool // answers of the /dev/kvm probe, in order; nil: always present
	kvmCalls int
	sizes    map[string]int64
	files    map[string]string
	fail     map[string]Result // call prefix -> answer
	failErr  map[string]error
	infoOK   int // `docker info` answers from this call on (1-based); 0 = never
	infoSeen int
	temp     []string
	removed  []string
}

type Result = dockercompact.Result

func newWin() *fakeHost {
	return &fakeHost{goos: "windows", admin: true, infoOK: 1,
		sizes: map[string]int64{vhdx: 236 << 30},
		files: map[string]string{},
		fail:  map[string]Result{}, failErr: map[string]error{}}
}

func (f *fakeHost) GOOS() string   { return f.goos }
func (f *fakeHost) Elevated() bool { return f.admin }
func (f *fakeHost) Getenv(k string) string {
	switch k {
	case "LOCALAPPDATA":
		return `C:\Users\ci\AppData\Local`
	case "APPDATA":
		return `C:\Users\ci\AppData\Roaming`
	}
	return ""
}
func (f *fakeHost) FileSize(p string) (int64, bool) { v, ok := f.sizes[p]; return v, ok }
func (f *fakeHost) ReadFile(p string) (string, error) {
	if v, ok := f.files[p]; ok {
		return v, nil
	}
	return "", errors.New("missing")
}
func (f *fakeHost) WriteTemp(pattern, content string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	name := `C:\Temp\` + strings.ReplaceAll(pattern, "*", "1")
	f.calls = append(f.calls, "write "+name+" "+strings.ReplaceAll(content, "\r\n", "|"))
	f.temp = append(f.temp, name)
	return name, nil
}
func (f *fakeHost) Remove(p string) { f.mu.Lock(); f.removed = append(f.removed, p); f.mu.Unlock() }
func (f *fakeHost) Sleep(context.Context, time.Duration) error {
	f.mu.Lock()
	f.calls = append(f.calls, "sleep")
	f.mu.Unlock()
	return nil
}

func (f *fakeHost) Run(_ context.Context, _ time.Duration, name string, args ...string) (Result, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	line := strings.TrimSpace(name + " " + strings.Join(args, " "))
	f.calls = append(f.calls, line)
	for prefix, err := range f.failErr {
		if strings.HasPrefix(line, prefix) {
			return Result{}, err
		}
	}
	for prefix, r := range f.fail {
		if strings.HasPrefix(line, prefix) {
			return r, nil
		}
	}
	if strings.HasSuffix(line, "sh -c test -e /dev/kvm") && f.kvmSeq != nil {
		n := f.kvmCalls
		f.kvmCalls++
		if n < len(f.kvmSeq) && !f.kvmSeq[n] {
			return Result{ExitCode: 1}, nil
		}
		return Result{}, nil
	}
	switch {
	case strings.HasPrefix(line, "reg query"):
		return Result{}, nil
	case strings.HasPrefix(line, "wsl -d docker-desktop -e df"):
		return Result{Out: "Filesystem 1-blocks Used Available Capacity Mounted on\r\n/dev/sdc 270000000000 76000000000 100 50% /mnt/docker-desktop-disk\r\n"}, nil
	case strings.HasPrefix(line, "powershell") && strings.Contains(line, "Get-Command"):
		return Result{ExitCode: 3}, nil // no Hyper-V tools: diskpart
	case line == "docker info":
		f.infoSeen++
		if f.infoOK > 0 && f.infoSeen >= f.infoOK {
			return Result{Out: "ok"}, nil
		}
		return Result{ExitCode: 1, Out: "error during connect"}, nil
	case strings.HasPrefix(line, "diskpart"):
		if strings.Contains(line, "compact") {
			f.sizes[vhdx] = 75 << 30
		}
		return Result{}, nil
	}
	return Result{}, nil
}

func (f *fakeHost) index(prefix string) int {
	for i, c := range f.calls {
		if strings.HasPrefix(c, prefix) {
			return i
		}
	}
	return -1
}

func (f *fakeHost) has(prefix string) bool { return f.index(prefix) >= 0 }

// The steps run in the order of the spec: trim, stop Docker, shut WSL down,
// compact, start Docker, wait. Sizes before and after are reported.
func TestWindowsCompactStepsInOrder(t *testing.T) {
	h := newWin()
	// the diskpart script run shrinks the file
	h.sizes[vhdx] = 236 << 30
	rep, err := dockercompact.Do(context.Background(), h)
	if err != nil {
		t.Fatalf("Do: %v\n%v", err, h.calls)
	}
	order := []string{"wsl -d docker-desktop -e fstrim", "docker desktop stop", "wsl --shutdown", "write ", "diskpart /s", "docker desktop start", "docker info"}
	last := -1
	for _, p := range order {
		i := h.index(p)
		if i < 0 || i <= last {
			t.Fatalf("step %q out of order or missing (at %d, previous %d): %v", p, i, last, h.calls)
		}
		last = i
	}
	script := h.calls[h.index("write ")]
	for _, want := range []string{`select vdisk file="` + vhdx + `"`, "attach vdisk readonly", "compact vdisk", "detach vdisk"} {
		if !strings.Contains(script, want) {
			t.Fatalf("script lacks %q: %s", want, script)
		}
	}
	if len(h.removed) != 1 {
		t.Fatalf("script file was not removed: %v", h.removed)
	}
	if rep.FileBefore() != 236<<30 || rep.FileAfter() != 75<<30 || !rep.DockerRunning || rep.UsedBefore != 76000000000 {
		t.Fatalf("report %+v", rep)
	}
	// RESULT line round-trips for the notices.
	back, ok := dockercompact.ParseReport(rep.Text())
	if !ok || back.FileAfter() != 75<<30 || back.Platform != "windows" {
		t.Fatalf("parse %+v %v", back, ok)
	}
}

func TestWindowsOptimizeVHDWhenHyperVToolsExist(t *testing.T) {
	h := newWin()
	h.fail["powershell -NoProfile -NonInteractive -Command if (Get-Command"] = Result{} // exit 0: tools exist
	if _, err := dockercompact.Do(context.Background(), h); err != nil {
		t.Fatal(err)
	}
	if !h.has("powershell -NoProfile -NonInteractive -Command Optimize-VHD -Path '"+vhdx+"' -Mode Full") || h.has("diskpart") {
		t.Fatalf("calls %v", h.calls)
	}
}

func TestWindowsWithoutAdminChangesNothing(t *testing.T) {
	h := newWin()
	h.admin = false
	_, err := dockercompact.Do(context.Background(), h)
	if !errors.Is(err, dockercompact.ErrNotElevated) {
		t.Fatalf("err %v", err)
	}
	if len(h.calls) != 0 {
		t.Fatalf("something ran: %v", h.calls)
	}
}

func TestWindowsNoDiskFileStopsNothing(t *testing.T) {
	h := newWin()
	h.sizes = map[string]int64{}
	if _, err := dockercompact.Do(context.Background(), h); err == nil || h.has("docker desktop stop") {
		t.Fatalf("err %v calls %v", err, h.calls)
	}
}

// A failed compact still starts Docker again and detaches the file.
func TestWindowsCompactFailureRestartsDocker(t *testing.T) {
	h := newWin()
	h.fail["diskpart /s C:\\Temp\\crsp-compact"] = Result{ExitCode: 1, Out: "DiskPart has encountered an error\r\n"}
	rep, err := dockercompact.Do(context.Background(), h)
	if err == nil || !strings.Contains(err.Error(), "compact") {
		t.Fatalf("err %v", err)
	}
	if !h.has("docker desktop start") || h.index("docker desktop start") < h.index("diskpart") {
		t.Fatalf("Docker not started again after the failure: %v", h.calls)
	}
	if !h.has("diskpart /s C:\\Temp\\crsp-detach") || !rep.DockerRunning || rep.Error == "" {
		t.Fatalf("detach or running missing: %v %+v", h.calls, rep)
	}
}

func TestWindowsShutdownFailureRestartsDockerAndSkipsCompact(t *testing.T) {
	h := newWin()
	h.fail["wsl --shutdown"] = Result{ExitCode: 1}
	_, err := dockercompact.Do(context.Background(), h)
	if err == nil || h.has("diskpart") || !h.has("docker desktop start") {
		t.Fatalf("err %v calls %v", err, h.calls)
	}
}

func TestWindowsStopFallsBackToKillingTheApp(t *testing.T) {
	h := newWin()
	h.fail["docker desktop stop"] = Result{ExitCode: 1, Out: "unknown command"}
	if _, err := dockercompact.Do(context.Background(), h); err != nil {
		t.Fatal(err)
	}
	if !h.has("taskkill /IM Docker Desktop.exe") {
		t.Fatalf("calls %v", h.calls)
	}
}

func TestWindowsStartFallsBackToTheApp(t *testing.T) {
	h := newWin()
	h.fail["docker desktop start"] = Result{ExitCode: 1}
	if _, err := dockercompact.Do(context.Background(), h); err != nil {
		t.Fatal(err)
	}
	if !h.has("cmd /c start") {
		t.Fatalf("calls %v", h.calls)
	}
}

func TestWindowsWaitsUntilDockerAnswers(t *testing.T) {
	h := newWin()
	h.infoOK = 4
	rep, err := dockercompact.Do(context.Background(), h)
	if err != nil || !rep.DockerRunning {
		t.Fatalf("err %v rep %+v", err, rep)
	}
	sleeps := 0
	for _, c := range h.calls {
		if c == "sleep" {
			sleeps++
		}
	}
	if sleeps < 3 {
		t.Fatalf("did not wait: %d sleeps", sleeps)
	}
}

func TestWindowsDockerNeverAnswersIsAClearError(t *testing.T) {
	h := newWin()
	h.infoOK = 0
	rep, err := dockercompact.Do(context.Background(), h)
	if err == nil || !strings.Contains(err.Error(), "docker info") || rep.DockerRunning {
		t.Fatalf("err %v rep %+v", err, rep)
	}
}

// A trim that fails only means less is reclaimed.
func TestWindowsTrimFailureDoesNotStopTheCompact(t *testing.T) {
	h := newWin()
	h.failErr["wsl -d docker-desktop -e fstrim"] = errors.New("distro not running")
	if _, err := dockercompact.Do(context.Background(), h); err != nil {
		t.Fatal(err)
	}
	if !h.has("diskpart") {
		t.Fatalf("calls %v", h.calls)
	}
}

func TestFindsDisksFromSettingsAndWSLRegistry(t *testing.T) {
	h := newWin()
	h.sizes = map[string]int64{`D:\Docker\DockerDesktopWSL\disk\docker_data.vhdx`: 10 << 30, `E:\wsl\ext4.vhdx`: 5 << 30}
	h.files[`C:\Users\ci\AppData\Roaming\Docker\settings-store.json`] = "\ufeff" + `{"CustomWslDistroDir":"D:\\Docker"}`
	h.fail["reg query"] = Result{Out: "HKEY_CURRENT_USER\\Software\\Microsoft\\Windows\\CurrentVersion\\Lxss\\{a}\r\n    DistributionName    REG_SZ    docker-desktop\r\n    BasePath    REG_SZ    \\\\?\\E:\\wsl\r\nHKEY_CURRENT_USER\\Software\\Microsoft\\Windows\\CurrentVersion\\Lxss\\{b}\r\n    DistributionName    REG_SZ    Ubuntu\r\n    BasePath    REG_SZ    \\\\?\\F:\\ubuntu\r\n"}
	in := dockercompact.Inspect(context.Background(), h)
	if len(in.Files) != 2 || !in.UsedKnown || !in.Elevated {
		t.Fatalf("inspection %+v", in)
	}
	if h.has("docker desktop stop") {
		t.Fatalf("inspect must not stop anything: %v", h.calls)
	}
	// 15 GiB of files, 76 GB (decimal) used: nothing to reclaim.
	if in.Reclaimable() != 0 {
		t.Fatalf("reclaimable %d", in.Reclaimable())
	}
}

func TestInspectReclaimable(t *testing.T) {
	h := newWin()
	in := dockercompact.Inspect(context.Background(), h)
	if want := int64(236<<30) - 76000000000; in.Reclaimable() != want {
		t.Fatalf("reclaimable %d want %d", in.Reclaimable(), want)
	}
}

// Linux and macOS: prune only, no disk-file step, no stop of Docker.
func TestUnixPrunesOnly(t *testing.T) {
	h := newWin()
	h.goos = "linux"
	rep, err := dockercompact.Do(context.Background(), h)
	if err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{"wsl", "diskpart", "docker desktop", "taskkill", "system prune", "volume prune"} {
		if h.has(bad) {
			t.Fatalf("%q ran: %v", bad, h.calls)
		}
	}
	if !h.has("docker builder prune --force") || !h.has("docker image prune --force") || !rep.DockerRunning {
		t.Fatalf("calls %v rep %+v", h.calls, rep)
	}
	for _, c := range h.calls {
		if strings.Contains(c, "prune") && (strings.Contains(c, "--all") || strings.Contains(c, " -a")) {
			t.Fatalf("prune removes more than unused: %s", c)
		}
	}
}

func TestUnixPruneFailureIsReported(t *testing.T) {
	h := newWin()
	h.goos = "darwin"
	h.fail["docker builder prune"] = Result{ExitCode: 1, Out: "daemon down"}
	_, err := dockercompact.Do(context.Background(), h)
	if err == nil || !strings.Contains(err.Error(), "builder prune") || !h.has("docker image prune") {
		t.Fatalf("err %v calls %v", err, h.calls)
	}
}

func TestParseDockerSize(t *testing.T) {
	for in, want := range map[string]int64{"1.5GB": 1500000000, "300MB": 300000000, "12kB": 12000, "0B": 0} {
		if got, ok := dockercompact.ParseDockerSize(in); !ok || got != want {
			t.Fatalf("%s: %d %v", in, got, ok)
		}
	}
	if _, ok := dockercompact.ParseDockerSize("lots"); ok {
		t.Fatal("garbage accepted")
	}
}

// Caches R5.58: when /dev/kvm was in the Docker VM before the compact, it must
// be there after the restart; the module is loaded again when it is not.
func TestWindowsCompactLoadsKVMAgainAfterTheRestart(t *testing.T) {
	h := newWin()
	h.infoOK = 1
	h.kvmSeq = []bool{true, false, true} // before: yes; after the restart: no; after modprobe: yes
	rep, err := dockercompact.Do(context.Background(), h)
	if err != nil {
		t.Fatalf("Do: %v\n%v", err, h.calls)
	}
	if !rep.KVMBefore || !rep.KVMAfter || !h.has("wsl -d docker-desktop -e sh -c modprobe kvm_intel || modprobe kvm_amd") {
		t.Fatalf("%+v\n%v", rep, h.calls)
	}
	if h.index("docker desktop start") > h.index("wsl -d docker-desktop -e sh -c modprobe") {
		t.Error("the module is loaded after Docker is back, never before")
	}
}

func TestWindowsCompactSaysWhenKVMDoesNotComeBack(t *testing.T) {
	h := newWin()
	h.infoOK = 1
	h.kvmSeq = []bool{true, false, false, false}
	rep, err := dockercompact.Do(context.Background(), h)
	if err == nil || !strings.Contains(err.Error(), "/dev/kvm is not back") || rep.KVMAfter {
		t.Fatalf("err %v rep %+v", err, rep)
	}
}

func TestWindowsCompactAsksNothingOfAMachineWithoutKVM(t *testing.T) {
	h := newWin()
	h.infoOK = 1
	h.kvmSeq = []bool{false}
	rep, err := dockercompact.Do(context.Background(), h)
	if err != nil || rep.KVMBefore || h.has("wsl -d docker-desktop -e sh -c modprobe") {
		t.Fatalf("err %v rep %+v\n%v", err, rep, h.calls)
	}
}
