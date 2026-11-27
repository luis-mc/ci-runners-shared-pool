package agentlink

import (
	"context"
	"testing"

	agentv1 "github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/gen/agent/v1"
)

type fakeHygiene struct {
	reported map[string]string
	keep     bool
	pending  bool
}

func (f *fakeHygiene) ReportClean(_ context.Context, _, name string, _ int64, why string) error {
	f.reported[name] = why
	return nil
}
func (f *fakeHygiene) KeepDependencies(context.Context, string, string) bool { return f.keep }
func (f *fakeHygiene) ClearCachePending(string, string) bool                 { return f.pending }

// R5.90, R5.91: the report of the agent reaches the controller and the answer
// carries the keep setting and a waiting Clear now. A report taken while a
// Clear now waits does not end it.
func TestRunnerCleanReportIsAnsweredWithTheSettings(t *testing.T) {
	var sent []*agentv1.RunnerHygiene
	l := New(sendFn(func(_ string, m *agentv1.SessionResponse) error {
		sent = append(sent, m.GetRunnerHygiene())
		return nil
	}))
	h := &fakeHygiene{reported: map[string]string{}, keep: false}
	l.Hygiene = h
	msg := &agentv1.SessionRequest{Message: &agentv1.SessionRequest_RunnerClean{RunnerClean: &agentv1.RunnerClean{
		Runners: []*agentv1.RunnerCleanStatus{{RunnerName: "r1", UncleanReason: "left /tmp/x"}},
	}}}
	if !l.OnMessage("m1", msg) {
		t.Fatal("not handled")
	}
	if h.reported["r1"] != "left /tmp/x" || len(sent) != 1 || sent[0].GetKeepDependencies() || sent[0].GetClearCache() {
		t.Fatalf("reported %v sent %v", h.reported, sent)
	}
	h.pending, h.keep = true, true
	delete(h.reported, "r1")
	l.OnMessage("m1", msg)
	if _, ok := h.reported["r1"]; ok {
		t.Fatal("a report ended a Clear now that was not carried out")
	}
	if len(sent) != 2 || !sent[1].GetClearCache() || !sent[1].GetKeepDependencies() {
		t.Fatalf("sent %v", sent)
	}
}
