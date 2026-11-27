// Analytics > Run elsewhere (LOOK65): jobs that another runner ran, in a plain table: Job, Repository, Runner, Took,
// Result. No search, no filter, no toggle: the list is short and the prototype shows only the table.
// The API has no runner_origin filter, so the browser keeps the jobs of other runners (owner decision D7).
import {
  Code,
  Empty,
  Section,
  Status,
  Table,
  formatDuration,
  type Column,
} from '@crsp/ui';
import { Load } from '../common';
import { useJobList } from './api';
import { jobStatus, type Job } from './words';

export const ELSEWHERE_WORDS = {
  title: 'Run elsewhere',
  empty: 'No job runs on another runner.',
  took: 'not known',
  runner: 'another runner',
} as const;

const KEPT_DAYS = 30;
const LIMIT = 100;

/** The runner in words: its name with its group and labels, as the job says. */
export function runnerOf(j: Job): string {
  const detail = [
    j.runner_group ? `group ${j.runner_group}` : undefined,
    j.runner_labels?.length ? `labels ${j.runner_labels.join(', ')}` : undefined,
  ]
    .filter(Boolean)
    .join('; ');
  return `${j.runner_name ?? ELSEWHERE_WORDS.runner}${detail ? ` (${detail})` : ''}`;
}

export const elsewhereColumns: Column<Job>[] = [
  {
    id: 'job',
    header: 'Job',
    cell: (j) => <Code>{j.job_name}</Code>,
    sort: (j) => j.job_name,
    title: (j) => j.job_name,
    kind: 'grow',
  },
  {
    id: 'repository',
    header: 'Repository',
    cell: (j) => j.repository,
    sort: (j) => j.repository,
    kind: 'grow',
  },
  {
    id: 'runner',
    header: 'Runner',
    cell: (j) => runnerOf(j),
    sort: (j) => j.runner_name ?? '',
    kind: 'grow',
    grow: 2,
  },
  {
    id: 'took',
    header: 'Took',
    cell: (j) =>
      j.run_seconds === undefined ? ELSEWHERE_WORDS.took : formatDuration(j.run_seconds),
    sort: (j) => j.run_seconds ?? 0,
    kind: 'fit',
  },
  {
    id: 'result',
    header: 'Result',
    cell: (j) => {
      const s = jobStatus(j);
      return <Status kind={s.kind} word={s.word} />;
    },
    sort: (j) => jobStatus(j).word,
    kind: 'fit',
  },
];

export function ElsewhereTab() {
  const from = new Date(
    Math.floor(Date.now() / 60_000) * 60_000 - KEPT_DAYS * 86_400_000,
  ).toISOString();
  const queued = useJobList({ state: 'queued', limit: LIMIT }, true);
  const running = useJobList({ state: 'running', limit: LIMIT }, true);
  const done = useJobList({ state: 'done', limit: LIMIT, from });
  const loading = [queued, running, done].find((r) => r.result.state !== 'ready');
  return (
    <Load
      result={(loading ?? done).result}
      what="the jobs that run elsewhere"
      reload={(loading ?? done).reload}
    >
      {() => {
        const rows = [queued, running, done]
          .flatMap((r) => (r.result.state === 'ready' ? r.result.data.items : []))
          .filter((j) => j.runner_origin === 'other');
        return (
          <Section title={ELSEWHERE_WORDS.title} count={rows.length}>
            {rows.length === 0 ? (
              <Empty title={ELSEWHERE_WORDS.empty} />
            ) : (
              <Table
                caption={ELSEWHERE_WORDS.title}
                oneLine
                columns={elsewhereColumns}
                rows={rows}
                rowKey={(j) => String(j.id)}
                rowLabel={(j) => j.job_name}
              />
            )}
          </Section>
        );
      }}
    </Load>
  );
}
