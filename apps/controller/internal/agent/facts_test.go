package agent_test

import (
	"context"
	"testing"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/agent"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/load"
)

// R5.10: the agent reports host name, OS name and version, kernel version, CPU
// model, core and thread count, architecture, total memory, total and free disk
// on the agent's data volume, and each GPU (vendor, model, memory).
func TestFactsLinux(t *testing.T) {
	p := &fakeSys{linuxBox()}
	p.cmds = []cmdRule{
		{[]string{"nvidia-smi"}, load.Result{Out: "NVIDIA GeForce RTX 4070, 12282\n"}, true},
	}
	f := agent.DetectFacts(context.Background(), p, "/var/lib/crsp", time.UnixMilli(1_700_000_000_000))
	if f.HostName != "box-1" || f.Os != "linux" || f.OsName != "Ubuntu" || f.OsVersion != "24.04" || f.KernelVersion != "6.8.0-generic" ||
		f.Arch != "x64" || f.CpuModel != "Test CPU @ 3GHz" || f.Cores != 2 || f.Threads != 4 ||
		f.MemoryBytes != 32000000*1024 || f.DiskTotalBytes != 500<<30 || f.DiskFreeBytes != 200<<30 || f.DetectedAtUnixMs != 1_700_000_000_000 {
		t.Fatalf("facts = %+v", f)
	}
	if len(f.Gpus) != 1 || f.Gpus[0].Vendor != "NVIDIA" || f.Gpus[0].Model != "NVIDIA GeForce RTX 4070" || f.Gpus[0].MemoryBytes != 12282<<20 {
		t.Fatalf("gpus = %+v", f.Gpus)
	}
}

func TestFactsLinuxGPUFromLspciAndNoGPU(t *testing.T) {
	p := &fakeSys{linuxBox()}
	p.cmds = []cmdRule{{[]string{"lspci"}, load.Result{Out: "00:02.0 VGA compatible controller: Intel Corporation UHD Graphics 630\n00:1f.3 Audio device: x\n"}, true}}
	f := agent.DetectFacts(context.Background(), p, "/", time.Now())
	if len(f.Gpus) != 1 || f.Gpus[0].Vendor != "Intel" {
		t.Fatalf("gpus = %+v", f.Gpus)
	}
	if f2 := agent.DetectFacts(context.Background(), &fakeSys{linuxBox()}, "/", time.Now()); len(f2.Gpus) != 0 {
		t.Fatalf("gpus = %+v", f2.Gpus)
	}
}

func TestFactsWindows(t *testing.T) {
	b := linuxBox()
	b.goos, b.files = "windows", map[string]string{}
	b.cmds = []cmdRule{
		{[]string{"powershell", "-NoProfile", "-Command", "$o=Get-CimInstance"}, load.Result{}, true}, // placeholder, replaced below
	}
	b.cmds = []cmdRule{{[]string{"powershell"}, load.Result{Out: "Microsoft Windows 11 Pro\n10.0.26100\n26100\nIntel(R) Core(TM) i7\n8\n16\n34359738368\n"}, true}}
	p := &fakeSys{b}
	f := agent.DetectFacts(context.Background(), p, `C:\crsp`, time.Now())
	if f.Os != "windows" || f.OsName != "Microsoft Windows 11 Pro" || f.OsVersion != "10.0.26100" || f.KernelVersion != "26100" ||
		f.CpuModel != "Intel(R) Core(TM) i7" || f.Cores != 8 || f.Threads != 16 || f.MemoryBytes != 34359738368 {
		t.Fatalf("facts = %+v", f)
	}
	gp := agent.ParseWindowsGPUs("NVIDIA|NVIDIA GeForce RTX 3080|10737418240\nMicrosoft|Basic Display|\n|\n")
	if len(gp) != 2 || gp[0].Vendor != "NVIDIA" || gp[0].MemoryBytes != 10737418240 {
		t.Fatalf("gpus = %+v", gp)
	}
}

func TestFactsMacOS(t *testing.T) {
	b := linuxBox()
	b.goos, b.arch, b.files = "darwin", "arm64", map[string]string{}
	b.cmds = []cmdRule{
		{[]string{"sw_vers", "-productName"}, load.Result{Out: "macOS\n"}, true},
		{[]string{"sw_vers", "-productVersion"}, load.Result{Out: "15.1\n"}, true},
		{[]string{"uname", "-r"}, load.Result{Out: "24.1.0\n"}, true},
		{[]string{"sysctl", "-n", "machdep.cpu.brand_string"}, load.Result{Out: "Apple M2 Pro\n"}, true},
		{[]string{"sysctl", "-n", "hw.physicalcpu"}, load.Result{Out: "10\n"}, true},
		{[]string{"sysctl", "-n", "hw.memsize"}, load.Result{Out: "17179869184\n"}, true},
		{[]string{"system_profiler"}, load.Result{Out: "Graphics/Displays:\n    Apple M2 Pro:\n      Chipset Model: Apple M2 Pro\n      Vendor: Apple (0x106b)\n"}, true},
	}
	f := agent.DetectFacts(context.Background(), &fakeSys{b}, "/", time.Now())
	if f.Os != "macos" || f.OsName != "macOS" || f.OsVersion != "15.1" || f.Arch != "arm64" || f.CpuModel != "Apple M2 Pro" ||
		f.Cores != 10 || f.MemoryBytes != 17179869184 || len(f.Gpus) != 1 || f.Gpus[0].Model != "Apple M2 Pro" {
		t.Fatalf("facts = %+v", f)
	}
}

func TestFactsParsers(t *testing.T) {
	if n, v := agent.ParseOSRelease("PRETTY_NAME=\"Debian GNU/Linux 12\"\nVERSION_ID=\"12\"\n"); n != "Debian GNU/Linux 12" || v != "12" {
		t.Fatalf("%q %q", n, v)
	}
	if got := agent.ParseMemTotal("MemTotal:  1024 kB\n"); got != 1<<20 {
		t.Fatalf("%d", got)
	}
	if m, c, th := agent.ParseCPUInfo("", 6); m != "" || c != 6 || th != 6 {
		t.Fatalf("%q %d %d", m, c, th)
	}
}
