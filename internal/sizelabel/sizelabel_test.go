package sizelabel

import (
	"strings"
	"testing"
)

func TestParseReadsCPUAndRAMAndIgnoresOtherLabels(t *testing.T) {
	s, err := Parse([]string{"self-hosted", "Linux", "cpu=4", "RAM=8gb", "region=eu"})
	if err != nil {
		t.Fatal(err)
	}
	if s.CPUs != 4 || s.MemoryGiB != 8 {
		t.Fatalf("got %+v", s)
	}
	if l := s.ContainerLimits(); l.MemoryMB != 8192 || l.CPUs != 4 {
		t.Fatalf("limits %+v", l)
	}
}

func TestParseRefusesWithClearErrors(t *testing.T) {
	cases := map[string]string{
		"cpu=0":      "whole number from 1",
		"cpu=2.5":    "whole number",
		"cpu=abc":    "not a number",
		"cpu=":       "no value",
		"ram=0.1":    "ram must be from",
		"ram=99999":  "ram must be from",
		"cpu=100000": "whole number from 1",
	}
	for l, want := range cases {
		_, err := Parse([]string{l})
		if err == nil || !strings.Contains(err.Error(), want) || !strings.Contains(err.Error(), l) {
			t.Errorf("%s: err %v, want %q", l, err, want)
		}
	}
	if _, err := Parse([]string{"cpu=2", "cpu=4"}); err == nil || !strings.Contains(err.Error(), "twice") {
		t.Errorf("duplicate: %v", err)
	}
}

func TestIsSizeLabel(t *testing.T) {
	for l, want := range map[string]bool{"cpu=4": true, " RAM=8": true, "cpu": false, "gpu=1": false, "linux": false} {
		if IsSizeLabel(l) != want {
			t.Errorf("%q want %v", l, want)
		}
	}
}

func TestFitAndPlacePicksSmallestMachineThatHolds(t *testing.T) {
	ms := []Machine{
		{Name: "big", OS: "linux", CPUs: 16, MemoryGiB: 64},
		{Name: "mid", OS: "linux", CPUs: 8, MemoryGiB: 16},
		{Name: "win", OS: "windows", CPUs: 8, MemoryGiB: 16},
		{Name: "tiny", OS: "linux", CPUs: 2, MemoryGiB: 4},
	}
	m, err := Place(Spec{CPUs: 4, MemoryGiB: 8}, "linux", ms)
	if err != nil || m.Name != "mid" {
		t.Fatalf("got %v %v", m, err)
	}
	_, err = Place(Spec{CPUs: 32}, "linux", ms)
	if err == nil || !strings.Contains(err.Error(), "machine big has 16 CPUs") {
		t.Fatalf("err %v", err)
	}
	if _, err := Place(Spec{}, "macos", ms); err == nil || !strings.Contains(err.Error(), "no machine runs macos") {
		t.Fatalf("err %v", err)
	}
}

func TestClampNeverRaisesAboveMachine(t *testing.T) {
	got := Spec{CPUs: 4, MemoryGiB: 8}.Clamp(Limits{MemoryMB: 4096, CPUs: 2})
	if got.MemoryMB != 4096 || got.CPUs != 2 {
		t.Fatalf("%+v", got)
	}
	got = Spec{CPUs: 4}.Clamp(Limits{MemoryMB: 4096})
	if got.MemoryMB != 4096 || got.CPUs != 4 {
		t.Fatalf("%+v", got)
	}
}
