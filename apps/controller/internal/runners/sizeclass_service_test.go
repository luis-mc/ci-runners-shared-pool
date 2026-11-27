package runners

import (
	"context"
	"errors"
	"testing"
)

type memClassStore struct {
	c       ClassSizes
	p, m    int
	saved   int
	savedBy string
}

func (m *memClassStore) ClassSizes(context.Context) ClassSizes { return m.c }
func (m *memClassStore) SaveClassSizes(_ context.Context, c ClassSizes, by string) error {
	m.c, m.saved, m.savedBy = c, m.saved+1, by
	return nil
}
func (m *memClassStore) ReserveLimits(context.Context) (int, int) { return m.p, m.m }
func (m *memClassStore) SaveReserveLimits(_ context.Context, p, mi int, _ string) error {
	m.p, m.m = p, mi
	return nil
}

type fixedFacts struct {
	runners []ClassRunner
	pools   []PoolRoom
}

func (f fixedFacts) ClassRunners(context.Context) ([]ClassRunner, error) { return f.runners, nil }
func (f fixedFacts) Pools(context.Context) ([]PoolRoom, error)           { return f.pools, nil }

func TestClassServiceSavesPresetAndReservationNumbers(t *testing.T) {
	st := &memClassStore{c: TableSizes(), p: 3, m: 10}
	svc := &ClassService{Store: st}
	three := 5
	v, err := svc.Save(context.Background(), ClassInput{Preset: PresetCompact, ReservePasses: &three}, "owner")
	if err != nil {
		t.Fatal(err)
	}
	if v.Preset != PresetCompact || st.c.Small.CPUs != 1 || st.p != 5 || st.m != 10 || st.savedBy != "owner" {
		t.Fatalf("view %+v store %+v", v, st)
	}
	if g := svc.Get(context.Background()); g.ReservePasses != 5 || g.ReserveMinutes != 10 {
		t.Fatalf("get %+v", g)
	}
}

func TestClassServiceRefusesAndSavesNothing(t *testing.T) {
	st := &memClassStore{c: TableSizes(), p: 3, m: 10}
	svc := &ClassService{Store: st}
	bad := TableSizes()
	bad.Large.CPUs = 3
	if _, err := svc.Save(context.Background(), ClassInput{Sizes: &bad}, "o"); err == nil || st.saved != 0 {
		t.Fatalf("err %v saved %d", err, st.saved)
	}
	zero := 0
	if _, err := svc.Save(context.Background(), ClassInput{ReserveMinutes: &zero}, "o"); err == nil || err.Error() != MsgWholeNumbers {
		t.Fatalf("zero minutes: %v", err)
	}
	if _, err := svc.Save(context.Background(), ClassInput{Preset: "nope"}, "o"); err == nil {
		t.Fatal("unknown preset")
	}
}

func TestClassServiceChecksRunnersAndLowering(t *testing.T) {
	st := &memClassStore{c: TableSizes(), p: 3, m: 10}
	facts := fixedFacts{
		runners: []ClassRunner{{Name: "r1", Class: SizeLarge, Pool: PoolRoom{Machine: "pc", CPUs: 8, MemoryGiB: 16}}},
		pools:   []PoolRoom{{Machine: "pc", CPUs: 8, MemoryGiB: 16}},
	}
	svc := &ClassService{Store: st, Facts: facts}
	lower := TableSizes()
	lower.Large = SizeSpec{SizeLarge, 6, 12}
	if _, err := svc.Save(context.Background(), ClassInput{Sizes: &lower}, "o"); !errors.Is(err, ErrConfirmLowering) {
		t.Fatalf("got %v", err)
	}
	if _, err := svc.Save(context.Background(), ClassInput{Sizes: &lower, ConfirmLowering: true}, "o"); err != nil {
		t.Fatal(err)
	}
	v := svc.Get(context.Background())
	if len(v.Unheld) != 1 || v.Unheld[0] != SizeXL {
		t.Fatalf("unheld %v", v.Unheld)
	}
}
