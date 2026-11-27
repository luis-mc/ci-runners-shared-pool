package jobcmd

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/vmcheck"
)

// stringList is a repeatable flag.
type stringList []string

func (s *stringList) String() string     { return strings.Join(*s, "; ") }
func (s *stringList) Set(v string) error { *s = append(*s, v); return nil }

// parseWithDistro parses flags with the one distro before, between or after them.
func parseWithDistro(fs *flag.FlagSet, args []string) (string, error) {
	fs.SetOutput(io.Discard)
	var rest []string
	for len(args) > 0 {
		if err := fs.Parse(args); err != nil {
			return "", err
		}
		if fs.NArg() == 0 {
			break
		}
		rest = append(rest, fs.Arg(0))
		args = fs.Args()[1:]
	}
	if len(rest) != 1 {
		return "", fmt.Errorf("exactly one distro is expected, got %d", len(rest))
	}
	return rest[0], nil
}

// ParseRun reads the arguments of `crsp vm run`.
func ParseRun(args []string) (vmcheck.RunSpec, string, error) {
	fs := flag.NewFlagSet("vm run", flag.ContinueOnError)
	var s vmcheck.RunSpec
	var cache string
	var install, check stringList
	fs.IntVar(&s.MemMB, "mem", 0, "")
	fs.IntVar(&s.CPUs, "cpus", 0, "")
	fs.DurationVar(&s.Timeout, "timeout", 0, "")
	fs.DurationVar(&s.RunTimeout, "run-timeout", 0, "")
	fs.StringVar(&s.Package, "package", "", "")
	fs.StringVar(&cache, "cache", "", "")
	fs.Var(&install, "install", "")
	fs.Var(&check, "check", "")
	d, err := parseWithDistro(fs, args)
	s.Distro, s.Install, s.Check = d, install, check
	return s, cache, err
}

// ParseSELinux reads the arguments of `crsp vm selinux`.
func ParseSELinux(args []string) (vmcheck.SELinuxSpec, string, error) {
	fs := flag.NewFlagSet("vm selinux", flag.ContinueOnError)
	var s vmcheck.SELinuxSpec
	var cache string
	fs.IntVar(&s.MemMB, "mem", 0, "")
	fs.IntVar(&s.CPUs, "cpus", 0, "")
	fs.DurationVar(&s.Timeout, "timeout", 0, "")
	fs.DurationVar(&s.RunTimeout, "run-timeout", 0, "")
	fs.StringVar(&s.RPM, "rpm", "", "")
	fs.StringVar(&s.Module, "module", "", "")
	fs.StringVar(&s.Service, "service", "", "")
	fs.StringVar(&s.ContextType, "context", "", "")
	fs.StringVar(&cache, "cache", "", "")
	d, err := parseWithDistro(fs, args)
	s.Distro = d
	return s, cache, err
}

func guestDeps(env Env) vmcheck.GuestDeps {
	if env.VMGuestDeps != nil {
		return *env.VMGuestDeps
	}
	return vmcheck.RealGuestDeps()
}

// vmPrepare downloads and checks the image of a distro into the cache. The ssh
// key and the cloud-init seed are not prepared here: a key is made fresh for
// each run and never stored (caches R5.46).
func vmPrepare(args []string, cat vmcheck.Catalogue, env Env) int {
	fs := flag.NewFlagSet("vm prepare", flag.ContinueOnError)
	var cache string
	fs.StringVar(&cache, "cache", "", "")
	distro, err := parseWithDistro(fs, args)
	if err != nil {
		_, _ = fmt.Fprintf(env.Stderr, "crsp vm: %v\n%s\n", err, vmUsage)
		return ExitVMUsage
	}
	d, err := cat.Get(distro)
	if err != nil {
		_, _ = fmt.Fprintln(env.Stderr, "crsp vm:", err)
		return ExitVMUsage
	}
	deps := vmcheck.RealDeps()
	if env.VMDeps != nil {
		deps = *env.VMDeps
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	path, img, _, err := vmcheck.Download(ctx, deps.Fetch, distro, d, cacheDir(cache, env))
	if err != nil {
		_, _ = fmt.Fprintln(env.Stderr, "FAIL prepare:", err)
		return ExitVMFail
	}
	_, _ = fmt.Fprintf(env.Stdout, "PASS %s image %s is in the cache: %s\n", distro, img, path)
	return 0
}

func guestSession(cache string, env Env) (vmcheck.GuestSpec, func(), error) {
	work, err := os.MkdirTemp("", "crsp-vm-")
	if err != nil {
		return vmcheck.GuestSpec{}, nil, err
	}
	return vmcheck.GuestSpec{
		BootSpec: vmcheck.BootSpec{CacheDir: cacheDir(cache, env), WorkDir: work},
		Out:      env.Stdout,
	}, func() { _ = os.RemoveAll(work) }, nil
}

func report(out vmcheck.RunOutcome, err error, env Env) int {
	if err != nil {
		_, _ = fmt.Fprintln(env.Stderr, "crsp vm:", err)
		return ExitVMUsage
	}
	if !out.Pass {
		_, _ = fmt.Fprintf(env.Stderr, "FAIL %s after %s: %s\n", out.Distro, out.Elapsed.Round(time.Second), out.Detail)
		if out.ExitCode == 0 {
			return ExitVMFail
		}
		return out.ExitCode
	}
	_, _ = fmt.Fprintf(env.Stdout, "PASS %s (%s) %d step(s) in %s\n", out.Distro, out.Image, len(out.Steps), out.Elapsed.Round(time.Second))
	return 0
}

func vmRun(args []string, cat vmcheck.Catalogue, env Env) int {
	spec, cache, err := ParseRun(args)
	if err != nil {
		_, _ = fmt.Fprintf(env.Stderr, "crsp vm: %v\n%s\n", err, vmUsage)
		return ExitVMUsage
	}
	gs, cleanup, err := guestSession(cache, env)
	if err != nil {
		_, _ = fmt.Fprintln(env.Stderr, "crsp vm:", err)
		return ExitVMFail
	}
	defer cleanup()
	spec.CacheDir, spec.WorkDir, spec.Out = gs.CacheDir, gs.WorkDir, gs.Out
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	warnMemory(env, guestDeps(env).FreeMemMB, spec.MemMB)
	out, err := vmcheck.Run(ctx, cat, spec, guestDeps(env), time.Now)
	trimCache(ctx, spec.CacheDir, cat, env)
	return report(out, err, env)
}

func vmSELinux(args []string, cat vmcheck.Catalogue, env Env) int {
	spec, cache, err := ParseSELinux(args)
	if err != nil {
		_, _ = fmt.Fprintf(env.Stderr, "crsp vm: %v\n%s\n", err, vmUsage)
		return ExitVMUsage
	}
	gs, cleanup, err := guestSession(cache, env)
	if err != nil {
		_, _ = fmt.Fprintln(env.Stderr, "crsp vm:", err)
		return ExitVMFail
	}
	defer cleanup()
	spec.CacheDir, spec.WorkDir, spec.Out = gs.CacheDir, gs.WorkDir, gs.Out
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	warnMemory(env, guestDeps(env).FreeMemMB, spec.MemMB)
	out, err := vmcheck.SELinux(ctx, cat, spec, guestDeps(env), time.Now)
	trimCache(ctx, spec.CacheDir, cat, env)
	return report(out, err, env)
}
