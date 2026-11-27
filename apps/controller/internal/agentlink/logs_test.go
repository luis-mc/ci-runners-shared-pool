package agentlink

import (
	"context"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/logs"
)

type memStore struct {
	mu    sync.Mutex
	lines []logs.Line
}

func (m *memStore) Insert(_ context.Context, l []logs.Line) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.lines = append(m.lines, l...)
	return nil
}
func (m *memStore) Search(context.Context, logs.Query) (logs.Page, error)  { return logs.Page{}, nil }
func (m *memStore) Prune(context.Context, time.Time, int64) (int64, error) { return 0, nil }

// TestLogBatchRoundTrip: agent lines survive the wire form and reach the store under the sending machine (R5.20).
func TestLogBatchRoundTrip(t *testing.T) {
	st := &memStore{}
	w := logs.NewFailOpen(st)
	in := []logs.Line{
		{At: time.UnixMilli(1000).UTC(), Level: slog.LevelWarn, Component: "runner-setup", Message: "m", Runner: "r1", Job: "77", Attrs: `{"a":1}`},
		{At: time.UnixMilli(2000).UTC(), Level: slog.LevelError, Message: "n", Job: "build"},
	}
	LogSink{Out: w}.LogBatch(context.Background(), "mach-1", LogBatchToProto(in, 5))
	w.Close()
	if len(st.lines) != 2 {
		t.Fatalf("stored %d lines", len(st.lines))
	}
	for i, l := range st.lines {
		want := in[i]
		want.Machine = "mach-1"
		if l != want {
			t.Errorf("line %d: %+v, want %+v", i, l, want)
		}
	}
}
