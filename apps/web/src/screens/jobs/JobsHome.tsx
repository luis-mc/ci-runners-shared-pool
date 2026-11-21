// Jobs, home: one tab row (Overview, Live, Done, Run elsewhere, Run insights, Charts; UX-74) and the one list of the
// Live, Done and Run elsewhere tabs: a search box, the state filter All, Queue, Running and Done, one table and a
// details panel (owner decision 20 Nov 2026, option 11 of CRSP211). Wait reasons say what is happening (UX-29); a
// status is a word, a glyph and a colour (UX-58). Runs are the first view, jobs the second.
import {
  ActionBar,
  Button,
  Cut,
  Empty,
  ExportButton,
  Facts,
  LinkButton,
  ListToolbar,
  Message,
  Page,
  PageHeader,
  Progress,
  SearchTable,
  Segmented,
  Stack,
  Status,
  Tabs,
  Text,
  TextLink,
  fileNameOf,
  formatShortDateTime,
  formatDuration,
  type Column,
} from '@crsp/ui';
import { useState } from 'react';
import { Outlet, useLocation, useNavigate, useSearchParams } from 'react-router-dom';
import { unwrap, useClient } from '../../api/api';
import type { Loaded } from '../../api/hooks';
import { Load } from '../common';
import { HelpTerm } from '../help/HelpTerm';
import {
  FILTER_KEYS,
  useJobList,
  useResolvedFilter,
  useRunList,
  type Filter,
  type JobsQuery,
  type RunsQuery,
} from './api';
import { csvFile } from './csv';
import {
  fromQueuedJobs,
  fromSummary,
  byRun,
  elapsedSeconds,
  type RunRow,
} from './runList';
import { GroupTab, type GroupBy } from './Groups';
import {
  BranchWords,
  followUpRunning,
  runColumns,
  runPath,
  runStatus,
  runWhen,
} from './RunRows';
import {
  RETENTION_WORDS,
  commitWords,
  FOLLOW_UP_RUNNING,
  eventWords,
  forecastOf,
  forecastOne,
  jobStatus,
  ranSeconds,
  secondsOrNone,
  startRange,
  startedNextWords,
  waitShort,
  waitWords,
  waitedSeconds,
  workflowFile,
  type Job,
} from './words';

export const JOBS_TABS = [
  { id: 'overview', label: 'Overview', to: '/jobs' },
  { id: 'live', label: 'Live', to: '/jobs/live' },
  { id: 'done', label: 'Done', to: '/jobs/done' },
  { id: 'elsewhere', label: 'Run elsewhere', to: '/jobs/elsewhere' },
  { id: 'insights', label: 'Run insights', to: '/jobs/insights' },
  { id: 'charts', label: 'Charts', to: '/jobs/charts' },
] as const;

/** Done: shows 25, "Show more" adds 25 up to 100 a page; older ones by filter and range (owner decision 1). */
export const DONE_KEPT = 100;
export const DONE_STEP = 25;
/** Days kept: 90 (owner decision 1 and 2). */
export const KEPT_DAYS = 90;
const RANGES = [
  { value: '7', label: 'Last 7 days' },
  { value: '30', label: 'Last 30 days' },
  { value: '90', label: 'Last 90 days' },
] as const;

export function JobsLayout() {
  const navigate = useNavigate();
  const { pathname } = useLocation();
  const segment = pathname.split('/')[2] ?? '';
  const current = JOBS_TABS.find((t) => t.id === segment)?.id ?? 'overview';
  return (
    <Page>
      <PageHeader
        title="Jobs"
        lead="Open a run to see its jobs, and a job to see its steps."
      />
      <Tabs
        label="Jobs sections"
        tabs={JOBS_TABS}
        current={current}
        onChange={(id) =>
          void navigate(JOBS_TABS.find((t) => t.id === id)?.to ?? '/jobs')
        }
      />
      <Outlet />
    </Page>
  );
}

const FILTER_NAMES: Record<(typeof FILTER_KEYS)[number], string> = {
  repository: 'repository',
  machine: 'machine',
  runner: 'runner',
  runner_kind: 'runner type',
  backend: 'backend',
  job_type: 'job type',
  branch: 'branch',
};

