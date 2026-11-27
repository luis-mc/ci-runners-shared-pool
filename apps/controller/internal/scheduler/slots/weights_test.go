package slots

import "testing"

func TestWeightOfClasses(t *testing.T) {
	s := Defaults()
	s.JobSlots = 4
	for _, c := range []struct {
		job   string
		class string
		w     Weight
	}{
		{"lint", "lint", Weight{1, 1}},
		{"e2e-web", "e2e", Weight{2, 2}},
		{"vm", "vm", Weight{2, 4}},
		{"deploy", "default", DefaultWeight},
	} {
		w, class := s.WeightOf(JobNames{Job: c.job})
		if w != c.w || class != c.class {
			t.Errorf("%s: got %+v %q want %+v %q", c.job, w, class, c.w, c.class)
		}
	}
	// the shard names the class too
	if w, class := s.WeightOf(JobNames{Job: "test", Shard: "e2e-2"}); class != "e2e" || w.CPU != 2 {
		t.Errorf("shard: %+v %q", w, class)
	}
}

func TestWeightCutToBudget(t *testing.T) {
	s := Defaults() // two slots
	if w, _ := s.WeightOf(JobNames{Job: "vm"}); w != (Weight{2, 2}) {
		t.Fatalf("got %+v", w)
	}
	s.Classes = []JobClass{{Name: "zero", Match: []string{"z"}, Weight: Weight{0, -3}}}
	if w, _ := s.WeightOf(JobNames{Job: "z"}); w != DefaultWeight {
		t.Fatalf("below 1 counts as 1, got %+v", w)
	}
}

func TestHeldWeightCountsCISlotsOnly(t *testing.T) {
	s := Defaults()
	s.JobSlots = 5
	held := []Slot{
		{Kind: KindCI, Job: Job{Job: "e2e"}},
		{Kind: KindCI, Job: Job{Job: "deploy"}},
		{Kind: KindHeavy, Job: Job{Job: "vm"}},
		{Kind: KindLight, Job: Job{Job: "smoke"}},
	}
	if got := s.HeldWeight(held); got != (Weight{3, 3}) {
		t.Fatalf("got %+v", got)
	}
}

// R5.88: a size from the scan, a measuring or an override feeds WeightOf; the
// name classes stay as the fallback.
func TestWeightOfUsesSizesThenNameClasses(t *testing.T) {
	s := Defaults()
	s.JobSlots = 4
	s.Sizes = []JobSize{
		{Repository: "o/r", Pattern: "integration-*", Weight: Weight{3, 2}, Class: "heavy", Source: "override"},
		{Repository: "o/r", Pattern: "lint", Weight: Weight{1, 1}, Class: "light", Source: "scan"},
		{Repository: "", Pattern: "e2e", Weight: Weight{9, 9}, Class: "heavy", Source: "scan"},
	}
	cases := []struct {
		name  string
		job   JobNames
		want  Weight
		class string
	}{
		{"override glob", JobNames{Repository: "o/r", Job: "integration-db"}, Weight{3, 2}, "heavy"},
		{"other repository falls back to the name class", JobNames{Repository: "x/y", Job: "integration-db"}, DefaultWeight, "default"},
		{"scan size beats the name class", JobNames{Repository: "o/r", Job: "lint"}, Weight{1, 1}, "light"},
		{"a size is cut to the budget", JobNames{Repository: "any/repo", Job: "e2e"}, Weight{4, 4}, "heavy"},
		{"no size: the name class", JobNames{Repository: "x/y", Job: "vm-a"}, Weight{2, 4}, "vm"},
		{"no size, no class: 1/1", JobNames{Repository: "x/y", Job: "zzz"}, DefaultWeight, "default"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			w, class := s.WeightOf(c.job)
			if w != c.want || class != c.class {
				t.Fatalf("WeightOf = %v %q, want %v %q", w, class, c.want, c.class)
			}
		})
	}
}
