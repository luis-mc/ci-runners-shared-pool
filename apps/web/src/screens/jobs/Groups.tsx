// Jobs sub-sections: the jobs grouped by repository, machine, runner, runner type or job type. One table each:
// running, queued, done, failed, median wait, median and slow-end run time, and a small trend of the last run times.
// The controller groups the jobs of the last 90 days; a row opens the list of its jobs with the group's filter.
import {
  Badge,
  Empty,
  Section,
  Sparkline,
  Stack,
  Table,
  Text,
  formatDuration,
  type Column,
} from '@crsp/ui';
import type { Schemas } from '../../api/api';
import { Load } from '../common';
import { useGroups, type GroupBy } from './api';
import { RETENTION_WORDS } from './words';

type Row = Schemas['JobGroupRow'];
export type { GroupBy };

interface Spec {
  title: string;
  what: string;
}

const SPECS: Record<GroupBy, Spec> = {
  repository: { title: 'Repositories', what: 'repository' },
  machine: { title: 'Machines', what: 'machine' },
  runner: { title: 'Runners', what: 'runner' },
  'runner-type': { title: 'Runner types', what: 'runner type' },
  'job-type': { title: 'Job types', what: 'job type' },
};

/** "runner_kind=persistent&backend=docker" as an address query, each value encoded. */
export function filterQuery(filter: string): string {
  return filter
    .split('&')
    .map((part) => {
      const at = part.indexOf('=');
      return at < 0
        ? part
        : `${part.slice(0, at)}=${encodeURIComponent(part.slice(at + 1))}`;
    })
    .join('&');
}

/** Where a group row goes: its jobs in the Done list (a repository opens its runs). */
export function groupLink(by: GroupBy, row: Row): string {
  return `/pool/jobs?${by === 'repository' ? '' : 'view=jobs&'}${filterQuery(row.filter)}`;
}

const secs = (s: number | undefined) =>
  s === undefined || s === 0 ? 'not known' : formatDuration(s);

/** A runner type is "persistent/docker": the kind and, after the slash, the backend. */
function splitKind(name: string): { kind: string; backend: string } {
  const at = name.indexOf('/');
  return at < 0
    ? { kind: name, backend: 'not known' }
    : { kind: name.slice(0, at), backend: name.slice(at + 1) };
}

export function GroupTab({ by }: { by: GroupBy }) {
  const spec = SPECS[by];
  const { result, reload } = useGroups(by);
  const columns: Column<Row>[] = [
    {
      id: 'name',
      header:
        by === 'runner-type'
          ? 'Runner type'
          : spec.what[0]?.toUpperCase() + spec.what.slice(1),
      cell: (g) => (by === 'runner-type' ? splitKind(g.name).kind : g.name),
      sort: (g) => g.name,
      kind: 'grow',
      grow: 2,
    },
    ...(by === 'runner-type'
      ? [
          {
            id: 'backend',
            header: 'Backend',
            cell: (g: Row) => splitKind(g.name).backend,
            kind: 'grow' as const,
            secondary: true,
          },
        ]
      : []),
    ...(by === 'job-type'
      ? [
          {
            id: 'weight',
            header: 'Weight',
            cell: (g: Row) => <Badge>{g.light ? 'Light' : 'Heavy'}</Badge>,
            sort: (g: Row) => (g.light ? 0 : 1),
          },
        ]
      : []),
    {
      id: 'running',
      header: 'Running',
      cell: (g) => String(g.running),
      sort: (g) => g.running,
      secondary: true,
    },
    {
      id: 'queued',
      header: 'Queued',
      cell: (g) => String(g.queued),
      sort: (g) => g.queued,
      secondary: true,
    },
    {
      id: 'done',
      header: 'Done',
      cell: (g) => String(g.done),
      sort: (g) => g.done,
      secondary: true,
    },
    {
      id: 'failed',
      header: 'Failed',
      cell: (g) => String(g.failed),
      sort: (g) => g.failed,
    },
    {
      id: 'wait',
      header: 'Median wait',
      cell: (g) => secs(g.wait_p50),
      sort: (g) => g.wait_p50,
      secondary: true,
    },
    {
      id: 'run',
      header: 'Median run',
      cell: (g) => secs(g.run_p50),
      sort: (g) => g.run_p50,
      secondary: true,
    },
    {
      id: 'run90',
      header: 'Slow end of run',
      cell: (g) => secs(g.run_p90),
      sort: (g) => g.run_p90,
      secondary: true,
    },
    {
      id: 'trend',
      header: 'Run times',
      cell: (g) =>
        g.trend.length > 1 ? (
          <Sparkline
            label={`Run times of ${g.name}, last ${g.trend.length} jobs`}
            values={g.trend}
            unit="seconds"
            markLast
          />
        ) : (
          'not enough jobs'
        ),
      secondary: true,
    },
  ];
  return (
    <Load result={result} what={`the jobs by ${spec.what}`} reload={reload}>
      {(data) => (
        <Section
          title={spec.title}
          count={data.items.length}
          hint={`Queued and running jobs, and the jobs done in the last 90 days, grouped by ${spec.what}. The slow end is the run time that 9 in 10 jobs stay under.`}
        >
          <Stack>
            {data.items.length === 0 ? (
              <Empty title="No jobs yet. Jobs show here when a workflow runs on the pool." />
            ) : (
              <Table
                caption={`Jobs by ${spec.what}`}
                columns={columns}
                rows={data.items}
                rowKey={(g) => g.filter}
                oneLine
                action={{
                  label: 'Open',
                  to: (g) => groupLink(by, g),
                  name: (g) => g.name,
                }}
              />
            )}
            {by === 'runner-type' ? (
              <Text tone="muted">
                Persistent runners keep state between jobs; ephemeral runners are fresh
                for every job. The backend is where the runner runs.
              </Text>
            ) : null}
            {by === 'job-type' ? (
              <Text tone="muted">
                A job type is the job name without its matrix values. Light jobs need
                little of a machine, heavy jobs a lot.
              </Text>
            ) : null}
            <Text tone="muted">{RETENTION_WORDS}</Text>
            {by === 'runner' || by === 'machine' ? (
              <Text tone="muted">
                A repository never picks a runner: the pool places each job, so this list
                shows where jobs went.
              </Text>
            ) : null}
          </Stack>
        </Section>
      )}
    </Load>
  );
}