/** The filters a group row hands to a list, in the address, and what the API makes of them. */
function useFilters(): {
  filter: Filter;
  query: JobsQuery;
  ready: boolean;
  unknown: boolean;
  note: string | undefined;
  clearTo: string;
} {
  const [params] = useSearchParams();
  const { pathname } = useLocation();
  const filter: Filter = {};
  for (const k of FILTER_KEYS) {
    const v = params.get(k);
    if (v) filter[k] = v;
  }
  const resolved = useResolvedFilter(filter);
  const parts = FILTER_KEYS.flatMap((k) =>
    filter[k]
      ? [
          `${FILTER_NAMES[k]} ${k === 'machine' ? (resolved.machineName ?? filter[k]) : filter[k]}`,
        ]
      : [],
  );
  return {
    filter,
    query: resolved.query,
    ready: resolved.ready,
    unknown: resolved.unknown,
    note: parts.length
      ? `Showing only what belongs to ${parts.join(' and ')}.`
      : undefined,
    clearTo: pathname,
  };
}

function FilterNote({ note, to }: { note: string | undefined; to: string }) {
  if (!note) return null;
  return (
    <Message
      tone="note"
      dismissible={false}
      action={<LinkButton to={to}>Show every job</LinkButton>}
    >
      {note}
    </Message>
  );
}

/** Runner and machine in one cell: "crsp-linuxbox-docker-2 on linux-box". */
function whereWords(j: Job): string {
  return j.runner
    ? `${j.runner.name} on ${j.runner.machine_name ?? 'its machine'}`
    : j.runner_name
      ? `${j.runner_name}`
      : 'not placed yet';
}

function BranchCell({ j }: { j: Job }) {
  return <BranchWords branch={j.branch ?? ''} task={j.task_id} />;
}

function JobCell({ j }: { j: Job }) {
  return (
    <Cut tail={j.run_attempt > 1 ? `, attempt ${j.run_attempt}` : ''}>
      {`${workflowFile(j.workflow ?? 'workflow')} / `}
      <TextLink to={`/jobs/${j.id}`}>{j.job_name}</TextLink>
    </Cut>
  );
}

function StatusCell({ j }: { j: Job }) {
  const s = jobStatus(j);
  return <Status kind={s.kind} word={s.word} />;
}

type Kind = 'queue' | 'running' | 'done';
type ColumnsKind = Kind | 'all';

