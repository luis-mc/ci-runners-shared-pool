package main

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"net/url"
	"strings"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/internal/machines"
	"github.com/luis-mc/ci-runners-shared-pool/internal/tray"
	"github.com/luis-mc/ci-runners-shared-pool/internal/trayserver"
	"github.com/luis-mc/ci-runners-shared-pool/internal/update"
	"github.com/luis-mc/ci-runners-shared-pool/internal/webui"
)

// LinkIssuer makes a one-time sign-in link for an OS user of this machine
// (internal/auth Service.IssueLinkForOSUser and CreateBindTicket sit behind it).
// The controller wires it in newAuthArea; a nil value (tests) gives plain addresses.
type LinkIssuer interface {
	LinkFor(ctx context.Context, osUser, target string) (string, error)
}

// trayBackend answers the tray's local channel from the update manager, the
// machines drain and the console address (tray R5.8, R5.29).
type trayBackend struct {
	Updates *update.Manager
	Drain   machineDrainer
	Console string // http(s)://host:port of the console
	Links   LinkIssuer
	// Digest returns the newest weekly insights digest file, "" when none (ci-run-insights R5.38); nil is none.
	Digest func() string
}

func (b trayBackend) response(ctx context.Context, st update.Status) tray.Response {
	on := st.AutoUpdate
	return tray.Response{
		State: tray.StateHealthy, Running: len(st.RunningJobs), Version: st.Current, AutoUpdate: &on,
		LatestVersion: st.Latest, ReleaseURL: st.ReleaseURL, UpdateAvailable: st.Available, UpdateState: string(st.State),
		LogDir: tray.LogDir(), Stage: stageInfo(st.Stage),
		Policy: b.policy(ctx), RunnerProblems: b.runnerProblems(ctx), DigestPath: b.digestPath(),
	}
}

func (b trayBackend) digestPath() string {
	if b.Digest == nil {
		return ""
	}
	return b.Digest()
}

func stageInfo(s *update.Stage) *tray.StageInfo {
	if s == nil {
		return nil
	}
	return &tray.StageInfo{Name: s.Name, Percent: s.Percent, Version: s.Version, Error: s.Error, LogPath: s.LogPath, WaitingFor: s.WaitingFor, FailedAt: s.FailedAt, StartedBy: s.StartedBy}
}

// runnerProblems are the runner services of this machine that did not start
// (runners R5.89); none before the machine joined.
func (b trayBackend) runnerProblems(ctx context.Context) []tray.RunnerProblem {
	v, ok := b.Drain.policy(ctx)
	if !ok {
		return nil
	}
	var out []tray.RunnerProblem
	for _, p := range v.RunnerProblems() {
		out = append(out, tray.RunnerProblem{Runner: p.Runner, Service: p.Service, Account: p.Account, Reason: p.Problem})
	}
	return out
}

// policy is this machine's mode and acceptance; nil before the machine joined.
func (b trayBackend) policy(ctx context.Context) *tray.PolicyInfo {
	v, ok := b.Drain.policy(ctx)
	if !ok {
		return nil
	}
	p := &tray.PolicyInfo{
		Mode: v.Settings.EffectiveMode(), Accepting: v.Policy.Accepting, PauseReason: v.Policy.PauseReason, OnBattery: v.Policy.OnBattery,
		UntilResumed: v.Settings.PausedUntilResumed,
	}
	if !v.Settings.PauseUntil.IsZero() && v.Settings.PauseUntil.After(time.Now()) {
		p.PauseUntilMs = v.Settings.PauseUntil.UnixMilli()
	}
	return p
}

func trayActor(c trayserver.Caller) string { return "tray:" + c.OSUser }

// SetMode sets this machine's mode (tray R5.42).
func (b trayBackend) SetMode(ctx context.Context, c trayserver.Caller, mode string) (tray.Response, error) {
	if err := b.Drain.setPolicy(ctx, trayActor(c), machines.SettingsUpdate{Mode: &mode}); err != nil {
		return tray.Response{}, err
	}
	return b.Status(ctx)
}

// Pause pauses new jobs for one hour or until Resume (tray R5.42).
func (b trayBackend) Pause(ctx context.Context, c trayserver.Caller, kind string) (tray.Response, error) {
	if kind != machines.PauseOneHour && kind != machines.PauseUntilResumedValue {
		return tray.Response{}, errors.New("a pause is one_hour or until_resumed")
	}
	if err := b.Drain.setPolicy(ctx, trayActor(c), machines.SettingsUpdate{Pause: &kind}); err != nil {
		return tray.Response{}, err
	}
	return b.Status(ctx)
}

