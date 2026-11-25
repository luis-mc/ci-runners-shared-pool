// Package cli is the crsp command line for people and scripts (feature spec
// cli). It talks to the controller only through the client generated from
// packages/contract/openapi.yaml (R5.2) and never prints a secret (R6.1).
//
// Command library: the standard flag package with a small command table
// (decision D4 in docs/features/cli). It adds no dependency, and completion
// scripts are written here from the table.
package cli

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/internal/secrets"
)

// Exit codes (cli R5.12).
const (
	ExitOK           = 0
	ExitFailed       = 1
	ExitUsage        = 2
	ExitAuth         = 3
	ExitNotFound     = 4
	ExitNotConfirmed = 5
	ExitUnreachable  = 6
	ExitConflict     = 7
	ExitFindings     = 8
)

// ModeFunc runs a long-lived mode such as controller or agent.
type ModeFunc func(ctx context.Context, args []string) error

// Env is everything the CLI takes from its surroundings, so tests can replace
// each part.
type Env struct {
	Stdin  io.Reader
	Stdout io.Writer
	Stderr io.Writer
	Getenv func(string) string
	// IsTerminal says whether stdin is a person at a terminal (R5.14).
	IsTerminal func() bool
	// ConfigDir holds cli.toml and, without an OS store, the secret file.
	ConfigDir string
	// Secrets is the OS secret store; nil means the 0600 file store.
	Secrets secrets.Store
	// OpenBrowser opens a URL on a desktop; nil or an error means print only.
	OpenBrowser func(url string) error
	// Now is the clock.
	Now func() time.Time
	// PollEvery overrides every wait between polls (tests).
	PollEvery time.Duration
	// Version and GoVersion for `version`.
	Version, GoVersion string
	// SchemaVersion is the database schema this build carries; `version` prints it (operations R5.13). Zero prints nothing.
	SchemaVersion int64
	// Modes are the built-in long-running modes, registered by cmd/crsp.
	Modes map[string]ModeFunc
	// Context is cancelled on Ctrl+C.
	Context context.Context
	// Uninstall replaces the machine-touching parts of `crsp uninstall` (tests); nil is the real thing.
	Uninstall *UninstallHooks
}

func (e *Env) defaults() {
	if e.Stdin == nil {
		e.Stdin = strings.NewReader("")
	}
	if e.Stdout == nil {
		e.Stdout = io.Discard
	}
	if e.Stderr == nil {
		e.Stderr = io.Discard
	}
	if e.Getenv == nil {
		e.Getenv = os.Getenv
	}
	if e.IsTerminal == nil {
		e.IsTerminal = func() bool { return false }
	}
	if e.Now == nil {
		e.Now = time.Now
	}
	if e.Context == nil {
		e.Context = context.Background()
	}
	if e.ConfigDir == "" {
		if d, err := os.UserConfigDir(); err == nil {
			e.ConfigDir = filepath.Join(d, "crsp")
		} else {
			e.ConfigDir = ".crsp"
		}
	}
	if e.Secrets == nil {
		e.Secrets = &FileStore{Path: filepath.Join(e.ConfigDir, "secrets.json")}
	}
}

// exitError carries an exit code and the message to print.
type exitError struct {
	Code    int
	Message string
	// Envelope is the controller's error, kept for --json.
	Envelope []byte
}

func (e *exitError) Error() string { return e.Message }

func fail(code int, format string, a ...any) error {
	return &exitError{Code: code, Message: fmt.Sprintf(format, a...)}
}

// global holds the flags every command accepts.
type global struct {
	context    string
	controller string
	json       bool
	noColor    bool
	timeout    time.Duration
}

func (g *global) bind(fs *flag.FlagSet) {
	fs.StringVar(&g.context, "context", "", "controller context to use")
	fs.StringVar(&g.controller, "controller", "", "controller URL that a context already pins")
	fs.BoolVar(&g.json, "json", false, "print JSON")
	fs.BoolVar(&g.noColor, "no-color", false, "no colour (output never uses colour)")
	fs.DurationVar(&g.timeout, "timeout", 30*time.Second, "timeout for each call")
}

// call is one command run.
type call struct {
	env    *Env
	g      global
	fs     *flag.FlagSet
	cmd    *command
	cached *session
	// after holds the arguments after a bare `--`, for a command with dashdash.
	after []string
	// in is the one reader of stdin for this call, so two questions in a row do not lose input to buffering.
	in *bufio.Reader
}

// stdin returns the call's reader of stdin.
func (c *call) stdin() *bufio.Reader {
	if c.in == nil {
		c.in = bufio.NewReader(c.env.Stdin)
	}
	return c.in
}

// command is a node of the command tree.
type command struct {
	name    string
	summary string
	// milestone is set when the command is not built yet (R5.1).
	milestone string
	subs      []*command
	flags     func(c *call, fs *flag.FlagSet)
	run       func(c *call, args []string) error
	args      string // usage text for positionals
	// raw passes every argument to run without flag parsing (long-running modes).
	raw bool
	// dashdash makes a bare `--` end the flags and the positionals: what follows
	// goes to call.after unparsed (a command to run, never read as flags).
	dashdash bool
}

func (c *command) find(name string) *command {
	for _, s := range c.subs {
		if s.name == name {
			return s
		}
	}
	return nil
}

// Run executes the CLI and returns the process exit code.
func Run(env *Env, args []string) int {
	env.defaults()
	if len(args) > 0 && (args[0] == "--version" || args[0] == "-v") {
		args = []string{"version"}
	}
	root := tree(env)
	cmd, path, rest := resolve(root, args)
	if cmd == nil {
		return usageError(env, root, args)
	}
	return execute(env, root, cmd, path, rest)
}