export function jobColumns(kind: ColumnsKind, now: number): Column<Job>[] {
  const repo: Column<Job> = {
    id: 'repo',
    header: 'Repository',
    cell: (j) => j.repository.split('/')[1] ?? j.repository,
    sort: (j) => j.repository,
    kind: 'grow',
    grow: 2,
  };
  const job: Column<Job> = {
    id: 'job',
    header: 'Workflow and job',
    cell: (j) => <JobCell j={j} />,
    title: (j) =>
      `${j.workflow ?? 'workflow'} / ${j.job_name}${j.run_attempt > 1 ? `, attempt ${j.run_attempt}` : ''}`,
    sort: (j) => j.job_name,
    kind: 'grow',
    grow: 2,
  };
  const branch: Column<Job> = {
    id: 'branch',
    header: 'Branch',
    cell: (j) => <BranchCell j={j} />,
    middle: (j) => j.branch ?? '',
    title: (j) => j.branch ?? '',
    sort: (j) => j.branch ?? '',
    kind: 'grow',
    grow: 1,
    secondary: true,
  };
  const where: Column<Job> = {
    id: 'where',
    header: 'Runner and machine',
    cell: (j) => whereWords(j),
    sort: (j) => j.runner?.name ?? '',
    kind: 'grow',
    secondary: true,
  };
  const waited: Column<Job> = {
    id: 'waited',
    header: 'Waited',
    cell: (j) => secondsOrNone(waitedSeconds(j, now)),
    sort: (j) => waitedSeconds(j, now) ?? 0,
    secondary: true,
  };
  const ran: Column<Job> = {
    id: 'ran',
    header: 'Ran',
    cell: (j) => secondsOrNone(ranSeconds(j, now)),
    sort: (j) => ranSeconds(j, now) ?? 0,
    secondary: true,
  };
  const status: Column<Job> = {
    id: 'status',
    header: 'Status',
    cell: (j) => <StatusCell j={j} />,
    sort: (j) => jobStatus(j).word,
  };
  if (kind === 'all')
    return [
      repo,
      job,
      branch,
      {
        id: 'when',
        header: 'When',
        cell: (j) =>
          j.state === 'queued'
            ? j.queue_eta
              ? startRange(j.queue_eta)
              : 'not known'
            : j.completed_at
              ? formatShortDateTime(j.completed_at)
              : secondsOrNone(ranSeconds(j, now)),
        sort: (j) => j.completed_at ?? j.queue_eta?.p50 ?? 0,
        secondary: true,
      },
      {
        id: 'why',
        header: 'Why it waits',
        cell: () => null,
        kind: 'grow',
        long: (j) =>
          j.state === 'queued' ? { full: waitWords(j), short: waitShort(j) } : null,
        secondary: true,
      },
      status,
    ];
  if (kind === 'queue')
    return [
      {
        id: 'order',
        header: 'Order',
        cell: (j) => String(j.queue_position ?? ''),
        sort: (j) => j.queue_position ?? 0,
      },
      repo,
      job,
      branch,
      {
        id: 'starts',
        header: 'Starts',
        // The place in line is the Order column; the cell says only when, as the run list does (UX-72, CRSP210).
        cell: (j) => (j.queue_eta ? startRange(j.queue_eta) : 'not known'),
        sort: (j) => j.queue_eta?.p50 ?? 0,
        secondary: true,
      },
      {
        id: 'why',
        header: 'Why it waits',
        cell: () => null,
        kind: 'grow',
        long: (j) => ({ full: waitWords(j), short: waitShort(j) }),
        secondary: true,
      },
      status,
    ];
  if (kind === 'running')
    return [
      repo,
      job,
      branch,
      where,
      {
        id: 'progress',
        header: 'Progress',
        kind: 'grow',
        cell: (j) => {
          const t = ranSeconds(j, now) ?? 0;
          const f = forecastOf(j);
          const known = f.basis !== 'not_enough_history';
          return (
            <Progress
              label={`${j.job_name}, ${formatDuration(t)} of ${known ? forecastOne(f) : 'an unknown time'}`}
              {...(known ? { value: Math.min(t, f.p50), max: f.p50 } : {})}
            />
          );
        },
        secondary: true,
      },
      ran,
      status,
    ];
  return [
    {
      id: 'finished',
      header: 'Finished',
      cell: (j) => (j.completed_at ? formatShortDateTime(j.completed_at) : 'not known'),
      sort: (j) => j.completed_at ?? '',
    },
    repo,
    job,
    branch,
    where,
    waited,
    ran,
    status,
  ];
}

/** Jobs another runner runs (the old pool, for one) are not crsp's to run: they have their own filter (owner decision D7). */
export const runsElsewhere = (j: Job): boolean => j.runner_origin === 'other';

/** The runner of a job run elsewhere, with its group and labels, in words. */
export function elsewhereWords(j: Job): string {
  const labels = j.runner_labels?.length
    ? `labels ${j.runner_labels.join(', ')}`
    : undefined;
  const group = j.runner_group ? `group ${j.runner_group}` : undefined;
  const detail = [group, labels].filter(Boolean).join('; ');
  return `${j.runner_name ?? 'another runner'}${detail ? ` (${detail})` : ''}`;
}

const elsewhereColumn: Column<Job> = {
  id: 'elsewhere',
  header: 'Runner and labels',
  cell: (j) => elsewhereWords(j),
  sort: (j) => j.runner_name ?? '',
  kind: 'grow',
  grow: 2,
};

type View = 'runs' | 'jobs';

/** Which list the reader sees: runs by default; a filter only jobs have (runner, machine, type) opens the jobs. */
function useView(): { view: View; set: (v: View) => void } {
  const [params, setParams] = useSearchParams();
  const jobOnly = ['machine', 'runner', 'runner_kind', 'backend', 'job_type'].some((k) =>
    params.get(k),
  );
  const raw = params.get('view');
  const view: View = raw === 'jobs' || raw === 'runs' ? raw : jobOnly ? 'jobs' : 'runs';
  return {
    view,
    set: (v) => {
      const next = new URLSearchParams(params);
      next.set('view', v);
      setParams(next, { replace: true });
    },
  };
}

