// The Jobs table of the run page (OPS42): every job of the run once, as the text alternative of the Job graph and of
// the Timeline (UX-41). One line a row (UX-72); the status is a word and a glyph (UX-58).
import { Status, Table, formatDuration, type Column } from '@crsp/ui';
import type { Schemas } from '../../api/api';
import { nodeId, stages } from './runViews';
import {
  forecastOf,
  forecastOne,
  jobStatus,
  ranSeconds,
  waitedSeconds,
  type Job,
} from './words';

type RunItem = Schemas['RunItem'];

/** The id the graph and the timeline point to (aria-describedby). */
export const RUN_JOBS_ID = 'run-jobs-table';

export interface RunJobRow {
  job: Job;
  key: string;
  stage: number;
  needs: string;
  critical: boolean;
  waited: number | undefined;
  ran: number | undefined;
}

/** One row per job, in stage order, then in the order the run lists them. */
export function runJobRows(run: RunItem, now: number): RunJobRow[] {
  const depth = stages(run.jobs);
  const names = new Map(run.jobs.map((j) => [nodeId(j), j.job_name]));
  const crit = new Set(run.critical_path);
  return run.jobs
    .map((job, i) => ({
      i,
      row: {
        job,
        key: String(job.id),
        stage: (depth.get(nodeId(job)) ?? 0) + 1,
        needs: (job.needs ?? []).map((n) => names.get(n) ?? n).join(', '),
        critical: crit.has(nodeId(job)),
        waited: waitedSeconds(job, now),
        ran: ranSeconds(job, now),
      },
    }))
    .sort((a, b) => a.row.stage - b.row.stage || a.i - b.i)
    .map((x) => x.row);
}

const duration = (n: number | undefined): string =>
  n === undefined ? 'not yet' : formatDuration(n);

export function RunJobsTable({
  run,
  now,
  onOpen,
}: {
  run: RunItem;
  now: number;
  onOpen: (j: Job) => void;
}) {
  const columns: Column<RunJobRow>[] = [
    {
      id: 'job',
      header: 'Job',
      cell: (r) => r.job.job_name,
      sort: (r) => r.job.job_name,
      kind: 'grow',
    },
    { id: 'stage', header: 'Stage', cell: (r) => String(r.stage), sort: (r) => r.stage },
    {
      id: 'needs',
      header: 'Needs',
      cell: (r) => r.needs || 'nothing',
      kind: 'grow',
      secondary: true,
    },
    {
      id: 'status',
      header: 'Status',
      cell: (r) => {
        const s = jobStatus(r.job);
        return <Status kind={s.kind} word={s.word} />;
      },
      sort: (r) => jobStatus(r.job).word,
    },
    {
      id: 'runner',
      header: 'Runner',
      cell: (r) => r.job.runner?.name ?? 'not placed yet',
      kind: 'grow',
      secondary: true,
    },
    { id: 'waited', header: 'Waited', cell: (r) => duration(r.waited), secondary: true },
    { id: 'ran', header: 'Ran', cell: (r) => duration(r.ran) },
    {
      id: 'forecast',
      header: 'Forecast',
      cell: (r) => forecastOne(forecastOf(r.job)),
      kind: 'grow',
      secondary: true,
    },
    {
      id: 'critical',
      header: 'Critical path',
      cell: (r) => (r.critical ? 'critical path' : 'no'),
      secondary: true,
    },
  ];
  return (
    <div id={RUN_JOBS_ID}>
      <Table
        caption={`Jobs of run ${run.id}`}
        columns={columns}
        rows={runJobRows(run, now)}
        rowKey={(r) => r.key}
        onOpen={(r) => onOpen(r.job)}
        action={{
          label: 'Open job',
          to: (r) => `/pool/jobs/${r.job.id}`,
          name: (r) => r.job.job_name,
        }}
        oneLine
      />
    </div>
  );
}
