package core

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/agentlink"
	agentv1 "github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/gen/agent/v1"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/registry"
)

// busyRetry is how long an agent waits before it asks again (R5.15).
const busyRetry = 5 * time.Second

// seenPushes bounds the memory of promoted push ids.
const seenPushes = 1024

// Sink answers the registry calls of agents on the controller
// (agentlink.RegistrySink). Tokens and promotion are REG1's; the other parts
// are optional and set by the wiring: a nil Locator makes EnsureImage answer
// "not in the registry" with no staging name, so the agent builds and pushes
// nothing; a nil Recorder drops the reports it would keep.
type Sink struct {
	Tokens   registry.TokenIssuer
	Promoter registry.Promoter
	Recorder registry.Recorder
	Locator  registry.Locator
	Outcomes Outcomes
	// Target names the image a build of (repository, profile) is promoted to:
	// crsp/std/<profile> or crsp/custom/<owner>-<repo>/<profile>. Nil means no
	// staging name is given.
	Target func(ctx context.Context, repository, profile string) (string, error)
	// Host is host:port of the registry on the controller's HTTPS listener,
	// given to agents in the answers.
	Host string
	// ReadOnly is true during the cleanup window: no staging name is given.
	ReadOnly func() bool
	Log      *slog.Logger

	mu   sync.Mutex
	seen map[string]bool
	ring []string
}

var _ agentlink.RegistrySink = (*Sink)(nil)

func (s *Sink) log() *slog.Logger {
	if s.Log != nil {
		return s.Log
	}
	return slog.Default()
}

// EnsureImage implements agentlink.RegistrySink.
func (s *Sink) EnsureImage(ctx context.Context, machineID string, req *agentv1.EnsureImage) *agentv1.EnsureImageAnswer {
	if s.Locator == nil {
		return nil
	}
	k := registry.Key(req.GetKey())
	ans, err := s.Locator.Ensure(ctx, machineID, k, req.GetArch())
	if err != nil || ans.Busy {
		return &agentv1.EnsureImageAnswer{Outcome: agentv1.EnsureImageAnswer_OUTCOME_BUSY, RetryAfterMs: busyRetry.Milliseconds()}
	}
	if ans.Found {
		return &agentv1.EnsureImageAnswer{
			Outcome: agentv1.EnsureImageAnswer_OUTCOME_FOUND, Name: ans.Name, ManifestDigest: ans.Manifest,
			SizeBytes: ans.Size, RegistryHost: s.Host,
		}
	}
	out := &agentv1.EnsureImageAnswer{Outcome: agentv1.EnsureImageAnswer_OUTCOME_NOT_FOUND}
	if s.Target == nil || (s.ReadOnly != nil && s.ReadOnly()) || !k.Valid() {
		return out
	}
	var in registry.KeyInput
	if json.Unmarshal([]byte(req.GetKeyInputJson()), &in) != nil || in.Profile == "" {
		return out
	}
	target, err := s.Target(ctx, req.GetRepository(), in.Profile)
	if err != nil || target == "" {
		return out
	}
	if _, ok := TargetKind(target); !ok {
		return out
	}
	out.StagingName, out.TargetName, out.RegistryHost = registry.IncomingName(machineID, target), target, s.Host
	return out
}

// RegistryToken implements agentlink.RegistrySink (R5.10, R5.11): the scopes
// asked are the scopes granted, or the answer says why not.
func (s *Sink) RegistryToken(ctx context.Context, machineID string, req *agentv1.RegistryTokenRequest) *agentv1.RegistryToken {
	var actions []string
	switch req.GetAction() {
	case agentv1.RegistryTokenRequest_ACTION_PULL:
		actions = []string{ActionPull}
	case agentv1.RegistryTokenRequest_ACTION_PUSH:
		actions = []string{ActionPush, ActionPull} // a push first looks for blobs it can skip
	default:
		return &agentv1.RegistryToken{Refused: "the request names no pull or push"}
	}
	want := make([]registry.Scope, 0, len(req.GetNames()))
	for _, n := range req.GetNames() {
		want = append(want, registry.Scope{Name: n, Actions: actions})
	}
	t, err := s.Tokens.Issue(ctx, machineID, want)
	if err != nil {
		return &agentv1.RegistryToken{Refused: refusalText(err)}
	}
	return &agentv1.RegistryToken{Token: t.Value, ExpiresAtUnixMs: t.ExpiresAt.UnixMilli()}
}

// refusalText is the reason an agent shows, in plain words and without any
// internal detail.
func refusalText(err error) string {
	switch {
	case errors.Is(err, registry.ErrOverBudget):
		return "the registry has no room for this image; the machine keeps its local image"
	case errors.Is(err, registry.ErrReadOnly):
		return "the registry is read-only during its cleanup; try again later"
	case errors.Is(err, ErrScope), errors.Is(err, ErrBadName):
		return "a machine may pull standard and custom images and push only to its own staging name"
	}
	return "the controller could not give a registry token"
}

