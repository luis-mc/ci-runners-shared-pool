package jobcmd

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/doctor"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/doctor/checks"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/runnercache"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/vmcheck"
)

// Settings of `crsp vm doctor`.
const (
	// VMPackagesVar lists the packages the runners need in the base disk,
	// separated by commas or spaces.
	VMPackagesVar = "CRSP_VM_PACKAGES"
	// VMBridgeVar names the network bridge the VMs may use.
	VMBridgeVar = "CRSP_VM_BRIDGE"
	// VMFloorVar is the free disk floor in GiB.
	VMFloorVar = "CRSP_VM_FLOOR_GIB"
	// DefaultVMFloorGiB is the free disk floor of vm.disk when nothing sets it.
	DefaultVMFloorGiB = 20
)

const vmDoctorUsage = "usage: crsp vm doctor [--distro NAME]... [--need PACKAGE]... [--floor-gib N] [--bridge NAME] [--json] [--cache DIR]"

// VMTools are the programs `crsp vm` starts; the seed tool is checked on its own.
var vmTools = []string{"qemu-system-x86_64", "qemu-img", "ssh", "scp", "ssh-keygen"}

// vmSeedTool builds the cloud-init seed disk of `crsp vm run`.
const vmSeedTool = "cloud-localds"

var bridgeName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9 ._-]{0,63}$`)

// VMHost is what `crsp vm doctor` asks of the machine. Tests replace it with fakes.
type VMHost struct {
	GOOS      string
	Hostname  func() string
	KVMUsable func() bool
	LookPath  func(name string) error
	// Output runs a short command and returns its output; built on exec.CommandContext.
	Output func(ctx context.Context, name string, args ...string) (string, error)
	// FreeBytes is the free space of the volume that holds path.
	FreeBytes func(path string) (int64, error)
	// BridgeExists says whether a network bridge of that name is on this Linux host.
	BridgeExists func(name string) bool
	// HashFile is the hex digest of a file with the algorithm named.
	HashFile func(path, algo string) (string, error)
}

// RealVMHost reads this machine.
func RealVMHost() VMHost {
	return VMHost{
		GOOS: runtime.GOOS,
		Hostname: func() string {
			h, err := os.Hostname()
			if err != nil || h == "" {
				return "this machine"
			}
			return h
		},
		KVMUsable: vmcheck.RealDeps().KVMUsable,
		LookPath: func(name string) error {
			_, err := exec.LookPath(name)
			return err
		},
		Output: func(ctx context.Context, name string, args ...string) (string, error) {
			out, err := exec.CommandContext(ctx, name, args...).Output() //nolint:gosec // fixed program names
			return string(out), err
		},
		FreeBytes: func(path string) (int64, error) {
			d, err := runnercache.OSDisk(path)
			return d.Free, err
		},
		BridgeExists: func(name string) bool {
			st, err := os.Stat(filepath.Join("/sys/class/net", name, "bridge"))
			return err == nil && st.IsDir()
		},
		HashFile: func(path, algo string) (string, error) {
			h, err := vmcheck.NewHash(algo)
			if err != nil {
				return "", err
			}
			f, err := os.Open(path) //nolint:gosec // a file of the VM cache
			if err != nil {
				return "", err
			}
			defer func() { _ = f.Close() }()
			if _, err := io.Copy(h, f); err != nil {
				return "", err
			}
			return hex.EncodeToString(h.Sum(nil)), nil
		},
	}
}

// vmDoctorOpts are what the flags and the environment say.
type vmDoctorOpts struct {
	cache    string
	distros  []string
	need     []string
	floor    int64
	bridge   string
	asJSON   bool
	catalogs vmcheck.Catalogue
}

// vmSource is the doctor's VMSource for this machine.
type vmSource struct {
	host VMHost
	opt  vmDoctorOpts
}

// VM reads the VM backend of this machine (doctor.VMSource).
func (s vmSource) VM(ctx context.Context) (doctor.VMState, error) {
	h, o := s.host, s.opt
	st := doctor.VMState{
		Machine:    doctor.Machine{ID: "local", Name: h.Hostname(), OS: h.GOOS},
		CacheDir:   o.cache,
		FloorBytes: o.floor,
		Needed:     vmcheck.CleanPackages(o.need),
	}
	s.accel(ctx, &st)
	for _, t := range vmTools {
		st.Tools = append(st.Tools, doctor.VMTool{Name: t, Found: h.LookPath(t) == nil})
	}
	st.SeedTool = doctor.VMTool{Name: vmSeedTool, Found: h.LookPath(vmSeedTool) == nil}
	if free, err := h.FreeBytes(existingParent(o.cache)); err == nil {
		st.FreeKnown, st.FreeBytes = true, free
	}
	s.bridge(ctx, &st)
	disks, err := s.disks()
	if err != nil {
		return st, err
	}
	st.Disks = disks
	return st, nil
}

// existingParent walks up from p to the first folder that exists, so the free
// space of a cache that is not made yet is the free space of its disk.
func existingParent(p string) string {
	for {
		if _, err := os.Stat(p); err == nil {
			return p
		}
		up := filepath.Dir(p)
		if up == p {
			return p
		}
		p = up
	}
}

func (s vmSource) accel(ctx context.Context, st *doctor.VMState) {
	h := s.host
	switch h.GOOS {
	case "linux":
		st.Accelerator, st.AccelKnown = "KVM", true
		if st.AccelOK = h.KVMUsable(); !st.AccelOK {
			st.AccelDetail = "/dev/kvm is missing or the runner user cannot open it for read and write."
		}
	case "windows":
		st.Accelerator = "Hyper-V"
		out, err := h.Output(ctx, "powershell", "-NoProfile", "-NonInteractive", "-Command", "(Get-WindowsOptionalFeature -Online -FeatureName Microsoft-Hyper-V-All).State")
		if err != nil {
			st.AccelDetail = "PowerShell did not answer (it needs an administrator): " + firstWord(err.Error())
			return
		}
		st.AccelKnown = true
		if st.AccelOK = strings.EqualFold(strings.TrimSpace(out), "Enabled"); !st.AccelOK {
			st.AccelDetail = "The Hyper-V Windows feature is " + strings.TrimSpace(out) + "."
		}
	case "darwin":
		st.Accelerator = "Virtualization.framework"
		out, err := h.Output(ctx, "sysctl", "-n", "kern.hv_support")
		if err != nil {
			st.AccelDetail = "sysctl did not answer: " + firstWord(err.Error())
			return
		}
		st.AccelKnown = true
		if st.AccelOK = strings.TrimSpace(out) == "1"; !st.AccelOK {
			st.AccelDetail = "kern.hv_support is not 1 on this Mac."
		}
	default:
		st.AccelDetail = "No VM backend is built for " + h.GOOS + "."
	}
}

func firstWord(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

func (s vmSource) bridge(ctx context.Context, st *doctor.VMState) {
	h, name := s.host, s.opt.bridge
	switch h.GOOS {
	case "linux":
		if name == "" {
			name = "virbr0"
		}
		if !bridgeName.MatchString(name) {
			return
		}
		st.Bridge, st.BridgeKnown, st.BridgePresent = name, true, h.BridgeExists(name)
	case "windows":
		if name == "" {
			name = "Default Switch"
		}
		if !bridgeName.MatchString(name) {
			return
		}
		st.Bridge = name
		// The name passes bridgeName, so it holds no quote or shell character.
		out, err := h.Output(ctx, "powershell", "-NoProfile", "-NonInteractive", "-Command", "(Get-VMSwitch -Name '"+name+"' -ErrorAction Stop).Name")
		if err != nil {
			var ee *exec.ExitError
			// A switch that does not exist ends the command with an error;
			// PowerShell that cannot run at all leaves the answer unknown.
			st.BridgeKnown = errors.As(err, &ee)
			return
		}
		st.BridgeKnown, st.BridgePresent = true, strings.EqualFold(strings.TrimSpace(out), name)
	}
}

// disks reads what the cache holds of the distros it has an image of and of the
// distros asked for with --distro.
func (s vmSource) disks() ([]doctor.VMDisk, error) {
	o := s.opt
	files, err := vmcheck.Scan(o.cache)
	if err != nil {
		return nil, err
	}
	want := map[string]bool{}
	for _, d := range o.distros {
		want[d] = true
	}
	imageRe := regexp.MustCompile(`^(.+)-([0-9a-f]{16})\.qcow2$`)
	newest := map[string]vmcheck.CacheFile{}
	for _, cf := range files["images"] {
		m := imageRe.FindStringSubmatch(filepath.Base(cf.Path))
		if m == nil {
			continue
		}
		if _, ok := o.catalogs[m[1]]; !ok {
			continue
		}
		want[m[1]] = true
		if b, ok := newest[m[1]]; !ok || cf.ModUnix > b.ModUnix || (cf.ModUnix == b.ModUnix && cf.Path > b.Path) {
			newest[m[1]] = cf
		}
	}
	names := make([]string, 0, len(want))
	for n := range want {
		names = append(names, n)
	}
	sort.Strings(names)
	var out []doctor.VMDisk
	for _, n := range names {
		d := doctor.VMDisk{Distro: n}
		img, ok := newest[n]
		if !ok {
			out = append(out, d)
			continue
		}
		d.ImagePresent = true
		prefix := imageRe.FindStringSubmatch(filepath.Base(img.Path))[2]
		sum, err := s.host.HashFile(img.Path, o.catalogs[n].Algo)
		if err == nil {
			d.ImageChecked, d.ImageOK = true, strings.HasPrefix(sum, prefix)
		}
		if d.ImageChecked && d.ImageOK {
			d.BasePresent, d.Packages = s.findBase(files["bases"], n, sum)
		}
		out = append(out, d)
	}
	return out, nil
}

// findBase picks the base disk made for this image: its name holds BaseKey of
// the image digest and the package list its manifest has.
func (s vmSource) findBase(bases []vmcheck.CacheFile, distro, digest string) (bool, []string) {
	for _, cf := range bases {
		name := filepath.Base(cf.Path)
		if !strings.HasPrefix(name, distro+"-") || !strings.HasSuffix(name, ".qcow2") {
			continue
		}
		pkgs, err := vmcheck.ReadManifest(cf.Path)
		if err != nil {
			continue
		}
		if cf.Path == vmcheck.BaseFile(s.opt.cache, distro, vmcheck.BaseKey(digest, pkgs)) {
			return true, pkgs
		}
	}
	return false, nil
}

func splitList(s string) []string {
	return strings.FieldsFunc(s, func(r rune) bool { return r == ',' || r == ' ' || r == '\n' || r == '\t' })
}

func parseVMDoctor(args []string, cat vmcheck.Catalogue, env Env) (vmDoctorOpts, error) {
	fs := flag.NewFlagSet("vm doctor", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	var o vmDoctorOpts
	var cache string
	var distros, need stringList
	floor := int64(-1)
	fs.StringVar(&cache, "cache", "", "")
	fs.Var(&distros, "distro", "")
	fs.Var(&need, "need", "")
	fs.Int64Var(&floor, "floor-gib", -1, "")
	fs.StringVar(&o.bridge, "bridge", "", "")
	fs.BoolVar(&o.asJSON, "json", false, "")
	if err := fs.Parse(args); err != nil {
		return o, err
	}
	if fs.NArg() > 0 {
		return o, fmt.Errorf("unexpected argument %q", fs.Arg(0))
	}
	for _, d := range distros {
		if _, err := cat.Get(d); err != nil {
			return o, err
		}
	}
	o.cache, o.distros, o.catalogs = cacheDir(cache, env), distros, cat
	o.need = append(splitList(env.Getenv(VMPackagesVar)), need...)
	if o.bridge == "" {
		o.bridge = env.Getenv(VMBridgeVar)
	}
	if floor < 0 {
		floor = DefaultVMFloorGiB
		if v := env.Getenv(VMFloorVar); v != "" {
			n, err := strconv.ParseInt(v, 10, 64)
			if err != nil || n < 0 {
				return o, fmt.Errorf("%s=%q is not a number of GiB", VMFloorVar, v)
			}
			floor = n
		}
	}
	o.floor = floor * gib
	return o, nil
}

// vmDoctor is `crsp vm doctor` (caches R5.46): it checks the VM backend of this
// machine with the doctor's engine and prints one line for each finding, with
// the fix under each one that is not ok. Exit 1 when a check fails, 2 for a
// wrong argument, else 0.
func vmDoctor(args []string, cat vmcheck.Catalogue, env Env) int {
	o, err := parseVMDoctor(args, cat, env)
	if err != nil {
		_, _ = fmt.Fprintf(env.Stderr, "crsp vm: %v\n%s\n", err, vmDoctorUsage)
		return ExitVMUsage
	}
	host := RealVMHost()
	if env.VMHost != nil {
		host = *env.VMHost
	}
	reg := doctor.NewRegistry()
	checks.RegisterVM(reg)
	eng := &doctor.Engine{
		Registry: reg, Store: doctor.NewMemStore(), Now: time.Now,
		Env: func(context.Context) (doctor.Env, error) {
			return doctor.Env{VM: vmSource{host: host, opt: o}}, nil
		},
	}
	run, err := eng.Run(context.Background(), doctor.Scope{}, doctor.TriggerCLI, "crsp vm doctor")
	if err != nil {
		_, _ = fmt.Fprintln(env.Stderr, "crsp vm:", err)
		return ExitVMFail
	}
	worst := doctor.Worst()
	for _, f := range run.Findings {
		worst = doctor.Worst(worst, f.Severity)
	}
	if o.asJSON {
		enc := json.NewEncoder(env.Stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(run.Findings); err != nil {
			_, _ = fmt.Fprintln(env.Stderr, "crsp vm:", err)
			return ExitVMFail
		}
	} else {
		printVMFindings(env.Stdout, run.Findings)
	}
	if worst == doctor.Fail {
		return ExitVMFail
	}
	return 0
}

func printVMFindings(w io.Writer, fs []doctor.Finding) {
	for _, f := range fs {
		_, _ = fmt.Fprintf(w, "%-4s %s: %s\n", strings.ToUpper(string(f.Severity)), f.Check, f.Title)
		if f.Severity == doctor.OK || f.Severity == doctor.Info {
			continue
		}
		if f.Detail != "" {
			_, _ = fmt.Fprintf(w, "     why: %s\n", f.Detail)
		}
		_, _ = fmt.Fprintf(w, "     fix: %s\n", f.Fix.Text)
		if f.Fix.Command != "" {
			_, _ = fmt.Fprintf(w, "          %s\n", f.Fix.Command)
		}
	}
}
