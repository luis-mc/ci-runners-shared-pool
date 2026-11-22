package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/internal/agent"
	"github.com/luis-mc/ci-runners-shared-pool/internal/api/client"
	"github.com/luis-mc/ci-runners-shared-pool/internal/config"
	"github.com/luis-mc/ci-runners-shared-pool/internal/uninstall"
	"github.com/luis-mc/ci-runners-shared-pool/internal/update"
)

// UninstallHooks lets the program and the tests replace the parts of `crsp uninstall` that touch the machine.
type UninstallHooks struct {
	// Steps builds the effects of the remove; runners unregisters the runners through the controller.
	// Nil builds the real ones for this machine.
	Steps func(runners func(ctx context.Context) (int, error)) uninstall.Steps
	// Window shows the native remove window (`crsp lifecycle-window remove`, built by TRAY12) and returns its
	// exit code: 10 keep my data, 11 delete my data too, 1 cancel. An error means no window could be shown.
	// Nil runs this program's own lifecycle-window command.
	Window func(ctx context.Context) (int, error)
	// RemoveProgram removes the program and the service (the installer's uninstall) when crsp uninstall is not
	// run by the installer. Nil does the real one: msiexec on Windows, a message elsewhere.
	RemoveProgram func(ctx context.Context, deleteData bool) error
}

// cmdUninstall is `crsp uninstall` and its alias `crsp remove` (OPS69).
func cmdUninstall(name string) *command {
	return &command{
		name:    name,
		summary: "remove crsp from this machine; your data is kept unless you ask to delete it",
		args:    "[--delete-data|--keep-data] [--yes]",
		flags: func(_ *call, fs *flag.FlagSet) {
			fs.Bool("delete-data", false, "also delete settings, database, logs, caches, runner images, containers and the crsp WSL distro (PURGE=1 does the same)")
			fs.Bool("keep-data", false, "keep your data without asking")
			yesFlag(fs)
			fs.String("actor", "", "who asked for the remove, for the audit log (default: the signed-in OS user)")
			fs.String("ask-to", "", "installer use: ask keep or delete, write the answer to this file, change nothing")
			fs.String("choice-file", "", "installer use: read the answer an earlier --ask-to wrote")
			fs.Bool("from-installer", false, "installer use: the installer removes the program and the service itself")
			fs.Bool("runners-only", false, "installer use: only unregister the runners from GitHub")
		},
		run: func(c *call, args []string) error { return c.uninstall(args) },
	}
}

func (c *call) flagStr(name string) string {
	if f := c.fs.Lookup(name); f != nil {
		return f.Value.String()
	}
	return ""
}

func (c *call) flagBool(name string) bool { return c.flagStr(name) == "true" }

func (c *call) uninstall(args []string) error {
	if len(args) != 0 {
		return fail(ExitUsage, "Usage: crsp uninstall [--delete-data|--keep-data] [--yes]")
	}
	if c.flagBool("delete-data") && c.flagBool("keep-data") {
		return fail(ExitUsage, "Use --delete-data or --keep-data, not both.")
	}
	hooks := c.env.Uninstall
	if hooks == nil {
		hooks = &UninstallHooks{}
	}
	ctx := c.env.Context
	w := c.env.Stderr

	// The installer asks in one step and removes in another: --ask-to only asks.
	if file := c.flagStr("ask-to"); file != "" {
		choice, err := c.askChoice(hooks)
		if err != nil {
			return err
		}
		word := "keep"
		if choice == uninstall.ChoiceDelete {
			word = "delete"
		}
		if err := os.WriteFile(file, []byte(word+"\n"), 0o644); err != nil {
			return fail(ExitFailed, "Could not write the answer: %v", err)
		}
		return nil
	}
	if c.flagBool("runners-only") {
		n, err := c.unregisterRunners(ctx)
		if err != nil {
			_, _ = fmt.Fprintf(w, "Could not unregister every runner from GitHub: %v\n", err)
			return nil // the installer goes on; the audit row of the full remove says what is left
		}
		_, _ = fmt.Fprintf(w, "Unregistered %d runner(s) from GitHub.\n", n)
		return nil
	}

	choice, err := c.askChoice(hooks)
	if err != nil {
		return err
	}
	deleteData := choice == uninstall.ChoiceDelete
	if !c.flagBool("from-installer") {
		items := []string{"crsp's runners are unregistered from GitHub", "the crsp program and service are removed"}
		if deleteData {
			items = append(items, "DELETED: settings, database, logs, caches, runner images, containers and the crsp WSL distro. This cannot be undone.")
		} else {
			items = append(items, "kept: your settings, database and logs")
		}
		if err := c.confirm("This removes crsp from this machine.", items, c.yesFlag()); err != nil {
			return err
		}
	}

	runners := func(ctx context.Context) (int, error) { return c.unregisterRunners(ctx) }
	var steps uninstall.Steps
	if hooks.Steps != nil {
		steps = hooks.Steps(runners)
	} else {
		steps = c.hostSteps(runners)
	}
	sum, err := uninstall.Run(ctx, uninstall.Options{DeleteData: deleteData, Actor: c.actor()}, steps, func(s string) { _, _ = fmt.Fprintln(w, s) })
	if err != nil {
		return fail(ExitFailed, "%v", err)
	}
	if c.flagBool("from-installer") {
		if f := c.flagStr("choice-file"); f != "" {
			_ = os.Remove(f) // the answer is used once
		}
		return nil
	}
	remove := hooks.RemoveProgram
	if remove == nil {
		remove = removeProgram
	}
	if err := remove(ctx, deleteData); err != nil {
		return fail(ExitFailed, "The data step is done, but the program could not be removed: %v", err)
	}
	if len(sum.Warnings) > 0 {
		_, _ = fmt.Fprintf(w, "crsp is removed, with %d warning(s) above.\n", len(sum.Warnings))
		return nil
	}
	if hooks.RemoveProgram == nil && runtime.GOOS == "windows" {
		_, _ = fmt.Fprintln(w, "Windows Installer is removing the crsp program and service now; it ends this program and finishes in a moment.")
		return nil
	}
	_, _ = fmt.Fprintln(w, "crsp is removed.")
	return nil
}