func resolve(root *command, args []string) (*command, []string, []string) {
	cur := root
	var path []string
	rest := args
	for len(rest) > 0 && !strings.HasPrefix(rest[0], "-") {
		next := cur.find(rest[0])
		if next == nil {
			break
		}
		cur = next
		path = append(path, rest[0])
		rest = rest[1:]
	}
	if cur == root && len(rest) > 0 && !strings.HasPrefix(rest[0], "-") {
		return nil, path, rest
	}
	return cur, path, rest
}

func usageError(env *Env, root *command, args []string) int {
	if len(args) == 0 {
		writeUsage(env.Stderr, root, nil)
		return ExitUsage
	}
	_, _ = fmt.Fprintf(env.Stderr, "unknown command %q\n\n", strings.Join(nonFlags(args), " "))
	writeUsage(env.Stderr, root, nil)
	return ExitUsage
}

func nonFlags(args []string) []string {
	var out []string
	for _, a := range args {
		if !strings.HasPrefix(a, "-") {
			out = append(out, a)
		}
	}
	if len(out) == 0 {
		return args
	}
	return out
}

func execute(env *Env, root, cmd *command, path, rest []string) int {
	c := &call{env: env, cmd: cmd}
	if cmd.raw && cmd.milestone == "" && (len(rest) != 1 || (rest[0] != "--help" && rest[0] != "-h")) {
		return c.report(cmd.run(c, rest))
	}
	fs := flag.NewFlagSet("crsp "+strings.Join(path, " "), flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	c.fs = fs
	c.g.bind(fs)
	if cmd.flags != nil {
		cmd.flags(c, fs)
	}
	if cmd.dashdash {
		for i, a := range rest {
			if a == "--" {
				rest, c.after = rest[:i], append([]string(nil), rest[i+1:]...)
				break
			}
		}
	}
	pos, err := parseInterleaved(fs, rest)
	if errors.Is(err, flag.ErrHelp) || (len(pos) > 0 && (pos[0] == "help") && len(cmd.subs) > 0) {
		writeUsage(env.Stdout, cmd, path)
		return ExitOK
	}
	if err != nil {
		_, _ = fmt.Fprintf(env.Stderr, "error: %v\n\n", err)
		writeUsage(env.Stderr, cmd, path)
		return ExitUsage
	}
	if cmd.milestone != "" {
		_, _ = fmt.Fprintf(env.Stderr, "error: %s is not built yet (planned for %s). Nothing changed.\n", strings.Join(path, " "), cmd.milestone)
		return ExitUsage
	}
	if cmd.run == nil {
		if len(pos) > 0 {
			_, _ = fmt.Fprintf(env.Stderr, "unknown command %q\n\n", strings.Join(append(append([]string{}, path...), pos[0]), " "))
			writeUsage(env.Stderr, cmd, path)
			return ExitUsage
		}
		writeUsage(env.Stderr, cmd, path)
		return ExitUsage
	}
	err = cmd.run(c, pos)
	return c.report(err)
}

// parseInterleaved lets flags come before or after positional arguments.
func parseInterleaved(fs *flag.FlagSet, args []string) ([]string, error) {
	var pos []string
	for {
		if err := fs.Parse(args); err != nil {
			return nil, err
		}
		rest := fs.Args()
		if len(rest) == 0 {
			return pos, nil
		}
		pos = append(pos, rest[0])
		args = rest[1:]
	}
}

func (c *call) report(err error) int {
	if err == nil {
		return ExitOK
	}
	var ee *exitError
	if !errors.As(err, &ee) {
		ee = &exitError{Code: ExitFailed, Message: err.Error()}
	}
	if c.g.json {
		if len(ee.Envelope) > 0 {
			_, _ = c.env.Stderr.Write(append(ee.Envelope, '\n'))
		} else {
			_ = writeEnvelope(c.env.Stderr, ee)
		}
	} else {
		_, _ = fmt.Fprintln(c.env.Stderr, "error:", ee.Message)
	}
	return ee.Code
}

func writeUsage(w io.Writer, cmd *command, path []string) {
	title := "crsp"
	if len(path) > 0 {
		title += " " + strings.Join(path, " ")
	}
	if len(cmd.subs) == 0 {
		_, _ = fmt.Fprintf(w, "Usage: %s %s\n\n%s\n", title, cmd.args, cmd.summary)
		if cmd.milestone != "" {
			_, _ = fmt.Fprintf(w, "\nNot built yet (planned for %s).\n", cmd.milestone)
		}
		return
	}
	_, _ = fmt.Fprintf(w, "Usage: %s <command>\n\n", title)
	if cmd.summary != "" && len(path) > 0 {
		_, _ = fmt.Fprintf(w, "%s\n\n", cmd.summary)
	}
	_, _ = fmt.Fprintln(w, "Commands:")
	subs := append([]*command(nil), cmd.subs...)
	for _, s := range subs {
		if strings.HasPrefix(s.name, "__") {
			continue
		}
		note := ""
		if s.milestone != "" {
			note = " (not built yet: " + s.milestone + ")"
		}
		_, _ = fmt.Fprintf(w, "  %-12s %s%s\n", s.name, s.summary, note)
	}
	if len(path) == 0 {
		_, _ = fmt.Fprint(w, "\nGlobal flags: --context NAME, --controller URL, --json, --no-color, --timeout 30s\nConfiguration of the controller is read from CRSP_* environment variables; see .env.example.\n")
	}
}

// Exit makes a mode function end with the given exit code and message, for a
// mode (such as the tray) that has its own exit codes.
func Exit(code int, message string) error { return fail(code, "%s", message) }
