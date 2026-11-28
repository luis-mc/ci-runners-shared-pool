// Package debugaccess holds the controller side of debug access to a running
// job (docs/features/debug-access, CRSP331): the per-repository switch, the
// policy that decides who may open a session and on which job, the session
// record and its audit rows, the time and idle limits, the kill switch and the
// redacted transcript.
//
// Security comes first and every check fails closed:
//
//   - Off by default. A repository without a saved setting is off (R5.1); the
//     pool switch is the kill switch (R5.26).
//   - Only the owner and admins open, extend or end a session; a repository can
//     be narrowed to the owner only, never widened (R5.2).
//   - A public repository and a job that runs for a pull request are refused,
//     and no setting changes that (R5.5). crsp does not record whether a pull
//     request comes from a fork, so every pull request job is refused.
//   - A session opens only into one job's own environment. A job whose
//     isolation is unknown, a shared container another job of the repository
//     runs in, and a persistent runner without its own setting are refused
//     (R5.6, R5.7, R5.10). The machine must say it can enter the environment
//     (Enterer, R5.32); with no Enterer every open is refused with the reason.
//   - Every open, refusal, extension and end writes an audit row; a change and
//     its row commit in one transaction (R5.24, R6.4).
//   - A transcript passes the shared redaction before it is stored, is capped
//     per session and for the pool, and expires after 30 days (R5.25, R6.6).
//   - The shell's environment is built from an allow list, never copied from
//     the runner (R5.12, ShellEnv).
//
// The byte stream between the console and the agent, the agent's Enter
// implementations and the held failed job are follow-ups of the spec's build
// order; this package gives them the record, the limits and the checks to use.
package debugaccess