// actor is who asked: --actor, else the OS user.
func (c *call) actor() string {
	if a := strings.TrimSpace(c.flagStr("actor")); a != "" {
		return a
	}
	if u, err := user.Current(); err == nil && u.Username != "" {
		return u.Username
	}
	return "unknown"
}

// askChoice decides keep or delete: a choice file, the flags and PURGE, a silent run, the native window, the
// terminal. Cancel is an error that changes nothing.
func (c *call) askChoice(h *UninstallHooks) (uninstall.Choice, error) {
	if file := c.flagStr("choice-file"); file != "" {
		b, err := os.ReadFile(file)
		if err != nil {
			// No answer file: the run was silent, so the data is kept unless a flag says otherwise.
			if !c.flagBool("delete-data") && !uninstall.PurgeFromEnv(c.env.Getenv) {
				return uninstall.ChoiceKeep, nil
			}
		} else if strings.TrimSpace(string(b)) == "delete" {
			return uninstall.ChoiceDelete, nil
		} else if !c.flagBool("delete-data") && !uninstall.PurgeFromEnv(c.env.Getenv) {
			return uninstall.ChoiceKeep, nil
		}
	}
	interactive := c.env.IsTerminal() && !c.yesFlag()
	window := func() (int, error) {
		fn := h.Window
		if fn == nil {
			fn = runLifecycleWindow
		}
		return fn(c.env.Context)
	}
	terminal := func() (uninstall.Choice, error) {
		_, _ = fmt.Fprint(c.env.Stderr, "Keep my data or delete my data too? Type keep, delete or cancel: ")
		line, _ := c.stdin().ReadString('\n')
		switch strings.ToLower(strings.TrimSpace(line)) {
		case "keep":
			return uninstall.ChoiceKeep, nil
		case "delete":
			return uninstall.ChoiceDelete, nil
		}
		return uninstall.ChoiceCancel, nil
	}
	if c.flagStr("ask-to") != "" {
		interactive = true // asking is the point of --ask-to
		if !c.env.IsTerminal() {
			terminal = nil
		}
	}
	choice, err := uninstall.Ask(c.flagBool("delete-data"), c.flagBool("keep-data"), uninstall.PurgeFromEnv(c.env.Getenv), interactive, window, terminal)
	if errors.Is(err, uninstall.ErrCancelled) {
		return choice, fail(ExitFailed, "Cancelled. Nothing was removed.")
	}
	if err != nil {
		return choice, fail(ExitUsage, "%v", err)
	}
	return choice, nil
}

// runLifecycleWindow runs this program's own `lifecycle-window remove` and returns its exit code. Only
// 10, 11 and 1 are answers; anything else (the command does not exist in this build, no desktop) is an
// error, and the question goes to the terminal.
func runLifecycleWindow(ctx context.Context) (int, error) {
	self, err := os.Executable()
	if err != nil {
		return 0, err
	}
	cmd := exec.CommandContext(ctx, self, "lifecycle-window", "remove")
	err = cmd.Run()
	code := 0
	var ee *exec.ExitError
	switch {
	case err == nil:
	case errors.As(err, &ee):
		code = ee.ExitCode()
	default:
		return 0, err
	}
	switch code {
	case uninstall.WindowExitKeep, uninstall.WindowExitDelete, uninstall.WindowExitCancel:
		return code, nil
	}
	return 0, fmt.Errorf("the remove window did not answer (exit %d)", code)
}

