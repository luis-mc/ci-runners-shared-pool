package doctor

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"
	"time"
)

func leftoverFinding() Finding {
	return Finding{
		Check: "machine.runner_leftovers", Subject: Subject{Kind: "machine", ID: "m1", Name: "alpha"}, Severity: Fail,
		Title: "2 runner leftovers no controller owns on alpha", NeedsAdmin: true, Milestone: 1,
		Items: []Item{
			{Kind: "service", Name: "actions.runner.old", Path: `C:\crsp\runners\old`, Account: `.\crsp-old`, Why: "no owner"},
			{Kind: "folder", Name: `C:\crsp\runners\old`, SizeBytes: 3 << 30, Why: "no runner uses it"},
		},
		Action:    &Action{ID: ActionRemoveRunnerLeftovers, Label: "Remove"},
		CheckedAt: time.Date(2026, 11, 18, 10, 0, 0, 0, time.UTC),
	}
}

func TestFindingItemsAndActionRoundTripJSON(t *testing.T) {
	in := leftoverFinding()
	b, err := json.Marshal(in)
	if err != nil {
		t.Fatal(err)
	}
	var out Finding
	if err := json.Unmarshal(b, &out); err != nil || !reflect.DeepEqual(in, out) {
		t.Fatalf("round trip: %v\n in  %+v\n out %+v", err, in, out)
	}
	// A finding without them writes neither key.
	b, _ = json.Marshal(Finding{Check: "x"})
	var m map[string]any
	_ = json.Unmarshal(b, &m)
	if _, ok := m["items"]; ok {
		t.Fatal("items written for a finding without items")
	}
	if _, ok := m["action"]; ok {
		t.Fatal("action written for a finding without action")
	}
}

func TestFindingAPIMapsItemsAndAction(t *testing.T) {
	g := leftoverFinding().API()
	if g.Items == nil || len(*g.Items) != 2 || g.Action == nil || string(g.Action.Id) != "remove_runner_leftovers" || g.Action.Label != "Remove" {
		t.Fatalf("api %+v", g)
	}
	a, b := (*g.Items)[0], (*g.Items)[1]
	if string(a.Kind) != "service" || a.Account == nil || *a.Account != `.\crsp-old` || a.SizeBytes != nil || a.Path == nil || a.Why != "no owner" ||
		string(b.Kind) != "folder" || b.SizeBytes == nil || *b.SizeBytes != 3<<30 || b.Account != nil {
		t.Fatalf("items %+v %+v", a, b)
	}
	if plain := (Finding{Check: "x"}).API(); plain.Items != nil || plain.Action != nil {
		t.Fatalf("plain finding carries items or action: %+v", plain)
	}
	// The store keeps them too.
	st := NewMemStore()
	_ = st.SaveRun(context.Background(), Run{ID: "r", State: Done, Findings: []Finding{leftoverFinding()}})
	fs, _, _ := st.Latest(context.Background(), "", "")
	if len(fs) != 1 || len(fs[0].Items) != 2 || fs[0].Action == nil {
		t.Fatalf("store lost them: %+v", fs)
	}
}
