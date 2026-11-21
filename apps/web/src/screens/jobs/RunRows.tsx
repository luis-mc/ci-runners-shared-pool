// A run as one row (owner decision A, 18 Nov 2026): the columns of the Queue, Running and Done lists. The row action
// Open run goes straight to the Run page (OPS43), as Open job goes to the Job page.
import {
  CappedList,
  Cluster,
  ShortText,
  Status,
  TextLink,
  formatShortDateTime,
  type Column,
} from '@crsp/ui';
import { runKey, type RunRow } from './runList';
import {
  FOLLOW_UP_RUNNING,
  etaOne,
  commitWords,
  eventWords,
  jobStatus,
  startRange,
  startedNextWords,
  taskIdOf,
} from './words';
import { waitShort, waitWords, workflowFile } from './words';

export type RunKind = 'queue' | 'running' | 'done' | 'all';

/** The branch cut in the middle; the task id is added only when the cut hides it. Worktree: see the detail. */
export function BranchWords({
  branch,
  task,
  max = 16,
}: {
  branch: string;
  task?: string | undefined;
  max?: number;
}) {
  const id = task ?? taskIdOf(branch);
  return (
    <>
      <ShortText text={branch} max={max} />
      {id && branch.length > max ? ` (task ${id})` : ''}
    </>
  );
}

/** The result of a run in a word, a glyph and a colour: the state while it is open, else the result. */
export function runStatus(r: Pick<RunRow, 'state' | 'result'>) {
  return jobStatus(r);
}

/** A finished run with a follow-up still queued or running (CIX24): its own result stays, the run is not finished. */
export const followUpRunning = (r: Pick<RunRow, 'overallState'>) =>
  r.overallState === 'follow_up_running';

