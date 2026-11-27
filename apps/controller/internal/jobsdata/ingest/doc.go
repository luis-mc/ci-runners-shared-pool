// Package ingest turns what crsp hears about jobs into jobsdata rows: GitHub
// job events (webhook, demand loop, poller, reconcile), the steps of a
// workflow_job event, crsp's own facts and the workflow graph. It never invents
// data: a field a source does not carry stays as an earlier source set it, and
// the store's rules (state never moves backward, facts kept) back that up.
package ingest