function ViewSwitch({ view, onChange }: { view: View; onChange: (v: View) => void }) {
  return (
    <Segmented<View>
      label="Show runs or jobs"
      options={[
        { value: 'runs', label: 'Runs' },
        { value: 'jobs', label: 'Jobs' },
      ]}
      value={view}
      onChange={(v) => onChange(v === '' ? 'runs' : v)}
      allLabel={null}
    />
  );
}

type StateFilter = 'all' | 'queued' | 'running' | 'done';
const STATES: readonly { value: StateFilter; label: string }[] = [
  { value: 'all', label: 'All' },
  { value: 'queued', label: 'Queue' },
  { value: 'running', label: 'Running' },
  { value: 'done', label: 'Done' },
];
const KIND_OF: Record<Exclude<StateFilter, 'all'>, Kind> = {
  queued: 'queue',
  running: 'running',
  done: 'done',
};
const TITLES: Record<StateFilter, string> = {
  all: 'All',
  queued: 'Queue',
  running: 'Running',
  done: 'Done',
};
const HINTS: Record<StateFilter, Record<View, string>> = {
  all: {
    runs: 'Waiting runs first, then running, then the latest finished. Open a run to see its jobs.',
    jobs: 'Waiting jobs first, then running, then the latest finished.',
  },
  queued: {
    runs: 'One row for each run that has jobs waiting: its place in line, when it should start, and why it waits.',
    jobs: 'Every waiting job in the order it will be placed. A job that waits says why and when it should start.',
  },
  running: {
    runs: 'One row for each run in progress: jobs done of all, what runs now, and when it should end.',
    jobs: 'What each job does now, on which runner, and how far it is against its forecast.',
  },
  done: {
    runs: 'The last finished runs, newest first. Open a run to see its jobs.',
    jobs: 'The last finished jobs, newest first.',
  },
};
const EMPTY: Record<StateFilter, string> = {
  all: 'No run yet. Runs show here when a workflow has run on the pool.',
  queued: 'Nothing is waiting. Every job has a slot.',
  running: 'No run is in progress.',
  done: 'No finished runs yet. Runs show here when a workflow has run on the pool.',
};
const WHAT: Record<StateFilter, string> = {
  all: 'the list',
  queued: 'the queue',
  running: 'the running list',
  done: 'the done list',
};
const GROUPS: readonly { value: GroupBy; label: string }[] = [
  { value: 'repository', label: 'Repository' },
  { value: 'machine', label: 'Machine' },
  { value: 'runner', label: 'Runner' },
  { value: 'runner-type', label: 'Runner type' },
  { value: 'job-type', label: 'Job type' },
];

/** Every row of a filter, page after page: the files of the screen hold all of it, not one page. */
async function allPages<T>(
  read: (
    cursor: string | undefined,
  ) => Promise<{ items: T[]; next_cursor?: string | undefined }>,
): Promise<T[]> {
  const out: T[] = [];
  let cursor: string | undefined;
  for (let page = 0; page < 100; page += 1) {
    const res = await read(cursor);
    out.push(...res.items);
    cursor = res.next_cursor;
    if (!cursor) break;
  }
  return out;
}

const DAY_MS = 86_400_000;

interface Part {
  on: boolean;
  result: Loaded<unknown>;
  reload: () => void;
}

/** The lists of one screen as one: loading until every list that is asked for is there, failed when one failed. */
function together(parts: readonly Part[]): { result: Loaded<true>; reload: () => void } {
  const on = parts.filter((p) => p.on);
  const reload = () => on.forEach((p) => p.reload());
  const loading = on.some((p) => p.result.state === 'loading');
  if (loading) return { result: { state: 'loading' }, reload };
  const bad = on.find((p) => p.result.state === 'failed');
  const loadedAt = Date.now();
  if (bad && bad.result.state === 'failed') {
    const had = on.every(
      (p) => p.result.state !== 'failed' || p.result.data !== undefined,
    );
    return {
      result: {
        state: 'failed',
        error: bad.result.error,
        ...(had ? { data: true as const, loadedAt } : {}),
      },
      reload,
    };
  }
  return { result: { state: 'ready', data: true, loadedAt }, reload };
}

const dataOf = <T,>(r: Loaded<T>, off: boolean): T | undefined =>
  off ? undefined : r.state === 'loading' ? undefined : (r as { data?: T }).data;

const secondsText = (j: Job, now: number) => secondsOrNone(ranSeconds(j, now));

