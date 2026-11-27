package agent

import (
	"context"
	"log/slog"
	"time"

	agentv1 "github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/gen/agent/v1"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/leftovers"
)

// Runner leftovers (doctor R5.48, OPS33). The controller asks for a scan, then
// for the removal of items the scan showed. The agent scans again before it
// removes, and never touches what a runner of the controller owns, so neither
// needs the machine to be drained.

func (c Config) leftoverOS() leftovers.OS {
	if c.Leftovers != nil {
		return c.Leftovers
	}
	return leftovers.NewHostOS()
}

func ownedFromProto(in []*agentv1.OwnedRunner) []leftovers.Owned {
	out := make([]leftovers.Owned, 0, len(in))
	for _, o := range in {
		out = append(out, leftovers.Owned{Name: o.GetName(), ServiceName: o.GetServiceName()})
	}
	return out
}

func (l *link) answerLeftovers(rep *agentv1.RunnerLeftoversReport) {
	_ = l.send(&agentv1.SessionRequest{Message: &agentv1.SessionRequest_RunnerLeftoversReport{RunnerLeftoversReport: rep}})
}

func (l *link) leftoversFailed(id string, err error, admin bool) {
	l.answerLeftovers(&agentv1.RunnerLeftoversReport{CommandId: id, Ok: false, Error: l.redactor().Redact(err.Error()), Admin: admin})
}

// scanLeftovers answers ScanRunnerLeftovers with the items found.
func (l *link) scanLeftovers(ctx context.Context, m *agentv1.ScanRunnerLeftovers) {
	id := m.GetCommandId()
	red := l.redactor()
	o := l.cfg.leftoverOS()
	sc, err := leftovers.Find(ctx, o, ownedFromProto(m.GetOwned()), time.Now())
	if err != nil {
		l.cfg.Logger.Warn("runner leftovers: scan failed", slog.String("error", red.Redact(err.Error())))
		l.leftoversFailed(id, err, o.Admin())
		return
	}
	rep := &agentv1.RunnerLeftoversReport{CommandId: id, Ok: true, Admin: sc.Admin, ScannedAtUnixMs: sc.At.UnixMilli()}
	for _, it := range sc.Items {
		rep.Items = append(rep.Items, &agentv1.Leftover{
			Kind: string(it.Kind), Name: it.Name, Path: it.Path, SizeBytes: it.SizeBytes, Account: it.Account, Why: it.Why,
		})
	}
	l.cfg.Logger.Info("runner leftovers: scanned", slog.Int("items", len(rep.Items)))
	l.answerLeftovers(rep)
}

// removeLeftovers answers RemoveRunnerLeftovers with one result per item, in order.
func (l *link) removeLeftovers(ctx context.Context, m *agentv1.RemoveRunnerLeftovers) {
	id := m.GetCommandId()
	red := l.redactor()
	var refs []leftovers.Ref
	for _, it := range m.GetItems() {
		refs = append(refs, leftovers.Ref{Kind: leftovers.Kind(it.GetKind()), Name: it.GetName()})
	}
	log := func(s string) { l.cfg.Logger.Info("runner leftovers: " + red.Redact(s)) }
	o := l.cfg.leftoverOS()
	results, err := leftovers.Remove(ctx, o, ownedFromProto(m.GetOwned()), refs, log)
	if err != nil {
		l.cfg.Logger.Warn("runner leftovers: removal failed", slog.String("error", red.Redact(err.Error())))
		l.leftoversFailed(id, err, o.Admin())
		return
	}
	rep := &agentv1.RunnerLeftoversReport{CommandId: id, Ok: true, Admin: o.Admin(), ScannedAtUnixMs: time.Now().UnixMilli()}
	for _, r := range results {
		out := &agentv1.LeftoverResult{Kind: string(r.Kind), Name: r.Name, Outcome: string(r.Outcome), Error: red.Redact(r.Error)}
		for _, s := range r.Steps {
			out.Steps = append(out.Steps, red.Redact(s))
		}
		rep.Results = append(rep.Results, out)
	}
	l.answerLeftovers(rep)
}
