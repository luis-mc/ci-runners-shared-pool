package load

import (
	"context"
	"os/exec"
	"runtime"
	"strings"
	"time"
)

// TopProcessText names the top 5 processes by CPU and by memory, names only,
// for hold and timeout lines. A failure to list processes never fails anything
// and gives an empty string (R5.46).
func TopProcessText() string {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var ps []Process
	switch runtime.GOOS {
	case "windows":
		out, err := exec.CommandContext(ctx, "powershell.exe", "-NoProfile", "-Command",
			"Get-Process | Select-Object ProcessName,CPU,WorkingSet64 | ConvertTo-Csv -NoTypeInformation").Output()
		if err != nil {
			return ""
		}
		ps = ParsePowerShellCSV(string(out))
	default:
		out, err := exec.CommandContext(ctx, "ps", "-eo", "comm=,pcpu=,pmem=").Output()
		if err != nil {
			return ""
		}
		ps = ParsePS(string(out))
	}
	cpu, mem := TopNames(ps, 5)
	if len(cpu) == 0 && len(mem) == 0 {
		return ""
	}
	return "top by CPU: " + strings.Join(cpu, ", ") + "; top by memory: " + strings.Join(mem, ", ")
}
