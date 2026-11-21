package jobsample

import (
	"bytes"
	"strconv"
	"strings"
)

// PSTable lists the processes with `ps -axo pid=,ppid=,rss=,time=`, which macOS and the BSDs have and a Linux
// without /proc has too. It is the macOS reader of OPS66: proc_pid_rusage needs cgo, and the agent is built
// with CGO_ENABLED=0, so the table comes from ps, at the 5 s of TreeInterval (one ps start per 5 s).
// Run returns the output of that command.
type PSTable struct {
	Run func() ([]byte, error)
}

// PSArgs are the arguments of the ps command PSTable parses.
var PSArgs = []string{"-axo", "pid=,ppid=,rss=,time="}

// List implements ProcTable. CPU ticks are centiseconds, so use TicksPerSecond 100.
func (p PSTable) List() ([]Proc, error) {
	out, err := p.Run()
	if err != nil {
		return nil, err
	}
	return parsePSTable(out), nil
}

func parsePSTable(out []byte) []Proc {
	var res []Proc
	for _, ln := range bytes.Split(out, []byte("\n")) {
		f := strings.Fields(string(ln))
		if len(f) < 4 {
			continue
		}
		pid, e1 := strconv.Atoi(f[0])
		ppid, e2 := strconv.Atoi(f[1])
		rss, e3 := strconv.ParseInt(f[2], 10, 64)
		cs, ok := parsePSTime(f[3])
		if e1 != nil || e2 != nil || e3 != nil || !ok {
			continue
		}
		res = append(res, Proc{PID: pid, PPID: ppid, CPUTicks: cs, RSSBytes: rss * 1024})
	}
	return res
}

// parsePSTime reads [[dd-]hh:]mm:ss[.cc] into centiseconds.
func parsePSTime(s string) (int64, bool) {
	var days int64
	if d, rest, ok := strings.Cut(s, "-"); ok {
		n, err := strconv.ParseInt(d, 10, 64)
		if err != nil {
			return 0, false
		}
		days, s = n, rest
	}
	sec, frac, _ := strings.Cut(s, ".")
	parts := strings.Split(sec, ":")
	if len(parts) < 2 || len(parts) > 3 {
		return 0, false
	}
	var total int64
	for _, p := range parts {
		n, err := strconv.ParseInt(p, 10, 64)
		if err != nil {
			return 0, false
		}
		total = total*60 + n
	}
	total += days * 86400
	cs := total * 100
	if frac != "" {
		f, err := strconv.ParseInt((frac + "00")[:2], 10, 64)
		if err != nil {
			return 0, false
		}
		cs += f
	}
	return cs, true
}