var pushStates = map[agentv1.PushReport_State]string{
	agentv1.PushReport_STATE_PUSHING:            "pushing",
	agentv1.PushReport_STATE_WAITING_CONTROLLER: "waiting_controller",
	agentv1.PushReport_STATE_PUSHED:             "pushed",
	agentv1.PushReport_STATE_FAILED:             "failed",
}

// PushReport implements agentlink.RegistrySink: it keeps the report and, on
// state PUSHED, checks and promotes (R5.5). The outcome shows in the records
// and in the next EnsureImageAnswer; the agent gets no answer.
func (s *Sink) PushReport(ctx context.Context, machineID string, rep *agentv1.PushReport) {
	state, ok := pushStates[rep.GetState()]
	if !ok {
		return
	}
	var in registry.KeyInput
	_ = json.Unmarshal([]byte(rep.GetKeyInputJson()), &in)
	staged := registry.Staged{
		MachineID: machineID, Name: rep.GetStagingName(), Target: rep.GetTargetName(), Key: registry.Key(rep.GetKey()),
		Manifest: rep.GetManifestDigest(), KeyInput: in, Source: rep.GetRepository(), Commit: rep.GetCommitSha(), Branch: rep.GetBranch(),
	}
	if s.Recorder != nil {
		err := s.Recorder.RecordPush(ctx, registry.PushRecord{
			Staged: staged, PushID: rep.GetPushId(), State: state, Seconds: rep.GetSeconds(), Error: rep.GetError(), At: time.Now(),
		})
		if err != nil {
			s.log().Warn("registry: could not keep a push report", "push", rep.GetPushId(), "err", err)
		}
	}
	if state != "pushed" || !s.firstPromotion(rep.GetPushId()) {
		return
	}
	img, err := s.Promoter.Promote(ctx, staged)
	promoted := err == nil
	why := ""
	if err != nil {
		why = strings.TrimPrefix(err.Error(), registry.ErrPromoteRefuse.Error()+": ")
		if errors.Is(err, registry.ErrReadOnly) {
			s.forget(rep.GetPushId()) // the machine sends the report again after the window
			return
		}
		s.log().Info("registry: a staged image was refused", "machine", machineID, "name", staged.Name, "why", why)
	}
	if s.Outcomes != nil {
		if oerr := s.Outcomes.SetPushOutcome(ctx, rep.GetPushId(), promoted, img.ID, why); oerr != nil {
			s.log().Warn("registry: could not keep a push outcome", "push", rep.GetPushId(), "err", oerr)
		}
	}
}

// PullReport implements agentlink.RegistrySink: the pull goes to the records.
func (s *Sink) PullReport(ctx context.Context, machineID string, rep *agentv1.PullReport) {
	if s.Recorder == nil {
		return
	}
	var src registry.Source
	switch rep.GetSource() {
	case agentv1.PullReport_SOURCE_LOCAL:
		src = registry.SourceLocal
	case agentv1.PullReport_SOURCE_PULLED:
		src = registry.SourcePulled
	case agentv1.PullReport_SOURCE_BUILT:
		src = registry.SourceBuilt
	default:
		return
	}
	state := map[agentv1.PullReport_State]string{
		agentv1.PullReport_STATE_PULLING: "pulling", agentv1.PullReport_STATE_WAITING_CONTROLLER: "waiting_controller",
		agentv1.PullReport_STATE_DONE: "done", agentv1.PullReport_STATE_FAILED: "failed",
	}[rep.GetState()]
	if state == "" {
		return
	}
	at := time.Now()
	if ms := rep.GetAtUnixMs(); ms > 0 {
		at = time.UnixMilli(ms)
	}
	err := s.Recorder.RecordPull(ctx, registry.PullRecord{
		PullID: rep.GetPullId(), Arch: rep.GetArch(), MachineID: machineID, Key: registry.Key(rep.GetKey()),
		Manifest: rep.GetManifestDigest(), Source: src, State: state, BytesDone: rep.GetBytesDone(), BytesTotal: rep.GetBytesTotal(),
		Seconds: rep.GetSeconds(), Repository: rep.GetRepository(), JobID: rep.GetJobId(), Error: rep.GetError(), At: at,
	})
	if err != nil {
		s.log().Warn("registry: could not keep a pull report", "pull", rep.GetPullId(), "err", err)
	}
}

// firstPromotion is true once per push id, so a resent PUSHED report never
// promotes twice nor turns a promotion into a refusal.
func (s *Sink) firstPromotion(id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.seen == nil {
		s.seen = map[string]bool{}
	}
	if s.seen[id] {
		return false
	}
	s.seen[id] = true
	s.ring = append(s.ring, id)
	if len(s.ring) > seenPushes {
		delete(s.seen, s.ring[0])
		s.ring = s.ring[1:]
	}
	return true
}

func (s *Sink) forget(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.seen, id)
}
