package load

import "testing"

const (
	dockerPS = "pool-main-runner\tyes\npool-main-db\tyes\nother-box\t\n"
	stats    = "pool-main-runner\t2GiB / 12GiB\npool-main-db\t512MiB / 12GiB\nother-box\t1.5GiB / 12GiB\n"
)

// R5.18 ports test_dl177_pool_measure.py DockerReadTests.
func TestContainerUseThroughWindowsCLI(t *testing.T) {
	p := winProbe(
		fakeCmd{[]string{"docker", "ps"}, Result{Out: dockerPS}, true},
		fakeCmd{[]string{"docker", "stats"}, Result{Out: stats}, true})
	got := ContainerUse(p)
	if got.Pool != 2.5*float64(g) || got.Other != 1.5*float64(g) || len(got.Others) != 1 || got.Others[0] != "other-box" {
		t.Fatalf("got %+v", got)
	}
	if p.calledWSL() {
		t.Fatal("the Windows docker CLI answered; wsl must not be called")
	}
}

func TestContainerUseFallsBackToWSL(t *testing.T) {
	p := winProbe(
		fakeCmd{[]string{"docker", "ps"}, Result{}, false},
		fakeCmd{[]string{"docker", "stats"}, Result{}, false},
		fakeCmd{[]string{"wsl", "docker", "ps"}, Result{Out: dockerPS}, true},
		fakeCmd{[]string{"wsl", "docker", "stats"}, Result{Out: stats}, true})
	got := ContainerUse(p)
	if got.Pool != 2.5*float64(g) || got.Other != 1.5*float64(g) {
		t.Fatalf("got %+v", got)
	}
}

func TestDockerCPUsCLIThenWSLThenNone(t *testing.T) {
	if got := DockerCPUs(winProbe(fakeCmd{[]string{"docker", "info"}, Result{Out: "20\n"}, true})); got != 20 {
		t.Fatalf("cli = %d", got)
	}
	if got := DockerCPUs(winProbe(fakeCmd{[]string{"docker", "info"}, Result{}, false},
		fakeCmd{[]string{"wsl", "docker", "info"}, Result{Out: "12\n"}, true})); got != 12 {
		t.Fatalf("wsl = %d", got)
	}
	if got := DockerCPUs(winProbe(fakeCmd{[]string{"docker", "info"}, Result{}, false},
		fakeCmd{[]string{"wsl", "docker", "info"}, Result{Code: 1, Out: "The command docker could not be found in this WSL 2 distro"}, true})); got != 0 {
		t.Fatalf("none = %d", got)
	}
}

func TestDockerMissingOnBothReadsZeroAndSaysSo(t *testing.T) {
	got := ContainerUse(winProbe())
	if got.Pool != 0 || got.Other != 0 || got.Note == "" {
		t.Fatalf("got %+v", got)
	}
}

func TestLinuxNeverCallsWSL(t *testing.T) {
	p := linux(nil)
	ContainerUse(p)
	DockerCPUs(p)
	if p.calledWSL() {
		t.Fatalf("Linux called wsl: %v", p.calls)
	}
	if len(p.calls) == 0 {
		t.Fatal("expected docker to be tried")
	}
}