function RunDetail({ r }: { r: RunRow }) {
  const s = runStatus(r);
  const waiting = r.state === 'queued' ? r.queued[0] : undefined;
  return (
    <>
      <Facts
        items={[
          { name: 'Repository', value: r.repository },
          { name: 'Workflow', value: r.workflow },
          {
            name: 'Run',
            value: `#${r.id}${r.attempt > 1 ? `, attempt ${r.attempt}` : ''}`,
          },
          { name: 'Branch', value: r.branch },
          { name: 'Commit', value: commitWords(r) },
          { name: 'Event', value: eventWords(r) },
          { name: 'State', value: <Status kind={s.kind} word={s.word} /> },
          ...(followUpRunning(r)
            ? [
                {
                  name: 'Still running',
                  value: <Status kind="running" word={FOLLOW_UP_RUNNING} />,
                },
              ]
            : []),
          ...(r.startedNext?.length
            ? [{ name: 'Started next', value: startedNextWords(r.startedNext) }]
            : []),
          {
            name: 'Jobs done',
            value:
              r.jobsTotal === undefined
                ? 'not known'
                : `${r.jobsDone ?? 0} of ${r.jobsTotal}`,
          },
          {
            name:
              r.state === 'queued'
                ? 'Starts'
                : r.state === 'running'
                  ? 'Ends'
                  : 'Finished',
            value: runWhen(r),
          },
          ...(waiting ? [{ name: 'Why it waits', value: waitWords(waiting) }] : []),
          ...(r.running.length
            ? [
                {
                  name: 'Running now',
                  value: r.running.map((j) => j.job_name).join(', '),
                },
              ]
            : []),
        ]}
      />
      <LinkButton to={runPath(r)}>Open run</LinkButton>
    </>
  );
}

function JobDetail({ j, now }: { j: Job; now: number }) {
  const s = jobStatus(j);
  return (
    <>
      <Facts
        items={[
          { name: 'Repository', value: j.repository },
          { name: 'Workflow', value: j.workflow ?? 'workflow' },
          {
            name: 'Job',
            value: `${j.job_name}${j.run_attempt > 1 ? `, attempt ${j.run_attempt}` : ''}`,
          },
          { name: 'Run', value: `#${j.run_id}` },
          { name: 'Branch', value: j.branch ?? 'not known' },
          { name: 'Runner', value: runsElsewhere(j) ? elsewhereWords(j) : whereWords(j) },
          { name: 'State', value: <Status kind={s.kind} word={s.word} /> },
          ...(j.state === 'queued'
            ? [
                {
                  name: 'Starts',
                  value: j.queue_eta ? startRange(j.queue_eta) : 'not known',
                },
                { name: 'Why it waits', value: waitWords(j) },
              ]
            : [
                { name: 'Waited', value: secondsOrNone(waitedSeconds(j, now)) },
                { name: 'Ran', value: secondsText(j, now) },
              ]),
        ]}
      />
      <LinkButton to={`/jobs/${j.id}`}>Open job</LinkButton>
    </>
  );
}

type Tab = 'live' | 'done' | 'elsewhere';

