package agentlink

import (
	"context"
	"time"

	agentv1 "github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/gen/agent/v1"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/registry/builds"
)

// The image registry calls (REG10 contract, docs/features/registry R5.20,
// protocol version 3). The agent asks; the controller answers with the same
// request id. This file is the contract stub: with no RegistrySink the
// controller says "not in the registry" and gives no staging name, so an agent
// builds locally as before and pushes nothing. REG1, REG2 and REG4 fill the
// sink; REG2 owns this file after the join.

// MinRegistryProtocol is the first protocol version with the registry calls.
const MinRegistryProtocol = agentv1.ProtocolVersion_PROTOCOL_VERSION_3

// RegistryCallTimeout bounds one answer of the sink.
const RegistryCallTimeout = 30 * time.Second

// RegistrySink answers the registry calls of agents on the controller.
// EnsureImage is answered by REG4's registry.Locator, RegistryToken by REG1's
// registry.TokenIssuer, the two reports go to REG4's registry.Recorder (and a
// PUSHED report to REG1's registry.Promoter).
type RegistrySink interface {
	EnsureImage(ctx context.Context, machineID string, req *agentv1.EnsureImage) *agentv1.EnsureImageAnswer
	RegistryToken(ctx context.Context, machineID string, req *agentv1.RegistryTokenRequest) *agentv1.RegistryToken
	PushReport(ctx context.Context, machineID string, rep *agentv1.PushReport)
	PullReport(ctx context.Context, machineID string, rep *agentv1.PullReport)
}

// ErrNoRegistry is the refusal text of a token asked from a controller without
// a registry.
const ErrNoRegistry = "this controller has no image registry yet"

// SupportsRegistry reports whether the machine's last report says its agent
// understands the registry calls.
func (l *Link) SupportsRegistry(machineID string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	r, ok := l.reports[machineID]
	return ok && r.MachineReport != nil && r.GetRegistry()
}

// onRegistry handles the four agent messages of the registry. It returns true
// when msg was one of them. Answers are sent from a goroutine so a slow sink
// never holds the session.
func (l *Link) onRegistry(machineID string, msg *agentv1.SessionRequest) bool {
	sink := l.Registry
	switch m := msg.GetMessage().(type) {
	case *agentv1.SessionRequest_EnsureImage:
		go func() {
			ans := &agentv1.EnsureImageAnswer{RequestId: m.EnsureImage.GetRequestId(), Outcome: agentv1.EnsureImageAnswer_OUTCOME_NOT_FOUND}
			if sink != nil {
				ctx, cancel := context.WithTimeout(context.Background(), RegistryCallTimeout)
				defer cancel()
				if a := sink.EnsureImage(ctx, machineID, m.EnsureImage); a != nil {
					ans = a
					ans.RequestId = m.EnsureImage.GetRequestId()
				}
			}
			_ = l.Sender.Send(machineID, &agentv1.SessionResponse{Message: &agentv1.SessionResponse_EnsureImageAnswer{EnsureImageAnswer: ans}})
		}()
		return true
	case *agentv1.SessionRequest_RegistryTokenRequest:
		go func() {
			tok := &agentv1.RegistryToken{RequestId: m.RegistryTokenRequest.GetRequestId(), Refused: ErrNoRegistry}
			if sink != nil {
				ctx, cancel := context.WithTimeout(context.Background(), RegistryCallTimeout)
				defer cancel()
				if t := sink.RegistryToken(ctx, machineID, m.RegistryTokenRequest); t != nil {
					tok = t
					tok.RequestId = m.RegistryTokenRequest.GetRequestId()
				}
			}
			_ = l.Sender.Send(machineID, &agentv1.SessionResponse{Message: &agentv1.SessionResponse_RegistryGrant{RegistryGrant: tok}})
		}()
		return true
	case *agentv1.SessionRequest_PushReport:
		if sink != nil {
			go sink.PushReport(context.Background(), machineID, m.PushReport)
		}
		return true
	case *agentv1.SessionRequest_BuildReport:
		l.onBuildReport(machineID, m.BuildReport)
		return true
	case *agentv1.SessionRequest_PullReport:
		if sink != nil {
			go sink.PullReport(context.Background(), machineID, m.PullReport)
		}
		return true
	}
	return false
}

// onBuildReport turns the report of an agent into the life of a build record
// (IMG1, R5.68, R5.69). The machine is the one of the session, whatever the
// report says. A report for a build the controller never saw begin is dropped.
func (l *Link) onBuildReport(machineID string, rep *agentv1.BuildReport) {
	sink := l.Builds
	if sink == nil || rep.GetBuildId() == "" {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), RegistryCallTimeout)
	defer cancel()
	id := machineID + "/" + rep.GetBuildId()
	switch rep.GetPhase() {
	case agentv1.BuildReport_PHASE_BEGIN:
		h, err := sink.Begin(ctx, builds.Meta{
			MachineID: machineID, Profile: rep.GetProfile(), Arch: rep.GetArch(), Key: rep.GetKey(), Reason: rep.GetReason(),
		})
		if err != nil || h.ID == "" {
			return
		}
		l.mu.Lock()
		if l.buildIDs == nil {
			l.buildIDs = map[string]string{}
		}
		l.buildIDs[id] = h.ID
		l.mu.Unlock()
	case agentv1.BuildReport_PHASE_PROGRESS:
		if rid, ok := l.buildID(id); ok {
			sink.Progress(ctx, rid, builds.Progress{Step: int(rep.GetStep()), Total: int(rep.GetTotal()), Name: rep.GetStepName()})
		}
	case agentv1.BuildReport_PHASE_END:
		rid, ok := l.buildID(id)
		if !ok {
			return
		}
		sink.End(ctx, rid, builds.Outcome{
			State: rep.GetState(), SizeBytes: rep.GetSizeBytes(), FailureStep: rep.GetFailureStep(),
			FailureClass: rep.GetFailureClass(), FailureWhy: rep.GetFailureWhy(), LogTail: rep.GetLogTail(),
		})
		l.mu.Lock()
		delete(l.buildIDs, id)
		l.mu.Unlock()
	}
}

func (l *Link) buildID(id string) (string, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	rid, ok := l.buildIDs[id]
	return rid, ok
}
