package jobcmd

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strconv"
	"syscall"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/vmcheck"
)

// VMCacheCapVar sets the size cap of the VM cache in GiB.
const VMCacheCapVar = "CRSP_VM_CACHE_GIB"

const gib = int64(1) << 30

// warnMemory prints the memory warning of runners R5.55 before a boot. It
// warns and goes on; an unknown free size says nothing.
func warnMemory(env Env, free func() (int64, bool), memMB int) {
	if free == nil {
		return
	}
	if memMB == 0 {
		memMB = vmcheck.DefaultMemMB
	}
	mb, ok := free()
	if w := vmcheck.MemoryWarning(memMB, mb, ok); w != "" {
		_, _ = fmt.Fprintln(env.Stderr, w)
	}
}

// capBytes is --cap-gib, else CRSP_VM_CACHE_GIB, else the default; a bad value
// is an error, 0 turns the cap off.
func capBytes(flagGiB int64, set bool, env Env) (int64, error) {
	if set {
		if flagGiB < 0 {
			return 0, fmt.Errorf("--cap-gib cannot be negative")
		}
		return flagGiB * gib, nil
	}
	if v := env.Getenv(VMCacheCapVar); v != "" {
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil || n < 0 {
			return 0, fmt.Errorf("%s=%q is not a number of GiB", VMCacheCapVar, v)
		}
		return n * gib, nil
	}
	return vmcheck.DefaultCacheCapGiB * gib, nil
}

// trimCache keeps the cache under its size cap after a job, oldest first,
// and does nothing while another VM job runs. A failure is not the job's.
func trimCache(ctx context.Context, cache string, cat vmcheck.Catalogue, env Env) {
	limit, err := capBytes(0, false, env)
	if err != nil || limit == 0 {
		return
	}
	res, err := vmcheck.Prune(ctx, cache, cat, vmcheck.PruneSpec{CapBytes: limit, Keep: 1 << 20}, vmcheck.PidAlive)
	if err != nil {
		_, _ = fmt.Fprintln(env.Stderr, "crsp vm: cache trim:", err)
		return
	}
	for _, p := range res.Removed {
		_, _ = fmt.Fprintln(env.Stdout, "removed from the VM cache:", p)
	}
}

// vmPrune is `crsp vm prune`: it removes the old downloaded images, prepared
// base disks and temporary files beyond what the catalogue lists, keeps the
// newest N of each distro (default 1), and with a size cap removes the oldest
// until the cache fits. A disk a running VM uses is never touched.
func vmPrune(args []string, cat vmcheck.Catalogue, env Env) int {
	fs := flag.NewFlagSet("vm prune", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	var cache string
	var keep, capGiB int64
	var dry bool
	fs.StringVar(&cache, "cache", "", "")
	fs.Int64Var(&keep, "keep", vmcheck.DefaultKeep, "")
	fs.Int64Var(&capGiB, "cap-gib", 0, "")
	fs.BoolVar(&dry, "dry-run", false, "")
	if err := fs.Parse(args); err == nil && fs.NArg() != 0 {
		err = fmt.Errorf("prune takes no argument, got %q", fs.Arg(0))
		_, _ = fmt.Fprintf(env.Stderr, "crsp vm: %v\n%s\n", err, vmUsage)
		return ExitVMUsage
	} else if err != nil {
		_, _ = fmt.Fprintf(env.Stderr, "crsp vm: %v\n%s\n", err, vmUsage)
		return ExitVMUsage
	}
	if keep < 1 {
		_, _ = fmt.Fprintf(env.Stderr, "crsp vm: --keep must be at least 1\n%s\n", vmUsage)
		return ExitVMUsage
	}
	set := false
	fs.Visit(func(f *flag.Flag) { set = set || f.Name == "cap-gib" })
	limit, err := capBytes(capGiB, set, env)
	if err != nil {
		_, _ = fmt.Fprintf(env.Stderr, "crsp vm: %v\n%s\n", err, vmUsage)
		return ExitVMUsage
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	res, err := vmcheck.Prune(ctx, cacheDir(cache, env), cat, vmcheck.PruneSpec{Keep: int(keep), CapBytes: limit, DryRun: dry}, vmcheck.PidAlive)
	if err != nil {
		_, _ = fmt.Fprintln(env.Stderr, "FAIL prune:", err)
		return ExitVMFail
	}
	verb := "removed"
	if dry {
		verb = "would remove"
	}
	for _, p := range res.Removed {
		_, _ = fmt.Fprintf(env.Stdout, "%s %s\n", verb, p)
	}
	for _, p := range res.InUse {
		_, _ = fmt.Fprintf(env.Stdout, "kept %s: a running VM uses it\n", p)
	}
	if res.Busy {
		_, _ = fmt.Fprintln(env.Stdout, "the size cap waits: a VM job is running")
	}
	if res.Over > 0 && !res.Busy {
		_, _ = fmt.Fprintf(env.Stdout, "warning: the cache is %d MiB over its cap; only files a run needs are left\n", res.Over>>20)
	}
	_, _ = fmt.Fprintf(env.Stdout, "PASS prune: %s %d file(s), %d MiB, %d kept\n", verb, len(res.Removed), res.Freed>>20, res.Kept)
	return 0
}
