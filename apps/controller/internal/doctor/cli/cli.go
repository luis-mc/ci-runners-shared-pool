// Package cli is `crsp doctor` (doctor R5.21 to R5.23): it asks the controller
// for a run, waits for it and prints the findings.
//
// Exit codes, owner decision in the CRSP23 brief: 1 when the worst finding is
// a fail, 0 for warn and below, 3 when the run could not happen (bad flags,
// controller not reachable, not signed in). The spec text of R5.22 (warn
// exits 1, fail exits 2) is superseded; a change note says so.
//
// `--local` (R5.24) is not built in this milestone slice: it needs the agent's
// OS probes and says so.
//
// Ported from dev-standards delivery_doctor.py main and test_doctor.py
// test_cli_exit_codes_and_output.
package cli

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/api/gen"
)

// Exit codes.
const (
	ExitOK        = 0
	ExitFail      = 1
	ExitCannotRun = 3
)

// Client is the controller as the command sees it.
type Client interface {
	// StartRun posts a run and returns it as started.
	StartRun(ctx context.Context, scope gen.DoctorScope) (gen.DoctorRun, error)
	// GetRun reads a run with its findings.
	GetRun(ctx context.Context, id string) (gen.DoctorRun, error)
	// ResolveRepositories turns OWNER/NAME into repository IDs.
	ResolveRepositories(ctx context.Context, fullNames []string) ([]int64, error)
}

// Options are the knobs a caller can set besides the flags.
type Options struct {
	// Poll is the gap between reads of a running run; zero means 500 ms.
	Poll time.Duration
	// Timeout bounds the wait; zero means 5 minutes.
	Timeout time.Duration
}

type multi []string

func (m *multi) String() string     { return strings.Join(*m, ",") }
func (m *multi) Set(v string) error { *m = append(*m, v); return nil }

var rank = map[string]int{"ok": 0, "info": 1, "skip": 2, "warn": 3, "fail": 4}

// Run runs the command with args (without "doctor") and returns the exit code.
func Run(ctx context.Context, args []string, stdout, stderr io.Writer, c Client, opt Options) int {
	fs := flag.NewFlagSet("crsp doctor", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var machines, repos, checks multi
	fs.Var(&machines, "machine", "check only this machine (name or ID); may repeat")
	fs.Var(&repos, "repo", "check only this repository, OWNER/NAME; may repeat")
	fs.Var(&checks, "check", "run only checks with this ID prefix; may repeat")
	minSev := fs.String("min-severity", "warn", "show findings at or above this severity: info, warn or fail")
	asJSON := fs.Bool("json", false, "print one JSON object with every finding")
	local := fs.Bool("local", false, "run the checks of this machine without the controller (not built yet)")
	if err := fs.Parse(args); err != nil {
		return ExitCannotRun
	}
	if fs.NArg() > 0 {
		_, _ = fmt.Fprintf(stderr, "crsp doctor: unexpected argument %q\n", fs.Arg(0))
		return ExitCannotRun
	}
	if *minSev != "info" && *minSev != "warn" && *minSev != "fail" {
		_, _ = fmt.Fprintf(stderr, "crsp doctor: --min-severity must be info, warn or fail, not %q\n", *minSev)
		return ExitCannotRun
	}
	if *local {
		_, _ = fmt.Fprintln(stderr, "crsp doctor: --local is not built yet; run `crsp doctor` against the controller")
		return ExitCannotRun
	}

	scope := gen.DoctorScope{}
	if len(machines) > 0 {
		m := []string(machines)
		scope.Machines = &m
	}
	if len(checks) > 0 {
		c := []string(checks)
		scope.Checks = &c
	}
	if len(repos) > 0 {
		ids, err := c.ResolveRepositories(ctx, repos)
		if err != nil {
			_, _ = fmt.Fprintln(stderr, "crsp doctor:", err)
			return ExitCannotRun
		}
		scope.Repositories = &ids
	}

	run, err := c.StartRun(ctx, scope)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "crsp doctor:", err)
		return ExitCannotRun
	}
	run, err = wait(ctx, c, run, opt)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "crsp doctor:", err)
		return ExitCannotRun
	}
	if run.State == gen.DoctorRunStateFailed {
		_, _ = fmt.Fprintln(stderr, "crsp doctor: the run failed on the controller")
		return ExitCannotRun
	}

	if *asJSON {
		b, err := json.MarshalIndent(JSON(run), "", "  ")
		if err != nil {
			_, _ = fmt.Fprintln(stderr, "crsp doctor:", err)
			return ExitCannotRun
		}
		_, _ = fmt.Fprintln(stdout, string(b))
	} else {
		Text(stdout, run, *minSev)
	}
	if run.Worst != nil && *run.Worst == gen.SeverityFail {
		return ExitFail
	}
	return ExitOK
}

