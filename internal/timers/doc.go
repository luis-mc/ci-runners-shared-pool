// Package timers is crsp's own clock for jobs and runs (scheduling R5.103 to
// R5.110). A job timer starts at the job's first real step and a run timer at
// its first job's real start. When one runs out a 30 s grace comes first, then
// crsp stops that job's runner through the agent, or cancels the whole run
// through GitHub. Everything outside the package (the agent, GitHub, the audit
// log, the store, the clock) comes in through an interface, so the engine is
// tested with fakes and a fake clock and never sleeps.
package timers
