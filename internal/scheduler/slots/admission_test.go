package slots

import "testing"

func TestRequirementIsNotCutToTheJobSlotBudget(t *testing.T) {
	s := Defaults() // 2 job slots: WeightOf cuts to 2
	s.Sizes = []JobSize{{Pattern: "big", Weight: Weight{CPU: 6, Memory: 12}, Class: "heavy", Source: "scan"}}
	n := JobNames{Job: "big"}
	if w, _ := s.WeightOf(n); w.CPU != 2 {
		t.Fatalf("weight %+v", w)
	}
	if w, c := s.RequirementOf(n); w != (Weight{6, 12}) || c != "heavy" {
		t.Fatalf("requirement %+v %q", w, c)
	}
	if w, c := s.RequirementOf(JobNames{Job: "unknown"}); w != DefaultWeight || c != "default" {
		t.Fatalf("default %+v %q", w, c)
	}
	if w, _ := s.RequirementOf(JobNames{Job: "e2e"}); w != (Weight{2, 2}) {
		t.Fatalf("name class %+v", w)
	}
}

func TestSmallestAndNextClass(t *testing.T) {
	a := Admission{Classes: []ClassCeiling{{"small", 2, 4}, {"medium", 4, 8}, {"large", 8, 16}, {"xl", 16, 32}}}
	if a.Smallest(3, 4) != "medium" || a.Smallest(2, 9) != "large" || a.Smallest(17, 1) != "" {
		t.Fatal("smallest")
	}
	if a.Next("large") != "xl" || a.Next("xl") != "" {
		t.Fatal("next")
	}
}
