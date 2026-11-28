---
change: minor
task: CRSP331
---
Built the controller core of debug access: the repository setting (off by default) and the pool switch, the policy that decides who may open a session on which job and refuses with a named reason, the session record with time, idle and job-end limits and one extension, an audit row in the same transaction for every step, the redacted transcript with its caps, the shell environment allow list, the API and the Debug access card on a job page. No agent can enter a job's environment yet, so every open is refused and audited. The terminal, the agent side, hold failed jobs, notifications and crsp debug follow.
