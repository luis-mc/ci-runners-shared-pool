package runners

import (
	"testing"
	"time"
)

// R5.33: one key per base, layers and pins.
func TestBuildOnceKeepNewest(t *testing.T) {
	in := ImageKeyInput{Base: "ubuntu-24.04", Layers: []string{"node", "playwright"}, Pins: map[string]string{"runner": "2.338.2"}}
	k := ImageKey(in)
	same := ImageKeyInput{Base: "ubuntu-24.04", Layers: []string{"playwright", "node", "node", " "}, Pins: map[string]string{"runner": "2.338.2"}}
	if ImageKey(same) != k {
		t.Fatal("the key must not depend on the layer order or repeats")
	}
	for name, other := range map[string]ImageKeyInput{
		"base":  {Base: "ubuntu-26.04", Layers: in.Layers, Pins: in.Pins},
		"layer": {Base: in.Base, Layers: []string{"node"}, Pins: in.Pins},
		"pin":   {Base: in.Base, Layers: in.Layers, Pins: map[string]string{"runner": "2.331.0"}},
	} {
		if ImageKey(other) == k {
			t.Errorf("a different %s gave the same key", name)
		}
	}

	// R5.51: the extra package names are part of the key; none leaves it as it was.
	withNone := in
	withNone.Extras = []string{}
	if ImageKey(withNone) != k {
		t.Error("no extra packages must not change the key")
	}
	a := in
	a.Extras = []string{"zlib1g-dev", "libc++-dev", "zlib1g-dev"}
	b := in
	b.Extras = []string{"libc++-dev", "zlib1g-dev"}
	if ImageKey(a) == k || ImageKey(a) != ImageKey(b) {
		t.Error("extra packages change the key, in any order")
	}
	b.Extras = []string{"libc++-dev"}
	if ImageKey(a) == ImageKey(b) {
		t.Error("a changed list must give a new key")
	}

	at := func(h int) time.Time { return time.Date(2026, 11, 15, h, 0, 0, 0, time.UTC) }
	imgs := []Image{
		{Key: "a", OS: "linux", CreatedAt: at(1)}, {Key: "b", OS: "linux", CreatedAt: at(2)},
		{Key: "c", OS: "linux", CreatedAt: at(3)}, {Key: "d", OS: "linux", CreatedAt: at(4)},
		{Key: "e", OS: "linux", CreatedAt: at(5)}, {Key: "w", OS: "windows", CreatedAt: at(1)},
	}
	// Keep 3 per OS: a and b are older; b is in use by a runner, so only a goes.
	got := SelectPrune(imgs, map[string]bool{"b": true}, 3)
	if len(got) != 1 || got[0].Key != "a" {
		t.Fatalf("prune = %+v", got)
	}
	// Nothing in use: both older ones go. The only Windows image stays.
	got = SelectPrune(imgs, nil, 3)
	if len(got) != 2 || got[0].Key != "a" || got[1].Key != "b" {
		t.Fatalf("prune = %+v", got)
	}
	if got := SelectPrune(imgs[:3], nil, 0); len(got) != 0 {
		t.Fatalf("the default keeps 3: %+v", got)
	}
}
