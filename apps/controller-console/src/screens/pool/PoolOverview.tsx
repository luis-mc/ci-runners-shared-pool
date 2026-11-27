// Pool > Overview (LOOK56, option B; LOOK70: no timeline, as the approved prototype): the queue plan, the running jobs and the routing labels in use. It only shows
// and links (UX-52).
import {
  Empty,
  Section,
  Status,
  Table,
  TextLink,
  formatDuration,
  type Column,
} from '@crsp/ui';
import { unwrap, useClient, type Schemas } from '../../api/api';
import { useLoad } from '../../api/hooks';
import { useNow } from '@crsp/ui';
import { Load } from '../common';
import { jobStatus, ranSeconds, type Job } from '../jobs/words';
import { WithOverview } from './OverviewState';
import { needsYou, placeWords, startsAbout, waitWords } from './poolWords';

type Runner = Schemas['Runner'];

export function PoolOverview() {
  return (
    <WithOverview>
      {(o, readAt) => (
        <>
          <QueuePlanTable queued={o.queued ?? []} readAt={readAt} />
          <RunningTable running={o.running} />
          <RoutingLabels jobs={[...o.running, ...(o.queued ?? [])]} />
        </>
      )}
    </WithOverview>
  );
}

function QueuePlanTable({ queued, readAt }: { queued: readonly Job[]; readAt: number }) {
  const rows = [...queued].sort(
    (a, b) => (a.queue_position ?? 1e9) - (b.queue_position ?? 1e9),
  );
  const columns: Column<Job>[] = [
    { id: 'n', header: '#', cell: (j) => String(rows.indexOf(j) + 1) },
    {
      id: 'job',
      header: 'Job',
      cell: (j) => `${j.job_name} ${j.repository} #${j.run_id}`,
    },
    {
      id: 'why',
      header: 'Why it waits',
      cell: (j) => (
        <>
          {waitWords(j)}
          {needsYou(j) ? <Status kind="waiting" word="Needs you" /> : null}
        </>
      ),
    },
    {
      id: 'start',
      header: 'Start',
      cell: (j) =>
        needsYou(j) ? 'Does not start by itself' : startsAbout(j.queue_eta, readAt),
    },
    { id: 'where', header: 'Machine and runner', cell: (j) => placeWords(j) },
  ];
  return (
    <Section
      title="Queue plan"
      count={rows.length}
      hint="In the order crsp will start them."
      actions={<TextLink to="/pool/queue">Open Queue Analysis</TextLink>}
    >
      {rows.length === 0 ? (
        <Empty title="Nothing is waiting. Every job has a slot." />
      ) : (
        <Table
          caption="Execution plan"
          columns={columns}
          rows={rows}
          rowKey={(j) => String(j.id)}
        />
      )}
    </Section>
  );
}

export function RunningTable({
  running,
  title = 'Running',
}: {
  running: readonly Job[];
  title?: string;
}) {
  const now = useNow();
  const columns: Column<Job>[] = [
    {
      id: 'job',
      header: 'Job',
      cell: (j) => <TextLink to={`/pool/jobs/${j.id}`}>{j.job_name}</TextLink>,
    },
    { id: 'where', header: 'Machine and runner', cell: (j) => placeWords(j) },
    {
      id: 'state',
      header: 'State',
      cell: (j) => {
        const s = jobStatus(j);
        return <Status kind={s.kind} word={s.word} />;
      },
    },
    {
      id: 'run',
      header: 'Run timer',
      cell: (j) => {
        const s = ranSeconds(j, now);
        return s === undefined ? 'not yet' : formatDuration(Math.round(s));
      },
    },
  ];
  return (
    <Section title={title} count={running.length}>
      {running.length === 0 ? (
        <Empty title="No jobs running. Jobs show here when a workflow runs on the pool." />
      ) : (
        <Table
          caption="Running jobs"
          columns={columns}
          rows={running}
          rowKey={(j) => String(j.id)}
        />
      )}
    </Section>
  );
}

interface LabelRow {
  label: string;
  jobs: string[];
  runners: string[];
}

function RoutingLabels({ jobs }: { jobs: readonly Job[] }) {
  const client = useClient();
  const runners = useLoad(() => unwrap(client.GET('/api/v1/runners')), [client]);
  const columns: Column<LabelRow>[] = [
    { id: 'label', header: 'Label', cell: (r) => r.label },
    { id: 'jobs', header: 'Jobs', cell: (r) => r.jobs.join(', ') || 'none' },
    {
      id: 'runners',
      header: 'Runner carrying it',
      cell: (r) => r.runners.join(', ') || 'none yet',
    },
  ];
  return (
    <Section title="Routing labels in use">
      <Load result={runners.result} what="the runners" reload={runners.reload}>
        {(r) => {
          const rows = labelRows(jobs, r.items);
          return rows.length === 0 ? (
            <Empty title="No job carries a routing label right now." />
          ) : (
            <Table
              caption="Routing labels"
              columns={columns}
              rows={rows}
              rowKey={(x) => x.label}
            />
          );
        }}
      </Load>
    </Section>
  );
}

/** The labels of the jobs in the pool, the jobs that carry each, and the runners that carry it too. */
export function labelRows(jobs: readonly Job[], runners: readonly Runner[]): LabelRow[] {
  const out = new Map<string, LabelRow>();
  for (const j of jobs)
    for (const label of j.labels) {
      const row = out.get(label) ?? { label, jobs: [], runners: [] };
      row.jobs.push(j.job_name);
      out.set(label, row);
    }
  for (const row of out.values())
    row.runners = runners.filter((r) => r.labels.includes(row.label)).map((r) => r.name);
  return [...out.values()].sort((a, b) => a.label.localeCompare(b.label));
}
