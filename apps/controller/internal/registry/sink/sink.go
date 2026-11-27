// Package sink answers the registry calls of agents on the controller (protocol
// version 3, docs/features/registry R5.13 to R5.20). It turns the agent link
// messages into the registry interfaces: the Locator answers "is key K there",
// the TokenIssuer gives a token, the Recorder keeps the pull and push reports
// and the Promoter promotes a pushed image. It is the join (REG11) of REG1,
// REG2 and REG4; every part is optional, and a missing part gives the answer a
// controller without a registry gives.
package sink

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/agentlink"
	agentv1 "github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/gen/agent/v1"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/profiles"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/registry"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/registry/keys"
)

// busyRetry is what a busy controller tells the agent to wait.
const busyRetry = 5 * time.Second

// Outcomes tells the records what became of a promoted or refused push.
type Outcomes interface {
	SetPushOutcome(ctx context.Context, pushID string, promoted bool, imageID, why string) error
}

// Profiles reads one profile (IMG3), to know the kind and the base of an image.
type Profiles interface {
	Get(ctx context.Context, id string) (profiles.Profile, error)
}

// Sink implements agentlink.RegistrySink.
type Sink struct {
	Locator  registry.Locator
	Tokens   registry.TokenIssuer
	Recorder registry.Recorder
	Promoter registry.Promoter
	Outcomes Outcomes
	Profiles Profiles
	// Host is host:port of the registry on the controller's HTTPS listener.
	Host   string
	Logger *slog.Logger
	Now    func() time.Time
}

var _ agentlink.RegistrySink = (*Sink)(nil)

func (s *Sink) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

func (s *Sink) log() *slog.Logger {
	if s.Logger != nil {
		return s.Logger
	}
	return slog.Default()
}

// EnsureImage answers "have key K for arch A".
func (s *Sink) EnsureImage(ctx context.Context, machineID string, req *agentv1.EnsureImage) *agentv1.EnsureImageAnswer {
	notFound := &agentv1.EnsureImageAnswer{Outcome: agentv1.EnsureImageAnswer_OUTCOME_NOT_FOUND}
	key := registry.Key(req.GetKey())
	if s.Locator == nil || !key.Valid() {
		return notFound
	}
	ans, err := s.Locator.Ensure(ctx, machineID, key, registry.OCIArch(req.GetArch()))
	if err != nil || ans.Busy {
		if err != nil {
			s.log().Warn("registry: ensure image", "machine", machineID, "key", key.Short(), "err", err)
		}
		return &agentv1.EnsureImageAnswer{Outcome: agentv1.EnsureImageAnswer_OUTCOME_BUSY, RetryAfterMs: busyRetry.Milliseconds()}
	}
	if ans.Found {
		return &agentv1.EnsureImageAnswer{
			Outcome: agentv1.EnsureImageAnswer_OUTCOME_FOUND, Name: ans.Name, ManifestDigest: ans.Manifest,
			SizeBytes: ans.Size, RegistryHost: s.Host,
		}
	}
	// Not in the registry: say where the agent pushes what it builds. Without a
	// token issuer nobody can push, so no staging name is given.
	if s.Tokens != nil {
		if target, ok := s.target(ctx, req); ok {
			notFound.TargetName = target
			notFound.StagingName = registry.IncomingName(machineID, target)
			notFound.RegistryHost = s.Host
		}
	}
	return notFound
}

// target is the name an image of the request is promoted to.
func (s *Sink) target(ctx context.Context, req *agentv1.EnsureImage) (string, bool) {
	in, err := parseKeyInput(req.GetKeyInputJson())
	if err != nil || in.Profile == "" {
		return "", false
	}
	if s.Profiles != nil {
		p, err := s.Profiles.Get(ctx, in.Profile)
		if err == nil && p.Kind == registry.KindCustom {
			owner, repo, ok := strings.Cut(req.GetRepository(), "/")
			if !ok || owner == "" || repo == "" {
				return "", false
			}
			return registry.CustomName(owner, repo, in.Profile), true
		}
		if err != nil && !errors.Is(err, profiles.ErrNotFound) {
			return "", false
		}
	}
	return registry.StdName(in.Profile), true
}

func parseKeyInput(js string) (registry.KeyInput, error) {
	var in registry.KeyInput
	err := json.Unmarshal([]byte(js), &in)
	return in, err
}

// RegistryToken issues a token for a pull or a push.
func (s *Sink) RegistryToken(ctx context.Context, machineID string, req *agentv1.RegistryTokenRequest) *agentv1.RegistryToken {
	if s.Tokens == nil {
		return &agentv1.RegistryToken{Refused: agentlink.ErrNoRegistry}
	}
	var action string
	switch req.GetAction() {
	case agentv1.RegistryTokenRequest_ACTION_PULL:
		action = "pull"
	case agentv1.RegistryTokenRequest_ACTION_PUSH:
		action = "push"
	default:
		return &agentv1.RegistryToken{Refused: "the token request names no action"}
	}
	if len(req.GetNames()) == 0 {
		return &agentv1.RegistryToken{Refused: "the token request names no repository"}
	}
	want := make([]registry.Scope, 0, len(req.GetNames()))
	for _, n := range req.GetNames() {
		want = append(want, registry.Scope{Name: n, Actions: []string{action}})
	}
	tok, err := s.Tokens.Issue(ctx, machineID, want)
	if err != nil {
		// The reason is for the agent's log: never a token or a path.
		return &agentv1.RegistryToken{Refused: err.Error()}
	}
	return &agentv1.RegistryToken{Token: tok.Value, ExpiresAtUnixMs: tok.ExpiresAt.UnixMilli()}
}

