// Package listenergate gates persistent runners that run on the machine itself
// (runners R5.134 to R5.140, scheduling R5.108; owner decision, Option 3 of the
// listener research).
//
// A gated persistent runner is registered and its listener is off. crsp turns the
// listener on only when admission admits a job for that runner, and off after
// the job's completed event, so a job waits queued on GitHub, with no clock
// running, until crsp has room. The steps of one job, kept as one lease per
// GitHub job id:
//
//  1. add the job-name custom label to the runner (labels API) and start the
//     listener;
//  2. the job runs; crsp never stops a listener while a job is assigned to it;
//  3. after the completed event: stop the listener, wait for a clean exit, remove
//     the label.
//
// A runner that other open leases still need keeps its listener (the second-job
// race: the listener takes a queued job at once after the first). Every gated
// runner is woken once a week for about a minute, with no job and no crsp label,
// so GitHub never removes it for not connecting for 14 days.
//
// The gate starts in observe mode (setting enforce, default false): it writes
// would_start and would_stop audit rows and a Doctor info line, and leaves every
// listener as it is. Every dependency is a small interface and the clock is
// injected, so the rules are tested on fakes with no sleep.
package listenergate