func wait(ctx context.Context, c Client, run gen.DoctorRun, opt Options) (gen.DoctorRun, error) {
	poll, timeout := opt.Poll, opt.Timeout
	if poll <= 0 {
		poll = 500 * time.Millisecond
	}
	if timeout <= 0 {
		timeout = 5 * time.Minute
	}
	deadline := time.Now().Add(timeout)
	for run.State == gen.DoctorRunStateRunning {
		if time.Now().After(deadline) {
			return run, errors.New("the doctor run did not finish in time")
		}
		select {
		case <-ctx.Done():
			return run, ctx.Err()
		case <-time.After(poll):
		}
		var err error
		if run, err = c.GetRun(ctx, run.Id); err != nil {
			return run, err
		}
	}
	return run, nil
}

// Text prints the findings at or above minSev, worst first, one block each
// with its fix (R5.21). Skips are counted in the footer when hidden.
func Text(w io.Writer, run gen.DoctorRun, minSev string) {
	shown, hiddenSkips := 0, 0
	findings := append([]gen.DoctorFinding(nil), run.Findings...)
	sort.SliceStable(findings, func(i, j int) bool { return rank[string(findings[i].Severity)] > rank[string(findings[j].Severity)] })
	for _, f := range findings {
		sev := string(f.Severity)
		if rank[sev] < rank[minSev] {
			if sev == "skip" {
				hiddenSkips++
			}
			continue
		}
		shown++
		_, _ = fmt.Fprintf(w, "%-5s %s  %s\n", strings.ToUpper(sev), f.Check, f.Subject.Name)
		_, _ = fmt.Fprintf(w, "      %s\n", f.Title)
		if f.Detail != nil && *f.Detail != "" {
			for _, line := range strings.Split(*f.Detail, "\n") {
				_, _ = fmt.Fprintf(w, "      %s\n", line)
			}
		}
		if f.Items != nil {
			detail := ""
			if f.Detail != nil {
				detail = *f.Detail
			}
			for _, it := range *f.Items {
				// The detail often says the item already; list only what it does not.
				if !strings.Contains(detail, it.Name) {
					_, _ = fmt.Fprintf(w, "      - %s %s: %s\n", it.Kind, it.Name, it.Why)
				}
			}
		}
		if f.Severity != gen.SeverityOk {
			_, _ = fmt.Fprintf(w, "      Fix: %s\n", f.Fix.Text)
			if f.Fix.Command != nil && *f.Fix.Command != "" {
				_, _ = fmt.Fprintf(w, "      Run: %s\n", *f.Fix.Command)
			}
			if f.Fix.Link != nil && *f.Fix.Link != "" {
				_, _ = fmt.Fprintf(w, "      See: %s\n", *f.Fix.Link)
			}
			if f.NeedsAdmin {
				_, _ = fmt.Fprintln(w, "      Needs administrator rights on the machine.")
			}
		}
		_, _ = fmt.Fprintln(w)
	}
	if shown == 0 {
		_, _ = fmt.Fprintf(w, "Nothing at %s or worse.\n", minSev)
	}
	_, _ = fmt.Fprintf(w, "Run %s: %s. Worst: %s.", run.Id, counts(run), worst(run))
	if hiddenSkips > 0 {
		_, _ = fmt.Fprintf(w, " %d skipped (--min-severity info shows them).", hiddenSkips)
	}
	_, _ = fmt.Fprintln(w)
}

func worst(run gen.DoctorRun) string {
	if run.Worst == nil {
		return "ok"
	}
	return string(*run.Worst)
}