// PullReport keeps a pull report.
func (s *Sink) PullReport(ctx context.Context, machineID string, rep *agentv1.PullReport) {
	if s.Recorder == nil {
		return
	}
	src := registry.SourceLocal
	switch rep.GetSource() {
	case agentv1.PullReport_SOURCE_PULLED:
		src = registry.SourcePulled
	case agentv1.PullReport_SOURCE_BUILT:
		src = registry.SourceBuilt
	}
	state := "pulling"
	switch rep.GetState() {
	case agentv1.PullReport_STATE_WAITING_CONTROLLER:
		state = "waiting_controller"
	case agentv1.PullReport_STATE_DONE:
		state = "done"
	case agentv1.PullReport_STATE_FAILED:
		state = "failed"
	}
	at := s.now()
	if ms := rep.GetAtUnixMs(); ms > 0 {
		at = time.UnixMilli(ms)
	}
	err := s.Recorder.RecordPull(ctx, registry.PullRecord{
		PullID: rep.GetPullId(), Arch: registry.OCIArch(rep.GetArch()), MachineID: machineID, Key: registry.Key(rep.GetKey()),
		Manifest: rep.GetManifestDigest(), Source: src, State: state, BytesDone: rep.GetBytesDone(), BytesTotal: rep.GetBytesTotal(),
		Seconds: rep.GetSeconds(), Repository: rep.GetRepository(), JobID: rep.GetJobId(), Error: rep.GetError(), At: at,
	})
	if err != nil {
		s.log().Warn("registry: record a pull", "machine", machineID, "err", err)
	}
}

// PushReport keeps a push report and, when the push ended, promotes the image.
func (s *Sink) PushReport(ctx context.Context, machineID string, rep *agentv1.PushReport) {
	if s.Recorder == nil {
		return
	}
	state := "pushing"
	switch rep.GetState() {
	case agentv1.PushReport_STATE_WAITING_CONTROLLER:
		state = "waiting_controller"
	case agentv1.PushReport_STATE_PUSHED:
		state = "pushed"
	case agentv1.PushReport_STATE_FAILED:
		state = "failed"
	}
	in, _ := parseKeyInput(rep.GetKeyInputJson())
	staged := registry.Staged{
		MachineID: machineID, Name: rep.GetStagingName(), Target: rep.GetTargetName(), Key: registry.Key(rep.GetKey()),
		Manifest: rep.GetManifestDigest(), KeyInput: in, Source: rep.GetRepository(), Commit: rep.GetCommitSha(), Branch: rep.GetBranch(),
	}
	err := s.Recorder.RecordPush(ctx, registry.PushRecord{
		Staged: staged, PushID: rep.GetPushId(), State: state, Seconds: rep.GetSeconds(), Error: rep.GetError(), At: s.now(),
	})
	if err != nil {
		s.log().Warn("registry: record a push", "machine", machineID, "err", err)
		return
	}
	if state != "pushed" || s.Promoter == nil {
		return
	}
	s.promote(ctx, rep.GetPushId(), staged)
}

func (s *Sink) promote(ctx context.Context, pushID string, st registry.Staged) {
	st.Labels = s.labels(ctx, st)
	img, err := s.Promoter.Promote(ctx, st)
	if err != nil {
		// A refusal is not an error of the controller: it shows in the console
		// and the image stays in staging until the cleanup.
		s.log().Info("registry: promotion refused", "machine", st.MachineID, "key", st.Key.Short(), "why", err.Error())
	}
	if s.Outcomes != nil {
		why := ""
		if err != nil {
			why = err.Error()
		}
		if oerr := s.Outcomes.SetPushOutcome(ctx, pushID, err == nil, img.ID, why); oerr != nil {
			s.log().Warn("registry: record a push outcome", "err", oerr)
		}
	}
}

// labels are the labels the image of the push must carry (R5.7), the same the
// agent put on it at the build.
func (s *Sink) labels(ctx context.Context, st registry.Staged) map[string]string {
	ref := registry.ProfileRef{ID: st.KeyInput.Profile, BaseDigest: st.KeyInput.BaseDigest}
	if s.Profiles != nil {
		if p, err := s.Profiles.Get(ctx, st.KeyInput.Profile); err == nil {
			ref = p.Ref()
			if ref.BaseDigest == "" {
				ref.BaseDigest = st.KeyInput.BaseDigest
			}
		}
	}
	return keys.LabelsFor(st.Key, st.KeyInput, ref, st.Source, st.Commit)
}
