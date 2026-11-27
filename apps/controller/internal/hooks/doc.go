// Package hooks is the `crsp hook job-started` and `crsp hook job-completed`
// commands that each persistent runner's hooks call (scheduling R5.12 to
// R5.30, R5.59). The job-started hook is the gate: it holds the job until it is
// first in line, a slot is free and the machine is under its budget. The
// job-completed hook frees the job's slots and empties its work folder.
//
// The hooks talk to the local agent over a Unix domain socket (named pipes are
// not needed: Windows 10 and later support them). When the agent cannot be
// reached the gate waits on local load only and logs it (R5.23).
//
// Ported from dev-standards claude/plugins/delivery/scripts/runner_load_gate.py
// and runner_job_done.py.
package hooks