// Resume ends a pause from the tray and, when the mode is Paused, returns it to
// Automatic (tray R5.42).
func (b trayBackend) Resume(ctx context.Context, c trayserver.Caller) (tray.Response, error) {
	none := machines.PauseNone
	u := machines.SettingsUpdate{Pause: &none}
	if v, ok := b.Drain.policy(ctx); ok && v.Settings.EffectiveMode() == machines.ModePaused {
		auto := machines.ModeAutomatic
		u.Mode = &auto
	}
	if err := b.Drain.setPolicy(ctx, trayActor(c), u); err != nil {
		return tray.Response{}, err
	}
	return b.Status(ctx)
}

func (b trayBackend) Status(ctx context.Context) (tray.Response, error) {
	return b.response(ctx, b.Updates.Status(ctx)), nil
}

func (b trayBackend) Check(ctx context.Context) (tray.Response, error) {
	st, err := b.Updates.Check(ctx)
	if err != nil {
		return tray.Response{}, err
	}
	return b.response(ctx, st), nil
}

// Install is the tray's Install now: the person said yes in the tray dialog, which named
// the running jobs, so the update is confirmed. It drains first and cuts no job.
func (b trayBackend) Install(ctx context.Context, c trayserver.Caller) (tray.Response, error) {
	// The download runs inside Apply and can outlast the channel's request time, so Apply runs on
	// its own context. The answer comes when Apply returns (a refusal), or after a moment with
	// the stage it has reached (the window follows the stage from there).
	type result struct {
		st  update.Status
		err error
	}
	done := make(chan result, 1)
	go func() {
		st, err := b.Updates.Apply(context.WithoutCancel(ctx), update.KindUpdate, true, trayActor(c))
		done <- result{st, err}
	}()
	select {
	case r := <-done:
		if r.err != nil {
			return tray.Response{}, r.err
		}
		return b.response(ctx, r.st), nil
	case <-time.After(installAnswerAfter):
		return b.response(ctx, b.Updates.Status(ctx)), nil
	case <-ctx.Done():
		return tray.Response{}, ctx.Err()
	}
}

// installAnswerAfter is how long Install waits for Apply before it answers with the stage.
const installAnswerAfter = 1500 * time.Millisecond

// Cancel is the tray's Cancel: it stops an update that downloads or waits for jobs (D15).
func (b trayBackend) Cancel(ctx context.Context, c trayserver.Caller) (tray.Response, error) {
	st, err := b.Updates.Cancel(ctx, trayActor(c))
	if err != nil {
		return tray.Response{}, err
	}
	return b.response(ctx, st), nil
}

// Link returns a one-time sign-in link when sign-in is wired. Until it is, the
// console has no sign-in, so the link is the console address on the target
// path; it carries no secret and signs nobody in.
func (b trayBackend) Link(ctx context.Context, c trayserver.Caller, target string) (string, error) {
	if b.Links != nil {
		return b.Links.LinkFor(ctx, c.OSUser, target)
	}
	u, err := url.Parse(b.Console)
	if err != nil {
		return "", errors.New("the console address is not valid")
	}
	if p := target; len(p) > 0 && p[0] == '/' && (len(p) == 1 || p[1] != '/') {
		u.Path = p
	}
	return u.String(), nil
}

// consoleURL turns the listen address into the address a browser on this machine uses.
func consoleURL(listen string) string {
	host, port, err := net.SplitHostPort(listen)
	if err != nil || host == "" || host == "0.0.0.0" || host == "::" || host == "[::]" {
		host = "127.0.0.1"
	}
	if port == "" {
		port = "8460"
	}
	return consoleScheme(host) + "://" + net.JoinHostPort(host, port)
}

// consoleScheme is http on 127.0.0.1 and ::1 and https on every other address
// (web-console R5.1).
func consoleScheme(host string) string {
	if webui.IsLoopback(host) {
		return "http"
	}
	return "https"
}

// startTrayChannel serves the local channel until ctx ends. A failure to open
// it is logged and does not stop the controller: the tray then shows its
// stopped state and the console works as before.
func startTrayChannel(ctx context.Context, logger *slog.Logger, b trayBackend) {
	ln, err := trayserver.Listen(trayserver.DefaultPath(), logger)
	if err != nil {
		logger.Warn("tray channel is off", "err", err)
		return
	}
	go func() {
		if err := (&trayserver.Server{Backend: b, Logger: logger.With("component", "tray-channel")}).Serve(ctx, ln); err != nil {
			logger.Warn("tray channel stopped", "err", err)
		}
	}()
}

// loopbackConsoleURL is the plain-http loopback origin on the port of console.
func loopbackConsoleURL(console string) string {
	_, port, err := net.SplitHostPort(strings.TrimPrefix(strings.TrimPrefix(console, "https://"), "http://"))
	if err != nil || port == "" {
		port = "8460"
	}
	return "http://" + net.JoinHostPort("127.0.0.1", port)
}
