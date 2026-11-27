package agentlink

import (
	"context"
	"errors"
	"time"

	agentv1 "github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/gen/agent/v1"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/leftovers"
)

// Waits of the runner leftovers commands (OPS33).
const (
	ScanLeftoversTimeout   = 2 * time.Minute
	RemoveLeftoversTimeout = 10 * time.Minute
)

// ErrAgentCannotFindLeftovers is a machine whose agent does not say
// `runner_leftovers` in its machine report: an older agent. Nothing is sent to it.
var ErrAgentCannotFindLeftovers = errors.New("the agent of this machine cannot look for runner leftovers yet, update it")

var _ leftovers.Source = (*Link)(nil)

// Scan asks the agent to look for runner leftovers. It reads only.
func (l *Link) Scan(ctx context.Context, machineID string, owned []leftovers.Owned) (leftovers.Scan, error) {
	rep, err := l.leftoversCall(ctx, machineID, ScanLeftoversTimeout, func(id string) *agentv1.SessionResponse {
		return &agentv1.SessionResponse{Message: &agentv1.SessionResponse_ScanRunnerLeftovers{ScanRunnerLeftovers: &agentv1.ScanRunnerLeftovers{
			CommandId: id, Owned: ownedToProto(owned),
		}}}
	})
	if err != nil {
		return leftovers.Scan{}, err
	}
	sc := leftovers.Scan{Admin: rep.GetAdmin(), At: l.now()}
	if ms := rep.GetScannedAtUnixMs(); ms > 0 {
		sc.At = time.UnixMilli(ms).UTC()
	}
	for _, it := range rep.GetItems() {
		sc.Items = append(sc.Items, leftovers.Item{
			Kind: leftovers.Kind(it.GetKind()), Name: it.GetName(), Path: it.GetPath(), SizeBytes: it.GetSizeBytes(),
			Account: it.GetAccount(), Why: it.GetWhy(),
		})
	}
	return sc, nil
}

// Remove asks the agent to remove the named items. The agent checks each again;
// what it refuses or cannot remove is a Result.
func (l *Link) Remove(ctx context.Context, machineID string, owned []leftovers.Owned, refs []leftovers.Ref) ([]leftovers.Result, error) {
	rep, err := l.leftoversCall(ctx, machineID, RemoveLeftoversTimeout, func(id string) *agentv1.SessionResponse {
		items := make([]*agentv1.LeftoverRef, 0, len(refs))
		for _, r := range refs {
			items = append(items, &agentv1.LeftoverRef{Kind: string(r.Kind), Name: r.Name})
		}
		return &agentv1.SessionResponse{Message: &agentv1.SessionResponse_RemoveRunnerLeftovers{RemoveRunnerLeftovers: &agentv1.RemoveRunnerLeftovers{
			CommandId: id, Owned: ownedToProto(owned), Items: items,
		}}}
	})
	if err != nil {
		return nil, err
	}
	out := make([]leftovers.Result, 0, len(rep.GetResults()))
	for _, r := range rep.GetResults() {
		out = append(out, leftovers.Result{
			Kind: leftovers.Kind(r.GetKind()), Name: r.GetName(), Outcome: leftovers.Outcome(r.GetOutcome()),
			Error: r.GetError(), Steps: r.GetSteps(),
		})
	}
	return out, nil
}

func ownedToProto(owned []leftovers.Owned) []*agentv1.OwnedRunner {
	out := make([]*agentv1.OwnedRunner, 0, len(owned))
	for _, o := range owned {
		out = append(out, &agentv1.OwnedRunner{Name: o.Name, ServiceName: o.ServiceName})
	}
	return out
}

// leftoversCall sends the message built with the command id and waits for the
// RunnerLeftoversReport with the same id. A report with ok false is an error
// holding the agent's text.
func (l *Link) leftoversCall(ctx context.Context, machineID string, wait time.Duration, build func(id string) *agentv1.SessionResponse) (*agentv1.RunnerLeftoversReport, error) {
	if rep, ok := l.Reports()[machineID]; !ok || !rep.GetRunnerLeftovers() {
		return nil, ErrAgentCannotFindLeftovers
	}
	id := newCommandID()
	ch := make(chan *agentv1.RunnerLeftoversReport, 1)
	l.mu.Lock()
	l.pendingLO[id] = ch
	l.mu.Unlock()
	defer func() { l.mu.Lock(); delete(l.pendingLO, id); l.mu.Unlock() }()
	if err := l.Sender.Send(machineID, build(id)); err != nil {
		return nil, errors.New("the machine's agent is not connected: " + err.Error())
	}
	t := time.NewTimer(wait)
	defer t.Stop()
	select {
	case r := <-ch:
		if !r.GetOk() {
			text := r.GetError()
			if text == "" {
				text = "the agent reported a failure without a reason"
			}
			return nil, errors.New(text)
		}
		return r, nil
	case <-t.C:
		return nil, ErrNoAnswer
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}
