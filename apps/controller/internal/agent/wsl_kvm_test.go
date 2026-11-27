package agent

import "testing"

func TestParseWslconfig(t *testing.T) {
	for name, c := range map[string]struct {
		text string
		want Nested
	}{
		"on":            {"[wsl2]\nmemory=8GB\nnestedVirtualization=true\n", NestedOn},
		"off":           {"[wsl2]\nnestedVirtualization=false\n", NestedOff},
		"unset":         {"[wsl2]\nmemory=8GB\n", NestedUnset},
		"empty":         {"", NestedUnset},
		"crlf+case":     {"[WSL2]\r\nNestedVirtualization = TRUE\r\n", NestedOn},
		"wrong section": {"[experimental]\nnestedVirtualization=true\n", NestedUnset},
		"comment":       {"[wsl2]\n# nestedVirtualization=true\n; nestedVirtualization=true\n", NestedUnset},
		"trailing":      {"[wsl2]\nnestedVirtualization=true # yes\n", NestedOn},
		"last wins":     {"[wsl2]\nnestedVirtualization=true\nnestedVirtualization=false\n", NestedOff},
	} {
		if got := ParseWslconfig(c.text); got != c.want {
			t.Errorf("%s: %v, want %v", name, got, c.want)
		}
	}
}

// TestWslKVMLoadsOnlyWhenNestedIsNotOff: machines R5.53.
func TestWslKVMLoadsOnlyWhenNestedIsNotOff(t *testing.T) {
	if p := PlanKVMLoad(true, NestedOff); p.Load || p.Reason != "" {
		t.Errorf("device there: %+v", p)
	}
	if p := PlanKVMLoad(false, NestedOff); p.Load || p.Reason != ReasonNestedOff {
		t.Errorf("nested off: %+v", p)
	}
	for _, n := range []Nested{NestedOn, NestedUnset} {
		if p := PlanKVMLoad(false, n); !p.Load {
			t.Errorf("nested %v: %+v", n, p)
		}
	}
	if WSLKVMReason(NestedOff) != "nested virtualization is off in .wslconfig" || WSLKVMReason(NestedOn) != "" {
		t.Error("reason text")
	}
}
