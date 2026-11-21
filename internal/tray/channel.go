package tray

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/internal/update"
)

// Request is one JSON line sent over the local channel (spec section 7).
type Request struct {
	Call   string `json:"call"`             // status | link | check | install | cancel | mode | pause | resume
	Target string `json:"target,omitempty"` // call=link: the console path to open, "" for the start page
	Mode   string `json:"mode,omitempty"`   // call=mode: automatic, always or paused
	Pause  string `json:"pause,omitempty"`  // call=pause: one_hour or until_resumed
}

// PolicyInfo is what the service says about this machine's mode and whether it
// takes new jobs (tray R5.42, machines R5.61 to R5.64). An older service leaves
// it out; the tray then shows no mode and offers none.
type PolicyInfo struct {
	// Mode is automatic, always or paused.
	Mode string `json:"mode"`
	// Accepting is whether the machine takes new jobs; PauseReason is why not:
	// paused, on_battery or budget.
	Accepting   bool   `json:"accepting"`
	PauseReason string `json:"pause_reason,omitempty"`
	OnBattery   bool   `json:"on_battery,omitempty"`
	// PauseUntilMs is the end of a pause from the tray with a timer (Unix ms);
	// UntilResumed is a pause that lasts until Resume.
	PauseUntilMs int64 `json:"pause_until_ms,omitempty"`
	UntilResumed bool  `json:"until_resumed,omitempty"`
}

// StageInfo is the live stage of an update, as the service writes it (internal/update Stage).
// The update window reads it; an older service leaves it out.
type StageInfo struct {
	Name    string `json:"name"` // downloading, waiting_for_jobs, installing, waiting_installer, restarting, done, failed
	Percent int    `json:"percent"`
	Version string `json:"version,omitempty"`
	Error   string `json:"error,omitempty"`
	LogPath string `json:"log_path,omitempty"`
	// WaitingFor names the other installer (waiting_installer only).
	WaitingFor string `json:"waiting_for,omitempty"`
	FailedAt   string `json:"failed_at,omitempty"`
	StartedBy  string `json:"started_by,omitempty"` // auto, tray or console
}

// Response is one JSON line answered by the service.
type Response struct {
	OK      bool   `json:"ok"`
	Error   string `json:"error,omitempty"`
	State   State  `json:"state,omitempty"`
	Running int    `json:"running,omitempty"`
	Waiting int    `json:"waiting,omitempty"`
	Runners int    `json:"runners,omitempty"`
	Fault   string `json:"fault,omitempty"`
	URL     string `json:"url,omitempty"` // call=link
	LogDir  string `json:"log_dir,omitempty"`

	// Version and update fields (operations R5.11 to R5.18). An older service
	// leaves them out; the tray then shows no update state and assumes auto-update on.
	Version         string `json:"version,omitempty"`
	AutoUpdate      *bool  `json:"auto_update,omitempty"`
	LatestVersion   string `json:"latest_version,omitempty"`
	UpdateAvailable bool   `json:"update_available,omitempty"`
	UpdateState     string `json:"update_state,omitempty"`
	ReleaseURL      string `json:"release_url,omitempty"` // the release page of LatestVersion (What changed)

	// Stage is the live update stage; nil when no update ran.
	Stage *StageInfo `json:"stage,omitempty"`

	// Policy is this machine's mode and acceptance (CRSP75).
	Policy *PolicyInfo `json:"policy,omitempty"`

	// RunnerProblems are the runner services of this machine that did not
	// start after the agent gave their account the logon right again and
	// tried once more (runners R5.89). An older service leaves it out.
	RunnerProblems []RunnerProblem `json:"runner_problems,omitempty"`

	// DigestPath is the newest weekly CI insights digest, a draft file on the controller's disk (ci-run-insights
	// R5.38). It is "" until one is written; an older service leaves it out.
	DigestPath string `json:"digest_path,omitempty"`
}

// RunnerProblem is one runner service that did not start.
type RunnerProblem struct {
	Runner  string `json:"runner"`
	Service string `json:"service"`
	Account string `json:"account,omitempty"`
	// Reason is why Windows did not start it, as the agent reported it.
	Reason string `json:"reason"`
}

// Dialer opens the local channel: a named pipe on Windows, a Unix socket elsewhere.
type Dialer func(ctx context.Context) (net.Conn, error)

// Client talks to the local service. It never blocks longer than Timeout (R6.6).
type Client struct {
	Dial    Dialer
	Timeout time.Duration
}

// NewClient returns a client for the OS default channel path.
func NewClient() *Client {
	return &Client{Dial: DefaultDialer(), Timeout: 3 * time.Second}
}

// Call sends one request and reads one response line.
func (c *Client) Call(ctx context.Context, call string) (Response, error) {
	return c.call(ctx, Request{Call: call})
}

