package insights

import (
	"reflect"
	"testing"
	"time"
)

func sf(id int64, mins int, age int, g Guardrail) StoredFinding {
	return StoredFinding{ID: id, FirstSeen: t0.Add(-time.Duration(age) * time.Hour), Finding: Finding{MinutesSavedWeek: mins, Guardrail: g}}
}

func ids(in []StoredFinding) []int64 {
	var out []int64
	for _, f := range in {
		out = append(out, f.ID)
	}
	return out
}

// R5.11: ranked by minutes saved a week, then first seen (older first), then id.
func TestRankByMinutesThenAge(t *testing.T) {
	in := []StoredFinding{sf(1, 10, 1, ""), sf(2, 30, 1, ""), sf(3, 10, 5, ""), sf(4, 10, 5, "")}
	got := ids(Rank(in))
	if want := []int64{2, 3, 4, 1}; !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
	if in[0].ID != 1 {
		t.Fatal("Rank changed its input")
	}
}

// R5.9: findings that save no time (G4, G5) come after the others, in guardrail order.
func TestRankZeroMinutesByGuardrail(t *testing.T) {
	in := []StoredFinding{sf(1, 0, 9, "G5"), sf(2, 0, 1, "G4"), sf(3, 1, 1, ""), sf(4, 0, 9, "")}
	got := ids(Rank(in))
	if want := []int64{3, 2, 1, 4}; !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
}

func TestTopN(t *testing.T) {
	in := []StoredFinding{sf(1, 1, 1, ""), sf(2, 2, 1, ""), sf(3, 3, 1, ""), sf(4, 4, 1, "")}
	if got := ids(TopN(in, 3)); !reflect.DeepEqual(got, []int64{4, 3, 2}) {
		t.Fatal(got)
	}
	if got := TopN(in[:2], 3); len(got) != 2 {
		t.Fatal(got)
	}
}
