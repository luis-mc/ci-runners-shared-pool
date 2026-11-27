package health

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	agentv1 "github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/gen/agent/v1"
)

// The Windows collectors. PowerShell answers in one JSON line each, so a
// parser needs no locale.

func ps(env Env, script string, timeout time.Duration) (string, bool) {
	r, ok := env.RunTimeout([]string{"powershell", "-NoProfile", "-NonInteractive", "-Command", script}, timeout)
	return strings.TrimSpace(r.Out), ok && r.Code == 0
}

func windowsBoot(env Env) *agentv1.BootHealth {
	up, err := env.TickCount()
	if err != nil {
		return &agentv1.BootHealth{UnknownReason: "GetTickCount64 failed: " + err.Error()}
	}
	return bootFromUptime(env.Now(), up)
}

const swapScript = `$p=@(Get-CimInstance Win32_PageFileUsage); [pscustomobject]@{n=$p.Count;total=($p|Measure-Object AllocatedBaseSize -Sum).Sum;used=($p|Measure-Object CurrentUsage -Sum).Sum}|ConvertTo-Json -Compress`

func windowsSwap(env Env) *agentv1.SwapHealth {
	out, ok := ps(env, swapScript, 20*time.Second)
	if !ok {
		return &agentv1.SwapHealth{UnknownReason: "the page file query failed"}
	}
	var v struct {
		N           int
		Total, Used float64
	}
	if json.Unmarshal([]byte(out), &v) != nil {
		return &agentv1.SwapHealth{UnknownReason: "the page file query gave no JSON"}
	}
	if v.N == 0 {
		return &agentv1.SwapHealth{UnknownReason: "this machine has no page file"}
	}
	return &agentv1.SwapHealth{Known: true, TotalBytes: int64(v.Total) << 20, UsedBytes: int64(v.Used) << 20}
}

const oomScript = `$e=@(Get-WinEvent -FilterHashtable @{LogName='System';Id=2004;StartTime=(Get-Date).AddHours(-24)} -ErrorAction SilentlyContinue); [pscustomobject]@{n=$e.Count;last=$(if($e.Count){[DateTimeOffset]::new($e[0].TimeCreated).ToUnixTimeMilliseconds()}else{0})}|ConvertTo-Json -Compress`

// windowsOOM counts Resource-Exhaustion-Detector events (id 2004, Windows ran
// low on memory and closed programs) of the last 24 hours.
func windowsOOM(env Env) *agentv1.OomHealth {
	out, ok := ps(env, oomScript, 30*time.Second)
	var v struct{ N, Last int64 }
	if !ok || json.Unmarshal([]byte(out), &v) != nil {
		return &agentv1.OomHealth{UnknownReason: "the Windows event log cannot be read"}
	}
	return &agentv1.OomHealth{Known: true, TotalKills: v.N, LastKillUnixMs: v.Last}
}

const winUpdateScript = `$u='HKLM:\SOFTWARE\Microsoft\Windows\CurrentVersion\WindowsUpdate';` +
	`$c=$null;try{$c=(New-Object -ComObject Microsoft.Update.Session).CreateUpdateSearcher().Search('IsInstalled=0 and IsHidden=0').Updates.Count}catch{};` +
	`$ux=Get-ItemProperty "HKLM:\SOFTWARE\Microsoft\WindowsUpdate\UX\Settings" -EA SilentlyContinue;` +
	`$au=Get-ItemProperty "HKLM:\SOFTWARE\Policies\Microsoft\Windows\WindowsUpdate\AU" -EA SilentlyContinue;` +
	`[pscustomobject]@{rr=(Test-Path "$u\Auto Update\RebootRequired");cbs=(Test-Path "HKLM:\SOFTWARE\Microsoft\Windows\CurrentVersion\Component Based Servicing\RebootPending");` +
	`pfro=($null -ne (Get-ItemProperty "HKLM:\SYSTEM\CurrentControlSet\Control\Session Manager" -Name PendingFileRenameOperations -EA SilentlyContinue).PendingFileRenameOperations);` +
	`count=$c;start=$ux.ActiveHoursStart;end=$ux.ActiveHoursEnd;noauto=$au.NoAutoRebootWithLoggedOnUsers}|ConvertTo-Json -Compress`

// windowsUpdates reads the restart flags, the pending update count and the active hours.
func windowsUpdates(env Env, timeout time.Duration) *agentv1.UpdateHealth {
	out, ok := ps(env, winUpdateScript, timeout)
	if !ok {
		return &agentv1.UpdateHealth{UnknownReason: "the Windows Update query did not finish in " + timeout.String()}
	}
	return parseWindowsUpdates(out, env.Now())
}