func counts(run gen.DoctorRun) string {
	var parts []string
	for _, s := range []string{"fail", "warn", "skip", "info", "ok"} {
		if n := run.Counts[s]; n > 0 {
			parts = append(parts, fmt.Sprintf("%d %s", n, s))
		}
	}
	if len(parts) == 0 {
		return "no findings"
	}
	return strings.Join(parts, ", ")
}

// JSONRun is the document of spec 7.4.
type JSONRun struct {
	Schema     int            `json:"schema"`
	RunID      string         `json:"run_id"`
	Trigger    string         `json:"trigger"`
	Scope      JSONScope      `json:"scope"`
	StartedAt  time.Time      `json:"started_at"`
	FinishedAt *time.Time     `json:"finished_at"`
	Worst      string         `json:"worst"`
	Counts     map[string]int `json:"counts"`
	Findings   []JSONFinding  `json:"findings"`
}

// JSONScope lists what the run was limited to; empty lists mean everything.
type JSONScope struct {
	Machines []string `json:"machines"`
	Repos    []int64  `json:"repos"`
	Checks   []string `json:"checks"`
}

// JSONFinding is one finding in the JSON document.
type JSONFinding struct {
	Check      string      `json:"check"`
	Subject    JSONSubject `json:"subject"`
	Severity   string      `json:"severity"`
	Title      string      `json:"title"`
	Detail     string      `json:"detail"`
	Fix        JSONFix     `json:"fix"`
	NeedsAdmin bool        `json:"needs_admin"`
	// Items and Action are set by a finding that lists things and offers a button (R5.48).
	Items     []gen.RunnerLeftover `json:"items,omitempty"`
	Action    *JSONAction          `json:"action,omitempty"`
	Milestone int                  `json:"milestone"`
	CheckedAt time.Time            `json:"checked_at"`
}

// JSONAction is the one button a finding offers.
type JSONAction struct {
	ID    string `json:"id"`
	Label string `json:"label"`
}

// JSONSubject is the subject of a finding.
type JSONSubject struct {
	Kind string `json:"kind"`
	ID   string `json:"id"`
	Name string `json:"name"`
}

// JSONFix is the fix of a finding; command and link are null when absent.
type JSONFix struct {
	Text    string  `json:"text"`
	Command *string `json:"command"`
	Link    *string `json:"link"`
}

// JSON converts a run to the document of spec 7.4. Every finding is in it,
// whatever --min-severity says (R5.23).
func JSON(run gen.DoctorRun) JSONRun {
	out := JSONRun{
		Schema: 1, RunID: run.Id, Trigger: string(run.Trigger), StartedAt: run.StartedAt, FinishedAt: run.FinishedAt,
		Worst: worst(run), Counts: run.Counts, Findings: make([]JSONFinding, 0, len(run.Findings)),
		Scope: JSONScope{Machines: []string{}, Repos: []int64{}, Checks: []string{}},
	}
	if out.Counts == nil {
		out.Counts = map[string]int{}
	}
	if s := run.Scope; s != nil {
		if s.Machines != nil {
			out.Scope.Machines = *s.Machines
		}
		if s.Repositories != nil {
			out.Scope.Repos = *s.Repositories
		}
		if s.Checks != nil {
			out.Scope.Checks = *s.Checks
		}
	}
	for _, f := range run.Findings {
		jf := JSONFinding{
			Check: f.Check, Subject: JSONSubject{Kind: f.Subject.Kind, ID: f.Subject.Id, Name: f.Subject.Name},
			Severity: string(f.Severity), Title: f.Title, NeedsAdmin: f.NeedsAdmin, Milestone: f.Milestone, CheckedAt: f.CheckedAt,
			Fix: JSONFix{Text: f.Fix.Text, Command: f.Fix.Command, Link: f.Fix.Link},
		}
		if f.Detail != nil {
			jf.Detail = *f.Detail
		}
		if f.Items != nil {
			jf.Items = *f.Items
		}
		if f.Action != nil {
			jf.Action = &JSONAction{ID: string(f.Action.Id), Label: f.Action.Label}
		}
		out.Findings = append(out.Findings, jf)
	}
	return out
}
