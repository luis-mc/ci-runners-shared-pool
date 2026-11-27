package jobsdata

import (
	"reflect"
	"testing"
)

func TestSplitName(t *testing.T) {
	cases := []struct {
		in     string
		typ    string
		matrix []string
	}{
		{"build", "build", nil},
		{"test (ubuntu, 22)", "test", []string{"ubuntu", "22"}},
		{"test (a)", "test", []string{"a"}},
		{"  lint  ", "lint", nil},
		{"(odd)", "(odd)", nil},
	}
	for _, c := range cases {
		typ, m := SplitName(c.in)
		if typ != c.typ || !reflect.DeepEqual(m, c.matrix) {
			t.Errorf("SplitName(%q) = %q %v, want %q %v", c.in, typ, m, c.typ, c.matrix)
		}
	}
}

func TestTaskFromBranch(t *testing.T) {
	cases := map[string]string{
		"OPS34-jobs-section": "OPS34",
		"RES15-jobs-section": "RES15",
		"main":               "",
		"feature-x":          "",
		"fix-1":              "",
		"A1-x":               "A1",
	}
	for in, want := range cases {
		if got := TaskFromBranch(in); got != want {
			t.Errorf("TaskFromBranch(%q) = %q, want %q", in, got, want)
		}
	}
}