func parseWindowsUpdates(out string, now time.Time) *agentv1.UpdateHealth {
	var v struct {
		RR, CBS, PFRO bool
		Count         *int32
		Start, End    *int32
		NoAuto        *int32
	}
	if json.Unmarshal([]byte(out), &v) != nil {
		return &agentv1.UpdateHealth{UnknownReason: "the Windows Update query gave no JSON"}
	}
	u := &agentv1.UpdateHealth{Known: true}
	for _, r := range []struct {
		on   bool
		text string
	}{{v.RR, "Windows Update asks for a restart (RebootRequired)"}, {v.CBS, "a component update waits for a restart (RebootPending)"}, {v.PFRO, "files wait to be renamed at the next start (PendingFileRenameOperations)"}} {
		if r.on {
			u.RestartPending = true
			u.RestartReasons = append(u.RestartReasons, r.text)
		}
	}
	if v.Count != nil {
		u.UpdateCountKnown, u.PendingUpdateCount = true, *v.Count
	}
	inActive := false
	if v.Start != nil && v.End != nil {
		u.ActiveHoursKnown, u.ActiveHoursStart, u.ActiveHoursEnd = true, *v.Start, *v.End
		inActive = hourInRange(int32(now.Hour()), *v.Start, *v.End) //nolint:gosec // 0 to 23
	}
	// Windows restarts by itself outside the active hours, unless a policy
	// keeps it from restarting while someone is logged on.
	u.AutoRestartPossible = v.NoAuto == nil || *v.NoAuto == 0
	u.RestartAllowedNow = u.AutoRestartPossible && !inActive
	if u.AutoRestartPossible {
		u.AutoUpdateState = "Windows Update restarts outside the active hours"
	} else {
		u.AutoUpdateState = "a policy keeps Windows Update from restarting"
	}
	return u
}

// hourInRange says whether hour h is in [start, end), where the range may wrap past midnight.
func hourInRange(h, start, end int32) bool {
	if start <= end {
		return h >= start && h < end
	}
	return h >= start || h < end
}

var (
	acRe   = regexp.MustCompile(`(?i)Current AC Power Setting Index:\s*0x([0-9a-f]+)`)
	dcRe   = regexp.MustCompile(`(?i)Current DC Power Setting Index:\s*0x([0-9a-f]+)`)
	guidRe = regexp.MustCompile(`(?i)Power Setting GUID:\s*([0-9a-f-]{36})`)
)

const (
	guidStandbyIdle   = "29f6c1db-86da-48c5-9fdb-f2b67b1f44da"
	guidHibernateIdle = "9d7815a6-7ee4-497e-8888-515a05f02364"
)

// parsePowercfg reads `powercfg /q SCHEME_CURRENT SUB_SLEEP`: the idle times
// (seconds, 0 is never) of sleep and hibernate on AC and on battery.
func parsePowercfg(out string) map[string][2]int64 {
	res := map[string][2]int64{}
	cur := ""
	for _, l := range strings.Split(out, "\n") {
		if m := guidRe.FindStringSubmatch(l); m != nil {
			cur = strings.ToLower(m[1])
			continue
		}
		if cur == "" {
			continue
		}
		v := res[cur]
		if m := acRe.FindStringSubmatch(l); m != nil {
			v[0], _ = strconv.ParseInt(m[1], 16, 64)
		}
		if m := dcRe.FindStringSubmatch(l); m != nil {
			v[1], _ = strconv.ParseInt(m[1], 16, 64)
		}
		res[cur] = v
	}
	return res
}

const powerScript = `$b=@(Get-CimInstance Win32_Battery -EA SilentlyContinue);` +
	`$w=Get-WinEvent -FilterHashtable @{LogName='System';ProviderName='Microsoft-Windows-Power-Troubleshooter';Id=1} -MaxEvents 1 -EA SilentlyContinue;` +
	`[pscustomobject]@{battery=$b.Count;status=$(if($b.Count){$b[0].BatteryStatus}else{0});wake=$(if($w){[DateTimeOffset]::new($w.TimeCreated).ToUnixTimeMilliseconds()}else{0})}|ConvertTo-Json -Compress`

func windowsPower(env Env) *agentv1.PowerHealth {
	p := &agentv1.PowerHealth{}
	var v struct{ Battery, Status, Wake int64 }
	if out, ok := ps(env, powerScript, 30*time.Second); ok && json.Unmarshal([]byte(out), &v) == nil {
		// BatteryStatus 1 is discharging, so the PC runs on battery.
		p.SourceKnown, p.OnBattery = true, v.Battery > 0 && v.Status == 1
		p.WakeKnown, p.LastWakeUnixMs = v.Wake > 0, v.Wake
	} else {
		p.SourceUnknownReason = "the battery query failed"
	}
	if out, ok := run(env, "powercfg", "/q", "SCHEME_CURRENT", "SUB_SLEEP"); ok {
		p.SleepKnown = true
		idle := parsePowercfg(out)
		idx := 0
		if p.OnBattery {
			idx = 1
		}
		for _, s := range []struct{ guid, name string }{{guidStandbyIdle, "sleep"}, {guidHibernateIdle, "hibernate"}} {
			if t := idle[s.guid][idx]; t > 0 {
				p.SleepEnabled = true
				p.SleepModes = append(p.SleepModes, fmt.Sprintf("%s after %d min", s.name, t/60))
			}
		}
	} else {
		p.SleepUnknownReason = "powercfg /q failed"
	}
	return p
}

// windowsWSLVersion reads `wsl --version`, which prints UTF-16.
func windowsWSLVersion(env Env) string {
	r, ok := env.RunTimeout([]string{"wsl", "--version"}, 10*time.Second)
	if !ok || r.Code != 0 {
		return ""
	}
	txt := strings.ReplaceAll(r.Out, "\x00", "")
	for _, l := range strings.Split(txt, "\n") {
		if k, val, ok := strings.Cut(l, ":"); ok && strings.Contains(strings.ToLower(k), "wsl") && strings.Contains(strings.ToLower(k), "version") {
			return strings.TrimSpace(val)
		}
	}
	return ""
}
