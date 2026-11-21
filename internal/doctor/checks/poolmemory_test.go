package checks

import (
	"strings"
	"testing"

	"github.com/luis-mc/ci-runners-shared-pool/internal/doctor"
)

// machines R5.68, OPS61: the finding when the configured pool or the WSL cap
// is above total RAM less the host p95, with a link to the machine page.
func TestPoolMemoryFinding(t *testing.T) {
	mk := func(id string, p doctor.PoolMemory) doctor.Machine {
		m := healthyMachine()
		m.ID, m.Name, m.PoolMemory = id, id, p
		return m
	}
	const total = 32 * gib
	fl := &fleet{machines: []doctor.Machine{
		mk("pool-over", doctor.PoolMemory{TotalBytes: total, PoolGiB: 24, HostP95Bytes: 10 * gib, HostReady: true}),
		mk("cap-over", doctor.PoolMemory{TotalBytes: total, PoolGiB: 8, WSLCapBytes: 26 * gib, HostP95Bytes: 10 * gib, HostReady: true}),
		mk("both-over", doctor.PoolMemory{TotalBytes: total, PoolGiB: 24, WSLCapBytes: 28 * gib, HostP95Bytes: 10 * gib, HostReady: true}),
		mk("fits", doctor.PoolMemory{TotalBytes: total, PoolGiB: 16, WSLCapBytes: 20 * gib, HostP95Bytes: 10 * gib, HostReady: true}),
		mk("measuring", doctor.PoolMemory{TotalBytes: total, PoolGiB: 30, HostP95Bytes: 10 * gib}),
		mk("nothing-set", doctor.PoolMemory{TotalBytes: total, HostP95Bytes: 10 * gib, HostReady: true}),
		mk("no-facts", doctor.PoolMemory{PoolGiB: 30, HostP95Bytes: 10 * gib, HostReady: true}),
	}}
	got := map[string]doctor.Finding{}
	for _, f := range run(t, doctor.Env{Fleet: fl}, "machine.pool.memory") {
		got[f.Subject.Name] = f
	}
	if len(got) != 4 {
		t.Fatalf("only machines with something to say get a finding: %+v", got)
	}
	for name, want := range map[string]string{"pool-over": "the pool is 24.0 GiB", "cap-over": "cap in .wslconfig is 26.0 GiB", "both-over": "the pool is 24.0 GiB and the WSL memory cap"} {
		f := got[name]
		if f.Severity != doctor.Warn || !strings.Contains(f.Detail, want) || !strings.Contains(f.Detail, "up to 10.0 GiB (p95, 7 days)") ||
			!strings.Contains(f.Detail, "22.0 GiB") || f.Fix.Link != "/machines/"+name || !strings.Contains(f.Detail, "WSL restarts") {
			t.Fatalf("%s: %+v", name, f)
		}
	}
	if got["fits"].Severity != doctor.OK {
		t.Fatalf("fits: %+v", got["fits"])
	}
}

func TestPoolMemoryOfflineMachineIsSkipped(t *testing.T) {
	m := healthyMachine()
	m.Online = false
	m.PoolMemory = doctor.PoolMemory{TotalBytes: 32 * gib, PoolGiB: 30, HostP95Bytes: 10 * gib, HostReady: true}
	fs := run(t, doctor.Env{Fleet: &fleet{machines: []doctor.Machine{m}}}, "machine.pool.memory")
	if len(fs) != 1 || fs[0].Severity != doctor.Skip {
		t.Fatalf("%+v", fs)
	}
}
