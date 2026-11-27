package audit

import (
	"context"
	"strconv"
	"testing"
	"time"
)

func TestNewestFirstAndFilters(t *testing.T) {
	ctx := context.Background()
	l := New(open(t))
	for i := 0; i < 5; i++ {
		who := "a"
		if i%2 == 1 {
			who = "b"
		}
		if err := l.Record(ctx, Actor{Login: who, Source: SourceAPI}, "thing."+strconv.Itoa(i), Target{Type: "t", ID: strconv.Itoa(i)}, nil); err != nil {
			t.Fatal(err)
		}
	}
	all, _ := l.List(ctx, Filter{})
	if len(all.Items) != 5 || all.Items[0].Action != "thing.4" || all.Items[4].Action != "thing.0" {
		t.Fatalf("not newest first: %+v", all.Items)
	}
	if p, _ := l.List(ctx, Filter{Actor: "b"}); len(p.Items) != 2 {
		t.Errorf("actor filter: %d", len(p.Items))
	}
	if p, _ := l.List(ctx, Filter{Action: "thing.3"}); len(p.Items) != 1 {
		t.Errorf("action filter: %d", len(p.Items))
	}
	if p, _ := l.List(ctx, Filter{TargetType: "t", TargetID: "2"}); len(p.Items) != 1 || p.Items[0].Action != "thing.2" {
		t.Errorf("target filter: %+v", p.Items)
	}
	if p, _ := l.List(ctx, Filter{From: time.Now().Add(time.Hour)}); len(p.Items) != 0 {
		t.Errorf("from filter: %d", len(p.Items))
	}
	if p, _ := l.List(ctx, Filter{To: time.Now().Add(time.Hour)}); len(p.Items) != 5 {
		t.Errorf("to filter: %d", len(p.Items))
	}
	// Paging by cursor visits every row once.
	var seen []string
	cur := ""
	for page := 0; page < 10; page++ { // 5 rows at 2 per page end within 3 pages
		p, err := l.List(ctx, Filter{Limit: 2, Cursor: cur})
		if err != nil {
			t.Fatal(err)
		}
		for _, e := range p.Items {
			seen = append(seen, e.Action)
		}
		if p.NextCursor == "" {
			break
		}
		cur = p.NextCursor
	}
	if len(seen) != 5 || seen[0] != "thing.4" || seen[4] != "thing.0" {
		t.Fatalf("paging: %v", seen)
	}
}

func TestSummaryJoinsKeysInOrder(t *testing.T) {
	if got := Summary(`{"b":2,"a":"x"}`); got != "a=x, b=2" {
		t.Error(got)
	}
	if Summary("not json") != "" || Summary("{}") != "" {
		t.Error("empty cases")
	}
}
