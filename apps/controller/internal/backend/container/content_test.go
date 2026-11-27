package container

import "testing"

func TestBaseToolsReadTheDockerfile(t *testing.T) {
	got := BaseTools()
	if len(got) < 2 || got[0].Name != "Ubuntu" || got[0].Version != "24.04" {
		t.Fatalf("base tools: %+v", got)
	}
	for _, tl := range got {
		if tl.Name == "node" && (tl.Version == "" || len(tl.Version) > 3) {
			t.Errorf("node should show its major version, got %q", tl.Version)
		}
		if tl.Name == "gh" && tl.Version == "" {
			t.Error("gh has no version")
		}
	}
}

func TestOptionalLayersHaveWords(t *testing.T) {
	ls := OptionalLayers()
	if len(ls) != 5 {
		t.Fatalf("optional layers: %d", len(ls))
	}
	for _, l := range ls {
		if l.Title == "" || l.Purpose == "" || len(l.Labels) == 0 || len(LayerAdds(l)) == 0 {
			t.Errorf("layer %s is missing words: %+v", l.Name, l)
		}
	}
}
