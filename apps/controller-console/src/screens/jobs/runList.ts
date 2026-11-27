// One line about a run: the numbers a Queue, Running or Done row shows (owner decision A, 18 Nov 2026). A run is
// one row; its jobs are the next level, its steps the one after. The runs list answers without the jobs of a run, so
// the Queue and Running lists join the queued and running jobs they also read, by run and attempt.
import type { Schemas } from '../../api/api';
import type { FollowUp, Job } from './words';

export type RunSummary = Schemas['RunSummary'];

export interface RunRow {
  id: number;
  attempt: number;
  repository: string;
  workflow: string;
  branch: string;
  head_sha: string;
  pull_request?: number | undefined;
  task_id?: string | undefined;
  trigger: string;
  state: 'queued' | 'running' | 'done';
  result?: Schemas['JobResult'] | undefined;
  created_at: string;
  completed_at?: string | undefined;
  /** Jobs of the run: all of them, those done and those that failed. Unknown for a row made from queued jobs. */
  jobsTotal?: number | undefined;
  jobsDone?: number | undefined;
  jobsFailed?: number | undefined;
  totalSeconds?: number | undefined;
  eta?: Schemas['Eta'] | undefined;
  /** The state with follow-ups counted: `follow_up_running` while a run this run started next is not finished. */
  overallState?: Schemas['RunSummary']['overall_state'] | undefined;
  /** The runs this run started next (event workflow_run, such as Installers after CI). */
  startedNext?: readonly FollowUp[] | undefined;
  /** The run's queued jobs, in line order, and its running jobs (Queue and Running lists). */
  queued: Job[];
  running: Job[];
}

export const runKey = (r: { id: number; attempt: number }) => `${r.id}-${r.attempt}`;

const byPlace = (a: Job, b: Job) =>
  (a.queue_position ?? Number.MAX_SAFE_INTEGER) -
  (b.queue_position ?? Number.MAX_SAFE_INTEGER);

/** Index jobs by the run attempt they belong to. */
export function byRun(jobs: readonly Job[]): Map<string, Job[]> {
  const out = new Map<string, Job[]>();
  for (const j of jobs) {
    const k = runKey({ id: j.run_id, attempt: j.run_attempt });
    out.set(k, [...(out.get(k) ?? []), j]);
  }
  return out;
}

/** A row from the runs list, with the queued and running jobs of the same run when they were read. */
export function fromSummary(
  r: RunSummary,
  queued: ReadonlyMap<string, Job[]> = new Map(),
  running: ReadonlyMap<string, Job[]> = new Map(),
): RunRow {
  const k = runKey(r);
  return {
    id: r.id,
    attempt: r.attempt,
    repository: r.repository,
    workflow: r.workflow,
    branch: r.branch,
    head_sha: r.head_sha,
    pull_request: r.pull_request,
    task_id: r.task_id,
    trigger: r.trigger,
    state: r.state,
    result: r.result,
    created_at: r.created_at,
    completed_at: r.completed_at,
    jobsTotal: r.jobs_total,
    jobsDone: r.jobs_done,
    jobsFailed: r.jobs_failed,
    totalSeconds: r.total_seconds,
    eta: r.eta,
    overallState: r.overall_state,
    startedNext: r.started_next,
    queued: [...(queued.get(k) ?? [])].sort(byPlace),
    running: running.get(k) ?? [],
  };
}

/** One row for each run that has queued jobs, from the queued jobs themselves; the runs list adds the totals. */
export function fromQueuedJobs(
  jobs: readonly Job[],
  runs: ReadonlyMap<string, RunSummary> = new Map(),
): RunRow[] {
  const groups = byRun(jobs);
  const rows: RunRow[] = [];
  for (const [k, js] of groups) {
    const first = js[0] as Job;
    const known = runs.get(k);
    const queued = [...js].sort(byPlace);
    rows.push({
      ...(known
        ? fromSummary(known)
        : {
            id: first.run_id,
            attempt: first.run_attempt,
            repository: first.repository,
            workflow: first.workflow ?? 'workflow',
            branch: first.branch ?? '',
            head_sha: first.head_sha ?? '',
            pull_request: first.pull_request,
            task_id: first.task_id,
            trigger: first.trigger ?? 'push',
            state: 'queued' as const,
            created_at: first.created_at ?? first.queued_at ?? '',
            queued: [],
            running: [],
          }),
      queued,
    });
  }
  return rows.sort(
    (a, b) => (a.queued[0]?.queue_position ?? 0) - (b.queued[0]?.queue_position ?? 0),
  );
}

/** Seconds a run took: the recorded total, else from its creation to its end (or to now while it is open). */
export function elapsedSeconds(r: RunRow, now: number): number {
  if (r.state === 'done') {
    if (r.totalSeconds !== undefined) return r.totalSeconds;
    return r.completed_at
      ? Math.max(0, (Date.parse(r.completed_at) - Date.parse(r.created_at)) / 1000)
      : 0;
  }
  return Math.max(0, (now - Date.parse(r.created_at)) / 1000);
}
