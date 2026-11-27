package metrics

import (
	"bytes"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"
)

// Metric names. The OTLP push uses the same names and the same values.
const (
	NameRunners       = metricPrefix + "runners"
	NameQueueWait     = metricPrefix + "queue_wait_seconds"
	NameJobDuration   = metricPrefix + "job_duration_seconds"
	NameFallback      = metricPrefix + "fallback_jobs_total"
	NameCacheHitRate  = metricPrefix + "cache_hit_ratio"
	NameCacheLookups  = metricPrefix + "cache_lookups_total"
	NameMachineUptime = metricPrefix + "machine_uptime_seconds"
	NameMachineUp     = metricPrefix + "machine_up"
)

func escLabel(s string) string {
	return strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\n", `\n`).Replace(s)
}

func num(f float64) string { return strconv.FormatFloat(f, 'g', -1, 64) }

func writeHist(w *bytes.Buffer, name, help string, h Hist) {
	fmt.Fprintf(w, "# HELP %s %s\n# TYPE %s histogram\n", name, help, name)
	for i, b := range h.Bounds {
		fmt.Fprintf(w, "%s_bucket{le=\"%s\"} %d\n", name, num(b), h.Counts[i])
	}
	fmt.Fprintf(w, "%s_bucket{le=\"+Inf\"} %d\n%s_sum %s\n%s_count %d\n", name, h.Count, name, num(h.Sum), name, h.Count)
}

// WritePrometheus writes s in the Prometheus text format (version 0.0.4).
func WritePrometheus(out io.Writer, s Snapshot) {
	var w bytes.Buffer
	render(&w, s)
	_, _ = out.Write(w.Bytes())
}

func render(w *bytes.Buffer, s Snapshot) {
	fmt.Fprintf(w, "# HELP %s Runners by state.\n# TYPE %s gauge\n", NameRunners, NameRunners)
	states := make([]string, 0, len(s.RunnersByState))
	for k := range s.RunnersByState {
		states = append(states, k)
	}
	sort.Strings(states)
	for _, k := range states {
		fmt.Fprintf(w, "%s{state=\"%s\"} %d\n", NameRunners, escLabel(k), s.RunnersByState[k])
	}
	writeHist(w, NameQueueWait, "Seconds recent jobs waited in the queue.", NewHist(s.QueueWaitSeconds))
	writeHist(w, NameJobDuration, "Seconds recent jobs ran.", NewHist(s.JobDurationSeconds))
	fmt.Fprintf(w, "# HELP %s Jobs sent to the hosted fallback since the controller started.\n# TYPE %s counter\n%s %d\n",
		NameFallback, NameFallback, NameFallback, s.FallbackTotal)
	fmt.Fprintf(w, "# HELP %s Cache lookups since the controller started, by result.\n# TYPE %s counter\n%s{result=\"hit\"} %d\n%s{result=\"miss\"} %d\n",
		NameCacheLookups, NameCacheLookups, NameCacheLookups, s.CacheHits, NameCacheLookups, s.CacheMisses)
	fmt.Fprintf(w, "# HELP %s Cache hits over lookups, 0 to 1.\n# TYPE %s gauge\n%s %s\n", NameCacheHitRate, NameCacheHitRate, NameCacheHitRate, num(s.CacheHitRate()))
	ms := append([]MachineUptime(nil), s.Machines...)
	sort.Slice(ms, func(i, j int) bool { return ms[i].Name < ms[j].Name })
	fmt.Fprintf(w, "# HELP %s Seconds the machine has been up.\n# TYPE %s gauge\n", NameMachineUptime, NameMachineUptime)
	for _, m := range ms {
		fmt.Fprintf(w, "%s{machine=\"%s\",os=\"%s\"} %s\n", NameMachineUptime, escLabel(m.Name), escLabel(m.OS), num(m.UptimeSeconds))
	}
	fmt.Fprintf(w, "# HELP %s 1 when the machine is online.\n# TYPE %s gauge\n", NameMachineUp, NameMachineUp)
	for _, m := range ms {
		v := 0
		if m.Online {
			v = 1
		}
		fmt.Fprintf(w, "%s{machine=\"%s\",os=\"%s\"} %d\n", NameMachineUp, escLabel(m.Name), escLabel(m.OS), v)
	}
}
