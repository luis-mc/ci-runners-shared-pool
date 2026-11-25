package runners

import (
	"errors"
	"os"
	"strings"
	"testing"
	"time"
)

func goodArgs() []string {
	return []string{"run", "-d", "--memory", "4096m", "--cpus", "2", "--pids-limit", "4096",
		"--network", JobNetwork, "-v", "/work/job1:/home/runner/_work",
		"-v", "/opt/crsp:/crsp:ro", "--tmpfs", "/tmp", "img"}
}

var okPaths = []string{"/work/job1", "/opt/crsp"}

func TestMemoryCPUAndPidsLimitsAlwaysSet(t *testing.T) {
	l := LimitsFor(0, 0)
	if l.MemoryMB <= 0 || l.CPUs <= 0 || l.Pids <= 0 {
		t.Fatalf("a machine with no pool size gets defaults: %+v", l)
	}
	if got := LimitsFor(8192, 4); got.MemoryMB != 8192 || got.CPUs != 4 {
		t.Fatalf("pool size wins: %+v", got)
	}
	if err := CheckJobContainerArgs(goodArgs(), okPaths); err != nil {
		t.Fatal(err)
	}
	for _, drop := range [][]string{{"--memory", "4096m"}, {"--cpus", "2"}, {"--pids-limit", "4096"}} {
		var a []string
		for i := 0; i < len(goodArgs()); i++ {
			g := goodArgs()
			if g[i] == drop[0] {
				i++
				continue
			}
			a = append(a, g[i])
		}
		if err := CheckJobContainerArgs(a, okPaths); !errors.Is(err, ErrJobContainer) {
			t.Fatalf("without %s: %v", drop[0], err)
		}
	}
	zero := strings.Replace(strings.Join(goodArgs(), " "), "--memory 4096m", "--memory=0", 1)
	if err := CheckJobContainerArgs(strings.Fields(zero), okPaths); err == nil {
		t.Fatal("memory 0 is no limit")
	}
}

func TestNoHostFolderMounts(t *testing.T) {
	for _, bad := range []string{"/etc:/etc", "/home/me:/h", `C:\Users\me:/h`, "/var/run/x:/x"} {
		a := append(goodArgs(), "-v", bad)
		if err := CheckJobContainerArgs(a, okPaths); err == nil {
			t.Fatalf("%s accepted", bad)
		}
	}
	a := append(goodArgs(), "--mount", "type=bind,source=/etc,target=/e")
	if CheckJobContainerArgs(a, okPaths) == nil {
		t.Fatal("bind mount accepted")
	}
	a = append(goodArgs(), "-v", "crsp-cache:/cache", "--mount", "type=volume,source=v,target=/v")
	if err := CheckJobContainerArgs(a, okPaths); err != nil {
		t.Fatalf("named volumes are not host folders: %v", err)
	}
}

func TestOneJobNetworkNoContainerToContainerTraffic(t *testing.T) {
	if strings.Join(NetworkCreateArgs(), " ") != "network create --driver bridge --opt com.docker.network.bridge.enable_icc=false crsp-jobs" {
		t.Fatalf("%v", NetworkCreateArgs())
	}
}

func TestCheckRunArgsRefusesOtherNetworks(t *testing.T) {
	for _, n := range []string{"bridge", "host", "other"} {
		a := strings.Replace(strings.Join(goodArgs(), " "), "--network "+JobNetwork, "--network "+n, 1)
		if CheckJobContainerArgs(strings.Fields(a), okPaths) == nil {
			t.Fatalf("network %s accepted", n)
		}
	}
	a := strings.Fields(strings.Replace(strings.Join(goodArgs(), " "), "--network "+JobNetwork, "", 1))
	if CheckJobContainerArgs(a, okPaths) == nil {
		t.Fatal("no network flag is the default bridge")
	}
}

func TestSecurityPageListsResidualRisks(t *testing.T) {
	b, err := os.ReadFile("../../SECURITY.md")
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	i := strings.Index(s, "## What a job can reach")
	if i < 0 {
		t.Fatal("SECURITY.md has no section `What a job can reach`")
	}
	sec := s[i:]
	for _, w := range []string{"loopback", "setuid", "CRSP_RUNS_ON_", "What to do"} {
		if !strings.Contains(sec, w) {
			t.Fatalf("section does not mention %q", w)
		}
	}
}

func TestPageNamesEveryLimit(t *testing.T) {
	b, _ := os.ReadFile("../../SECURITY.md")
	s := string(b)
	for _, w := range []string{"memory limit", "CPU limit", "process-count limit", "--pids-limit", JobNetwork, "no host folder"} {
		if !strings.Contains(s, w) {
			t.Errorf("SECURITY.md does not name %q (R5.63)", w)
		}
	}
}

func TestStartTime(t *testing.T) {
	var fast, slow []time.Duration
	for i := 0; i < StartSamples; i++ {
		fast = append(fast, time.Duration(8+i%5)*time.Second)
		slow = append(slow, 20*time.Second)
	}
	if !WithinStartBudget(fast, ContainerStartBudget) {
		t.Fatal("median 10 s is within 15 s")
	}
	if WithinStartBudget(slow, ContainerStartBudget) {
		t.Fatal("median 20 s is over 15 s")
	}
	if WithinStartBudget(fast[:5], ContainerStartBudget) {
		t.Fatal("needs 20 starts")
	}
	if StartMedian([]time.Duration{4 * time.Second, 2 * time.Second}) != 3*time.Second {
		t.Fatal("even median")
	}
	if StartupAllowance("linux") > ContainerStartBudget+10*time.Second {
		t.Fatal("allowance far off budget")
	}
}