export function runColumns(kind: RunKind, _now: number): Column<RunRow>[] {
  const repo: Column<RunRow> = {
    id: 'repo',
    header: 'Repository',
    cell: (r) => r.repository.split('/')[1] ?? r.repository,
    sort: (r) => r.repository,
    kind: 'grow',
    grow: 2,
  };
  // The file name only: the full path is the hover text and the title of the Run page (UX-72, CRSP210).
  const workflow: Column<RunRow> = {
    id: 'workflow',
    header: 'Workflow',
    cell: (r) => <TextLink to={runPath(r)}>{workflowFile(r.workflow)}</TextLink>,
    kind: 'grow',
    grow: 2,
    title: (r) => r.workflow,
    sort: (r) => r.workflow,
  };
  // The run number in its own column, whole, so it never sits outside its cell (CRSP210).
  const runNumber: Column<RunRow> = {
    id: 'run',
    header: 'Run',
    cell: (r) => `#${r.id}${r.attempt > 1 ? `, attempt ${r.attempt}` : ''}`,
    sort: (r) => r.id,
    kind: 'fit',
  };
  const branch: Column<RunRow> = {
    id: 'branch',
    header: 'Branch',
    cell: (r) => <BranchWords branch={r.branch} task={r.task_id} />,
    middle: (r) => r.branch,
    title: (r) => r.branch,
    sort: (r) => r.branch,
    kind: 'grow',
    grow: 1,
    secondary: true,
  };
  const commit: Column<RunRow> = {
    id: 'commit',
    header: 'Commit',
    cell: (r) => commitWords(r),
    sort: (r) => commitWords(r),
    kind: 'fit',
    secondary: true,
  };
  const event: Column<RunRow> = {
    id: 'event',
    header: 'Event',
    cell: (r) => eventWords(r),
    sort: (r) => eventWords(r),
    kind: 'fit',
    secondary: true,
  };
  const status: Column<RunRow> = {
    id: 'status',
    header: 'Result',
    cell: (r) => {
      const s = runStatus(r);
      return followUpRunning(r) ? (
        <Cluster>
          <Status kind={s.kind} word={s.word} />
          <Status kind="running" word={FOLLOW_UP_RUNNING} />
        </Cluster>
      ) : (
        <Status kind={s.kind} word={s.word} />
      );
    },
    // The follow-up is named in the hover text and in the details panel; a column of its own would not fit one line.
    title: (r) =>
      r.startedNext?.length ? `Started next: ${startedNextWords(r.startedNext)}` : '',
    sort: (r) => runStatus(r).word,
  };
  const doneOf: Column<RunRow> = {
    id: 'jobs',
    header: 'Jobs done',
    cell: (r) =>
      r.jobsTotal === undefined ? 'not known' : `${r.jobsDone ?? 0} of ${r.jobsTotal}`,
    sort: (r) => (r.jobsDone ?? 0) / Math.max(1, r.jobsTotal ?? 1),
    secondary: true,
  };
  // All states in one list (the unified list, 20 Nov 2026): when it starts, ends or ended, and what it waits for. The
  // commit and event are in the details panel, so the columns fit one line at 1300 px.
  if (kind === 'all')
    return [
      repo,
      workflow,
      runNumber,
      branch,
      doneOf,
      {
        id: 'when',
        header: 'When',
        cell: (r) => runWhen(r),
        sort: (r) => r.completed_at ?? r.eta?.p50 ?? 0,
        secondary: true,
      },
      {
        id: 'now',
        header: 'Waits for or runs',
        cell: (r) =>
          r.state === 'running' ? (
            <CappedList items={r.running.map((j) => j.job_name)} max={1} />
          ) : null,
        title: (r) => r.running.map((j) => j.job_name).join(', '),
        kind: 'grow',
        long: (r) => {
          const n = r.state === 'queued' ? r.queued[0] : undefined;
          return n ? { full: waitWords(n), short: waitShort(n) } : null;
        },
        secondary: true,
      },
      status,
    ];
  if (kind === 'queue')
    return [
      {
        id: 'order',
        header: 'Place',
        cell: (r) => String(r.queued[0]?.queue_position ?? ''),
        sort: (r) => r.queued[0]?.queue_position ?? 0,
      },
      repo,
      workflow,
      runNumber,
      branch,
      commit,
      event,
      {
        id: 'starts',
        header: 'Starts',
        cell: (r) => {
          const e = r.queued[0]?.queue_eta;
          return e ? startRange(e) : 'not known';
        },
        sort: (r) => r.queued[0]?.queue_eta?.p50 ?? 0,
      },
      {
        id: 'why',
        header: 'Why it waits',
        cell: () => null,
        kind: 'grow',
        long: (r) => {
          const n = r.queued[0];
          return n ? { full: waitWords(n), short: waitShort(n) } : null;
        },
        secondary: true,
      },
    ];
  if (kind === 'running')
    return [
      repo,
      workflow,
      runNumber,
      branch,
      commit,
      event,
      doneOf,
      {
        id: 'now',
        header: 'Running now',
        cell: (r) => <CappedList items={r.running.map((j) => j.job_name)} max={1} />,
        title: (r) => r.running.map((j) => j.job_name).join(', '),
        kind: 'grow',
        secondary: true,
      },
      {
        id: 'ends',
        header: 'Ends',
        cell: (r) => (r.eta ? etaOne(r.eta) : 'not known'),
        sort: (r) => r.eta?.p50 ?? 0,
        secondary: true,
      },
      status,
    ];
  return [
    {
      id: 'finished',
      header: 'Finished',
      cell: (r) => (r.completed_at ? formatShortDateTime(r.completed_at) : 'not known'),
      sort: (r) => r.completed_at ?? '',
    },
    repo,
    workflow,
    runNumber,
    branch,
    commit,
    event,
    doneOf,
    {
      id: 'failed',
      header: 'Jobs failed',
      cell: (r) => String(r.jobsFailed ?? 0),
      sort: (r) => r.jobsFailed ?? 0,
      secondary: true,
    },
    status,
  ];
}

/** When a run starts (queued), ends (running) or ended (done), in words. */
export function runWhen(r: RunRow): string {
  if (r.state === 'queued') {
    const e = r.queued[0]?.queue_eta;
    return e ? startRange(e) : 'not known';
  }
  if (r.state === 'running') return r.eta ? etaOne(r.eta) : 'not known';
  return r.completed_at ? formatShortDateTime(r.completed_at) : 'not known';
}

/** Where a run row opens: the Run page of that attempt. */
export function runPath(r: Pick<RunRow, 'id' | 'attempt'>): string {
  return `/jobs/run/${r.id}?attempt=${r.attempt}`;
}

export { runKey };
