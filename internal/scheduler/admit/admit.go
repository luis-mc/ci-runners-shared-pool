// Package admit decides whether a runner may take a job now (scheduling R5.90
// to R5.94). Runners own no CPUs; they share the machine's pool. Before a
// runner takes a job two checks hold: the job's requirement is within the
// runner's class ceiling, and the pool's free CPU and free memory both cover
// it. A job that fails either waits, and a later job that fits may pass it.
// A waiting job that is passed too often, or waits too long, becomes the
// machine's reserved job and later jobs may start only as backfill.
//
// The package is pure: it reads no clock and no store.
package admit

import (
	"fmt"
	"time"
)

// Req is what a job needs: whole CPUs and GiB of memory (one scheduler unit is
// one CPU and one GiB).
type Req struct{ CPUs, MemoryGiB int }

// Pool is the CPUs and memory a machine's pool holds.
type Pool struct{ CPUs, MemoryGiB int }

// Ceiling is the class ceiling of a runner.
type Ceiling struct {
	Class string
	Req
}

// Held is a job that holds pool room. End is its forecast end (observability
// R5.40); EndKnown is false when there is no forecast.
type Held struct {
	Req      Req
	End      time.Time
	EndKnown bool
}

// Free is the pool less what the held jobs take. It never goes below 0.
func Free(p Pool, held []Held) Req {
	f := Req{p.CPUs, p.MemoryGiB}
	for _, h := range held {
		f.CPUs -= h.Req.CPUs
		f.MemoryGiB -= h.Req.MemoryGiB
	}
	if f.CPUs < 0 {
		f.CPUs = 0
	}
	if f.MemoryGiB < 0 {
		f.MemoryGiB = 0
	}
	return f
}

// Fits is true when need is within room in both kinds.
func (need Req) Fits(room Req) bool {
	return need.CPUs <= room.CPUs && need.MemoryGiB <= room.MemoryGiB
}

// Why is the reason a job does not start.
type Why string

// The reasons. WhyNone means the job may start.
const (
	WhyNone    Why = ""
	WhyCeiling Why = "ceiling" // the job needs more than the runner's class holds
	WhyPool    Why = "pool"    // the pool's free CPU or memory does not cover it
	WhyNever   Why = "never"   // the pool as a whole is too small for the job
)

// Verdict is the answer of Check.
type Verdict struct {
	Why  Why
	Text string
}

// OK is true when the job may start.
func (v Verdict) OK() bool { return v.Why == WhyNone }

// Check runs the two checks of R5.90. ceilingKnown false skips the first one:
// a runner whose class is not known is not judged by it. class names the
// smallest class that holds the job ("" when none does).
func Check(class string, need Req, ceiling Ceiling, ceilingKnown bool, pool Pool, held []Held) Verdict {
	if need.CPUs > pool.CPUs || need.MemoryGiB > pool.MemoryGiB {
		return Verdict{WhyNever, fmt.Sprintf("the job needs %d CPUs and %d GiB; the pool is %d CPUs and %d GiB",
			need.CPUs, need.MemoryGiB, pool.CPUs, pool.MemoryGiB)}
	}
	if ceilingKnown && !need.Fits(ceiling.Req) {
		return Verdict{WhyCeiling, fmt.Sprintf("the job needs class %s (%d CPUs, %d GiB); this runner is %s (%d CPUs, %d GiB)",
			orNone(class), need.CPUs, need.MemoryGiB, ceiling.Class, ceiling.CPUs, ceiling.MemoryGiB)}
	}
	free := Free(pool, held)
	if !need.Fits(free) {
		return Verdict{WhyPool, fmt.Sprintf("the job needs %d CPUs and %d GiB; the pool has %d CPUs and %d GiB free",
			need.CPUs, need.MemoryGiB, free.CPUs, free.MemoryGiB)}
	}
	return Verdict{}
}

func orNone(s string) string {
	if s == "" {
		return "above xl"
	}
	return s
}

// CouldRunHere is true when a waiting job could run on the machine at all: the
// pool as a whole holds it and the runner ceiling does too. A job that cannot
// is never counted as passed and never reserved.
func CouldRunHere(need Req, ceiling Ceiling, ceilingKnown bool, pool Pool) bool {
	if need.CPUs > pool.CPUs || need.MemoryGiB > pool.MemoryGiB {
		return false
	}
	return !ceilingKnown || need.Fits(ceiling.Req)
}