func (c *Client) call(ctx context.Context, req Request) (Response, error) {
	to := c.Timeout
	if to <= 0 {
		to = 3 * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, to)
	defer cancel()
	conn, err := c.Dial(ctx)
	if err != nil {
		return Response{}, err
	}
	defer func() { _ = conn.Close() }()
	if dl, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(dl)
	}
	// Unblock reads when the context ends (pipes may not honour deadlines).
	done := make(chan struct{})
	defer close(done)
	go func() {
		select {
		case <-ctx.Done():
			_ = conn.Close()
		case <-done:
		}
	}()
	if err := json.NewEncoder(conn).Encode(req); err != nil {
		return Response{}, err
	}
	line, err := bufio.NewReader(conn).ReadBytes('\n')
	if err != nil && (!errors.Is(err, io.EOF) || len(line) == 0) {
		if ctx.Err() != nil {
			return Response{}, ctx.Err()
		}
		return Response{}, err
	}
	var r Response
	if err := json.Unmarshal(line, &r); err != nil {
		return Response{}, fmt.Errorf("local channel: bad answer: %w", err)
	}
	if !r.OK {
		if r.Error == update.ErrNoUpdate.Error() {
			return r, update.ErrNoUpdate // a plain answer, not a failure: no internal prefix (TRAY8)
		}
		return r, fmt.Errorf("local channel: %s", r.Error)
	}
	return r, nil
}

// Status asks the service for its status.
func (c *Client) Status(ctx context.Context) (Status, error) {
	r, err := c.Call(ctx, "status")
	if err != nil {
		return Status{}, err
	}
	return statusFrom(r), nil
}

func statusFrom(r Response) Status {
	st := r.State
	if st == "" {
		st = StateHealthy
	}
	out := Status{
		State: st, Running: r.Running, Waiting: r.Waiting, Runners: r.Runners, Fault: r.Fault, Version: r.Version,
		Update: UpdateInfo{Latest: r.LatestVersion, Available: r.UpdateAvailable, State: r.UpdateState, ReleaseURL: r.ReleaseURL, Jobs: r.Running, AutoOff: r.AutoUpdate != nil && !*r.AutoUpdate},
	}
	out.Update.Stage = r.Stage
	out.RunnerProblems = r.RunnerProblems
	out.DigestPath = r.DigestPath
	if p := r.Policy; p != nil {
		out.Policy = MachinePolicy{
			Known: true, Mode: p.Mode, Accepting: p.Accepting, PauseReason: p.PauseReason, OnBattery: p.OnBattery,
			UntilResumed: p.UntilResumed,
		}
		if p.PauseUntilMs > 0 {
			out.Policy.PauseUntil = time.UnixMilli(p.PauseUntilMs)
		}
	}
	return out
}

// SetMode asks the service to set this machine's mode: automatic, always or
// paused. The console shows the change at once (tray R5.42).
func (c *Client) SetMode(ctx context.Context, mode string) (Status, error) {
	r, err := c.call(ctx, Request{Call: "mode", Mode: mode})
	if err != nil {
		return Status{}, err
	}
	return statusFrom(r), nil
}

// Pause asks the service to pause new jobs on this machine: for one hour
// (PauseOneHour) or until Resume (PauseUntilResumed).
func (c *Client) Pause(ctx context.Context, kind string) (Status, error) {
	r, err := c.call(ctx, Request{Call: "pause", Pause: kind})
	if err != nil {
		return Status{}, err
	}
	return statusFrom(r), nil
}

// Resume asks the service to end a pause from the tray, and a Paused mode.
func (c *Client) Resume(ctx context.Context) (Status, error) {
	r, err := c.call(ctx, Request{Call: "resume"})
	if err != nil {
		return Status{}, err
	}
	return statusFrom(r), nil
}

// Check asks the service to look for a newer stable release now (Look for updates).
func (c *Client) Check(ctx context.Context) (Status, error) {
	r, err := c.Call(ctx, "check")
	if err != nil {
		return Status{}, err
	}
	return statusFrom(r), nil
}

// Install asks the service to install the newest stable release (Install now).
// It drains first: no running job is cut. Only a person's yes in the tray sends it.
func (c *Client) Install(ctx context.Context) (Status, error) {
	r, err := c.Call(ctx, "install")
	if err != nil {
		return Status{}, err
	}
	return statusFrom(r), nil
}

// Cancel stops an update that is downloading or waiting for jobs; nothing has changed then
// (D15). The service refuses from installing on.
func (c *Client) Cancel(ctx context.Context) (Status, error) {
	r, err := c.Call(ctx, "cancel")
	if err != nil {
		return Status{}, err
	}
	return statusFrom(r), nil
}

// Link asks the service for a one-time sign-in URL (R5.6).
func (c *Client) Link(ctx context.Context) (string, error) { return c.LinkTo(ctx, "") }

// LinkTo asks for a one-time link that lands on a console path, for example the
// restart or update Review step (tray R5.15). The service signs the user in as
// for Open; the path is only where the console goes after sign-in.
func (c *Client) LinkTo(ctx context.Context, target string) (string, error) {
	r, err := c.call(ctx, Request{Call: "link", Target: target})
	if err != nil {
		return "", err
	}
	if r.URL == "" {
		return "", errors.New("local channel: no link in answer")
	}
	return r.URL, nil
}