function JobsListTab({ tab }: { tab: Tab }) {
  const f = useFilters();
  const client = useClient();
  const { view: chosen, set } = useView();
  const [params, setParams] = useSearchParams();
  const change = (edit: (next: URLSearchParams) => void) => {
    const next = new URLSearchParams(params);
    edit(next);
    setParams(next, { replace: true });
  };
  // Run elsewhere lists jobs: the API has no runner_origin filter, so this one filters in the browser.
  const elsewhere = tab === 'elsewhere';
  const view: View = elsewhere ? 'jobs' : chosen;
  const rawState = params.get('state');
  const state: StateFilter =
    STATES.find((s) => s.value === rawState)?.value ?? (tab === 'done' ? 'done' : 'all');
  const rawGroup = params.get('group');
  const group =
    tab === 'done' ? GROUPS.find((g) => g.value === rawGroup)?.value : undefined;
  // The words are kept in the page, not in the address: an address that changes at every key draws the page again.
  const [q, setQuery] = useState(params.get('q') ?? '');
  const mine = (items: Job[], s: Exclude<StateFilter, 'all'>): Job[] =>
    elsewhere
      ? items.filter(runsElsewhere)
      : s === 'done'
        ? items
        : items.filter((j) => !runsElsewhere(j));
  const days = RANGES.some((r) => r.value === params.get('days'))
    ? (params.get('days') as string)
    : String(KEPT_DAYS);
  const [shown, setShown] = useState(DONE_STEP);
  const want = (s: Exclude<StateFilter, 'all'>) => state === 'all' || state === s;
  // The range starts at a whole minute, so the question stays the same between two renders and the list does not
  // ask again on every render.
  const minute = Math.floor(Date.now() / 60_000) * 60_000;
  const from = new Date(minute - Number(days) * DAY_MS).toISOString();
  const wait = !f.ready || f.unknown || group !== undefined;
  const queuedQuery: JobsQuery = { ...f.query, state: 'queued', limit: 100 };
  const runningQuery: JobsQuery = { ...f.query, state: 'running', limit: 100 };
  const doneQuery: JobsQuery = { ...f.query, state: 'done', limit: shown, from };
  // Runs take the repository and the branch only; a filter on a runner or a machine shows jobs.
  const runsOf = (s: 'running' | 'done'): RunsQuery => ({
    state: s,
    limit: s === 'done' ? shown : 100,
    ...(f.query.repository_id !== undefined
      ? { repository_id: f.query.repository_id }
      : {}),
    ...(f.query.branch ? { branch: f.query.branch } : {}),
    ...(s === 'done' ? { from } : {}),
  });
  const queued = useJobList(queuedQuery, true, wait || !want('queued'));
  const runningJobs = useJobList(runningQuery, true, wait || !want('running'));
  const doneJobs = useJobList(doneQuery, false, wait || !want('done') || view !== 'jobs');
  const runningRuns = useRunList(
    runsOf('running'),
    true,
    wait || !want('running') || view !== 'runs',
  );
  const doneRuns = useRunList(
    runsOf('done'),
    false,
    wait || !want('done') || view !== 'runs',
  );
  const navigate = useNavigate();
  const live = Date.now();
  const unit = view === 'runs' ? 'runs' : 'jobs';
  const all = together([
    { on: !wait && want('queued'), result: queued.result, reload: queued.reload },
    {
      on: !wait && want('running'),
      result: runningJobs.result,
      reload: runningJobs.reload,
    },
    {
      on: !wait && want('running') && view === 'runs',
      result: runningRuns.result,
      reload: runningRuns.reload,
    },
    {
      on: !wait && want('done') && view === 'jobs',
      result: doneJobs.result,
      reload: doneJobs.reload,
    },
    {
      on: !wait && want('done') && view === 'runs',
      result: doneRuns.result,
      reload: doneRuns.reload,
    },
  ]);

  const exportRows = async () => {
    const runsQuery = runsOf('done');
    if (view === 'runs') {
      const rows = await allPages((cursor) =>
        unwrap(
          client.GET('/api/v1/runs', {
            params: {
              query: { ...runsQuery, limit: 100, ...(cursor ? { cursor } : {}) },
            },
          }),
        ),
      );
      return csvFile(
        'crsp-done-runs.csv',
        [
          'repository',
          'workflow',
          'run',
          'attempt',
          'branch',
          'commit',
          'event',
          'jobs_done',
          'jobs_total',
          'total_seconds',
          'result',
        ],
        rows.map((r) => {
          const row = fromSummary(r);
          return [
            r.repository,
            r.workflow,
            r.id,
            r.attempt,
            r.branch,
            r.head_sha.slice(0, 7),
            r.pull_request ? `PR ${r.pull_request}` : r.trigger,
            r.jobs_done,
            r.jobs_total,
            Math.round(elapsedSeconds(row, live)),
            jobStatus(r).word,
          ];
        }),
      );
    }
    const { limit: _limit, ...filterOnly } = doneQuery;
    const res = await client.GET('/api/v1/exports/jobs', {
      params: { query: { ...filterOnly, format: 'csv' } },
      parseAs: 'blob',
    });
    if (!res.response.ok) {
      const env = res.error as { error?: { message?: string } } | undefined;
      throw new Error(
        env?.error?.message ?? `The controller answered ${res.response.status}.`,
      );
    }
    return {
      blob: res.data as Blob,
      filename: fileNameOf(res.response.headers.get('Content-Disposition')),
    };
  };

  // The rows of every state in scope: waiting first, then running, then the latest finished.
  const runRows: RunRow[] = [
    ...(want('queued')
      ? fromQueuedJobs(mine(dataOf(queued.result, false)?.items ?? [], 'queued'))
      : []),
    ...(want('running')
      ? (dataOf(runningRuns.result, false)?.items ?? []).map((r) =>
          fromSummary(
            r,
            new Map(),
            byRun(mine(dataOf(runningJobs.result, false)?.items ?? [], 'running')),
          ),
        )
      : []),
    ...(want('done')
      ? (dataOf(doneRuns.result, false)?.items ?? []).map((r) => fromSummary(r))
      : []),
  ];
  const jobRows: Job[] = [
    ...(want('queued') ? mine(dataOf(queued.result, false)?.items ?? [], 'queued') : []),
    ...(want('running')
      ? mine(dataOf(runningJobs.result, false)?.items ?? [], 'running')
      : []),
    ...(want('done') ? mine(dataOf(doneJobs.result, false)?.items ?? [], 'done') : []),
  ];
  const donePage =
    view === 'runs' ? dataOf(doneRuns.result, false) : dataOf(doneJobs.result, false);
  const canMore =
    want('done') && donePage?.next_cursor !== undefined && shown < DONE_KEPT;
  const rowCount = view === 'runs' ? runRows.length : jobRows.length;
  const doneCount = (view === 'runs' ? runRows : jobRows).filter(
    (r) => r.state === 'done',
  ).length;

  const filters = (
    <>
      <Segmented<StateFilter>
        label="State"
        options={STATES}
        value={state}
        onChange={(v) => {
          change((n) => n.set('state', v || 'all'));
          setShown(DONE_STEP);
        }}
        allLabel={null}
      />
      {!elsewhere ? (
        <ViewSwitch view={view} onChange={set} />
      ) : (
        <HelpTerm term="elsewhere" />
      )}
    </>
  );

  // Done adds its own choices, in a bar of their own so the bar of the list stays one row: the range of the finished
  // work, and one Group by choice in place of the five By tabs (UX-74).
  const doneBar =
    tab === 'done' ? (
      <ListToolbar
        label="Done"
        filters={
          <>
            <Segmented
              label="Range"
              options={RANGES}
              value={days}
              onChange={(v) => {
                change((n) => n.set('days', v || String(KEPT_DAYS)));
                setShown(DONE_STEP);
              }}
              allLabel={null}
            />
            <Segmented<GroupBy>
              label="Group by"
              options={GROUPS}
              value={group ?? ''}
              onChange={(v) => change((n) => (v ? n.set('group', v) : n.delete('group')))}
              allLabel="None"
            />
          </>
        }
      />
    ) : null;

  const caption = `${TITLES[state]} ${unit}`;
  const below = (
    <>
      {canMore ? (
        <Button onPress={() => setShown((n) => Math.min(DONE_KEPT, n + DONE_STEP))}>
          Show 25 more
        </Button>
      ) : null}
      {want('done') && rowCount > 0 ? (
        <Text tone="muted">
          {`Showing ${doneCount} of ${donePage?.total ?? doneCount} finished ${unit} in the last ${days} days. ${
            donePage?.next_cursor !== undefined && shown >= DONE_KEPT
              ? `A page holds at most ${DONE_KEPT}: choose a shorter range or a filter to see others. `
              : ''
          }Jobs are kept ${KEPT_DAYS} days. ${RETENTION_WORDS}`}
        </Text>
      ) : null}
    </>
  );
  const emptyWords = elsewhere
    ? 'No job runs on another runner.'
    : f.note
      ? `No ${unit.slice(0, -1)} matches this filter.`
      : EMPTY[state];
  const common = {
    label: elsewhere ? 'Run elsewhere' : TITLES[state],
    title: elsewhere
      ? 'Run elsewhere'
      : state === 'all'
        ? view === 'runs'
          ? 'Runs'
          : 'Jobs'
        : TITLES[state],
    hint: HINTS[state][view],
    query: q,
    onQuery: setQuery,
    filters,
    count: view === 'runs' ? 'One row per run' : 'One row per job',
    caption,
    below,
    empty: q ? undefined : emptyWords,
    expandLabel: view === 'runs' ? 'Why' : 'Details',
  };
  const state1 = state === 'all' ? undefined : KIND_OF[state];

  const body = () => {
    if (group) return <GroupTab by={group} />;
    if (f.unknown)
      return (
        <Empty title="This repository is not on the list of repositories, so no run belongs to it." />
      );
    if (!f.ready) return null;
    return (
      <Load result={all.result} what={WHAT[state]} reload={all.reload}>
        {() =>
          view === 'runs' ? (
            <SearchTable<RunRow>
              {...common}
              rows={runRows}
              text={(r) =>
                [
                  r.repository,
                  r.workflow,
                  r.branch,
                  r.task_id ?? '',
                  `#${r.id}`,
                  r.state,
                  runStatus(r).word,
                  ...r.running.map((j) => j.job_name),
                  ...r.queued.map((j) => j.job_name),
                ].join(' ')
              }
              fields={{
                repo: (r) => r.repository,
                state: (r) => `${r.state} ${runStatus(r).word}`,
                runner: (r) =>
                  r.running.map((j) => j.runner?.name ?? j.runner_name ?? '').join(' '),
                branch: (r) => r.branch,
                job: (r) =>
                  [
                    r.workflow,
                    ...r.running.map((j) => j.job_name),
                    ...r.queued.map((j) => j.job_name),
                  ].join(' '),
              }}
              columns={runColumns(state1 ?? 'all', live)}
              rowKey={(r) => `${r.id}-${r.attempt}`}
              rowLabel={(r) => `${r.workflow} run ${r.id}`}
              onOpen={(r) => void navigate(runPath(r))}
              action={{
                label: 'Open run',
                to: runPath,
                name: (r) => `${r.workflow} run ${r.id}`,
              }}
              detail={{
                title: (r) => `${r.repository} #${r.id}`,
                render: (r) => <RunDetail r={r} />,
              }}
            />
          ) : (
            <SearchTable<Job>
              {...common}
              rows={jobRows}
              text={(j) =>
                [
                  j.repository,
                  j.workflow ?? '',
                  j.job_name,
                  j.branch ?? '',
                  `#${j.run_id}`,
                  j.state,
                  jobStatus(j).word,
                  whereWords(j),
                  elsewhereWords(j),
                ].join(' ')
              }
              fields={{
                repo: (j) => j.repository,
                state: (j) => `${j.state} ${jobStatus(j).word}`,
                runner: (j) => `${whereWords(j)} ${j.runner_name ?? ''}`,
                branch: (j) => j.branch ?? '',
                job: (j) => `${j.workflow ?? ''} ${j.job_name}`,
              }}
              columns={withElsewhere(jobColumns(state1 ?? 'all', live), elsewhere)}
              rowKey={(j) => String(j.id)}
              rowLabel={(j) => j.job_name}
              onOpen={(j) => void navigate(`/jobs/${j.id}`)}
              action={{
                label: 'Open job',
                to: (j) => `/jobs/${j.id}`,
                name: (j) => j.job_name,
              }}
              detail={{
                title: (j) => `${j.repository} ${j.job_name}`,
                render: (j) => <JobDetail j={j} now={live} />,
              }}
            />
          )
        }
      </Load>
    );
  };

  return (
    <>
      <Stack>
        <FilterNote note={f.note} to={f.clearTo} />
        {doneBar}
        {body()}
      </Stack>
      <ActionBar
        primary={
          tab === 'done' ? (
            <ExportButton
              what={`done ${unit}`}
              run={exportRows}
              label="Export CSV"
              primary
            />
          ) : (
            <LinkButton to="/pool/runners/add" kind="primary">
              Add runners
            </LinkButton>
          )
        }
      />
    </>
  );
}

/** Run elsewhere names the runner and its labels where a crsp list names the runner and machine. */
function withElsewhere(cols: Column<Job>[], on: boolean): Column<Job>[] {
  if (!on) return cols;
  if (cols.some((c) => c.id === 'where'))
    return cols.map((c) => (c.id === 'where' ? elsewhereColumn : c));
  return cols.flatMap((c) => (c.id === 'branch' ? [c, elsewhereColumn] : [c]));
}

export const LiveTab = () => <JobsListTab tab="live" />;
export const DoneTab = () => <JobsListTab tab="done" />;
export const ElsewhereTab = () => <JobsListTab tab="elsewhere" />;