// unregisterRunners removes every runner from GitHub through the controller and waits for them to go.
func (c *call) unregisterRunners(ctx context.Context) (int, error) {
	s, err := c.open()
	if err != nil {
		return 0, errors.New("crsp is not signed in to its controller (run crsp login), so the runners stay registered in GitHub; delete them in the repository's runner settings")
	}
	rs, err := c.runners(s, client.ListRunnersParams{})
	if err != nil {
		return 0, err
	}
	yes := true
	for i := range rs {
		d, err := s.client.DeleteRunnerWithResponse(ctx, rs[i].Id, &client.DeleteRunnerParams{Confirm: &yes})
		if err != nil {
			return 0, s.transport(err)
		}
		if d.StatusCode() == 404 {
			continue
		}
		if err := apiError(d.StatusCode(), d.Body); err != nil {
			return 0, err
		}
	}
	deadline := c.env.Now().Add(10 * time.Minute)
	for _, r := range rs {
		for {
			g, err := s.client.GetRunnerWithResponse(ctx, r.Id)
			if err != nil {
				return 0, s.transport(err)
			}
			if g.StatusCode() == 404 {
				break
			}
			if g.JSON200 != nil && g.JSON200.State == client.RunnerStateFailed {
				return 0, fmt.Errorf("removing runner %s failed", r.Name)
			}
			if c.env.Now().After(deadline) {
				return 0, fmt.Errorf("runner %s is still draining after 10 minutes; its job is not cut", r.Name)
			}
			if err := c.sleep(2 * time.Second); err != nil {
				return 0, err
			}
		}
	}
	return len(rs), nil
}

// hostSteps is the real remove on this machine.
func (c *call) hostSteps(runners func(ctx context.Context) (int, error)) uninstall.Steps {
	paths := config.DefaultPaths(runtime.GOOS, c.env.Getenv)
	dirs := []string{paths.DataDir, agent.DefaultDataDir(runtime.GOOS)}
	if d := strings.TrimSpace(c.env.Getenv(config.EnvDataDir)); d != "" {
		dirs[0] = d
	}
	return &uninstall.Host{
		GOOS:      runtime.GOOS,
		DataDirs:  dirs,
		Services:  []string{"crsp-agent", "crsp"},
		Exec:      uninstall.RealExec,
		Runners:   runners,
		SystemLog: uninstall.SystemLogFor(runtime.GOOS, uninstall.RealExec),
	}
}

// removeProgram removes the program and the service when crsp uninstall is run by hand. On Windows, for an
// MSI install (the install-kind marker next to the exe), it hands the removal to Windows Installer: msiexec
// /x with PURGE=1 for --delete-data, started detached so it can replace this very exe. The installer ends
// this process, so the call returns once msiexec has started. Elsewhere the package manager owns the files.
func removeProgram(ctx context.Context, deleteData bool) error {
	if runtime.GOOS != "windows" {
		return errors.New("remove the package with your package manager (apt remove crsp, dnf remove crsp, or the macOS uninstaller); the data step is done")
	}
	if self, err := os.Executable(); err == nil && update.InstallKindFor(self, "windows") != update.KindMSI {
		return errors.New("this copy of crsp was not installed by the Windows installer; delete its folder yourself; the data step is done")
	}
	return uninstall.MSIHandoff{
		SystemRoot:  os.Getenv("SystemRoot"),
		ProductCode: msiProductCode,
		Start:       uninstall.StartDetached,
		LogFile:     filepath.Join(os.TempDir(), "crsp-uninstall.log"),
	}.Remove(ctx, deleteData)
}

// msiProductCode reads the ProductCode of the installed crsp: from the Apps list, else from the UpgradeCode.
func msiProductCode(ctx context.Context) (string, error) {
	out, err := exec.CommandContext(ctx, "powershell.exe", "-NoProfile", "-NonInteractive", "-Command",
		`$c = (Get-ItemProperty 'HKLM:\Software\Microsoft\Windows\CurrentVersion\Uninstall\*','HKLM:\Software\WOW6432Node\Microsoft\Windows\CurrentVersion\Uninstall\*' -ErrorAction SilentlyContinue | Where-Object { $_.DisplayName -eq 'CI Runners Shared Pool' } | Select-Object -First 1).PSChildName; `+
			`if (-not $c) { try { $c = @((New-Object -ComObject WindowsInstaller.Installer).RelatedProducts('`+uninstall.UpgradeCode+`'))[0] } catch { } }; $c`).Output()
	code := strings.TrimSpace(string(out))
	if err != nil || !strings.HasPrefix(code, "{") {
		return "", errors.New("crsp is not in the Apps list; nothing to remove")
	}
	return code, nil
}
