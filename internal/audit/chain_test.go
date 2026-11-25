package audit

import (
	"context"
	"errors"
	"testing"
)

func TestChainRecordsAreLinked(t *testing.T) {
	ctx := context.Background()
	l := New(open(t))
	for i := 0; i < 3; i++ {
		if err := l.Record(ctx, System, "a.b", Target{}, nil); err != nil {
			t.Fatal(err)
		}
	}
	rep, text, err := CheckChain(ctx, l)
	if err != nil || !rep.OK || rep.Checked != 3 || text == "" {
		t.Fatalf("%+v %q %v", rep, text, err)
	}
}

type failing struct{}

func (failing) Verify(context.Context) (ChainReport, error) { return ChainReport{}, errors.New("disk") }

func TestCheckChainReturnsReadError(t *testing.T) {
	if _, _, err := CheckChain(context.Background(), failing{}); err == nil {
		t.Fatal("a read error was hidden")
	}
}
