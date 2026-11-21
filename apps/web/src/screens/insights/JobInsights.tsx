// The insight panes of the Job page and the Run page (spec 4.5, 4.6): where a job ran, where its steps came from,
// when crsp last re-read it, its CPU and memory, and the savings of a run.
import {
  Empty,
  Facts,
  Message,
  Section,
  Stack,
  Text,
  TrendChart,
  formatAgo,
  formatDuration,
  formatNumber,
} from '@crsp/ui';
import { Load } from '../common';
import { useJobResources, useRunSavings } from './api';
import type { Schemas } from '../../api/api';
import { MEASURED_WORDS, keyLine } from './words';

type Job = Schemas['Job'];

/** Who ran the job, in words (R5.39). */
export function runsOnWords(
  j: Pick<
    Job,
    | 'runner_origin'
    | 'runner'
    | 'runner_name'
    | 'runner_group'
    | 'runner_labels'
    | 'labels'
  >,
): string {
  if (j.runner_origin === 'crsp' || (!j.runner_origin && j.runner))
    return `crsp runner ${j.runner?.name ?? j.runner_name ?? 'not known'} on ${j.runner?.machine_name ?? 'its machine'}`;
  if (j.runner_origin === 'other') {
    const detail = [
      j.runner_group ? `group ${j.runner_group}` : undefined,
      j.runner_labels?.length ? `labels ${j.runner_labels.join(', ')}` : undefined,
    ]
      .filter(Boolean)
      .join('; ');
    return `Another runner: ${j.runner_name ?? 'not named'}${detail ? ` (${detail})` : ''}`;
  }
  if (j.runner_origin === 'hosted')
    return `GitHub-hosted runner (${j.runner_labels?.[0] ?? j.labels[0] ?? 'label not known'})`;
  return 'not known yet';
}

export const STEPS_SOURCE_WORDS: Record<NonNullable<Job['steps_source']>, string> = {
  events: 'from GitHub events',
  rest: 'read from GitHub',
};

/** The line under the steps waterfall: where the steps came from and when crsp last checked. */
export function StepsSource({ j }: { j: Pick<Job, 'steps_source' | 'checked_at'> }) {
  if (!j.steps_source && !j.checked_at) return null;
  return (
    <Text tone="muted">
      {[
        j.steps_source ? `Steps ${STEPS_SOURCE_WORDS[j.steps_source]}.` : undefined,
        j.checked_at ? `Last checked with GitHub ${formatAgo(j.checked_at)}.` : undefined,
      ]
        .filter(Boolean)
        .join(' ')}
    </Text>
  );
}

export const runsOnFact = (j: Parameters<typeof runsOnWords>[0]) => ({
  name: 'Runs on',
  value: runsOnWords(j),
});

const gb = (b: number): number => b / 1_000_000_000;

/** The measured peaks of one job run, the flags that keep a run out of sizing, and the job-key line (R5.60 to R5.63). */
function MeasuredBlock({ m }: { m: NonNullable<Schemas['JobResources']['measured']> }) {
  return (
    <Stack>
      <Text>{MEASURED_WORDS.title}</Text>
      <Facts
        items={[
          {
            name: MEASURED_WORDS.cpu,
            value: `${formatNumber(m.peak_cpu_cores, 1)} cores`,
          },
          {
            name: MEASURED_WORDS.memory,
            value: `${formatNumber(gb(m.peak_memory_bytes), 1)} GB`,
          },
          { name: MEASURED_WORDS.samples, value: formatNumber(m.sample_count, 0) },
          { name: MEASURED_WORDS.duration, value: formatDuration(m.duration_seconds) },
        ]}
      />
      {m.oom_killed ? (
        <Message tone="needs-you" dismissible={false}>
          {MEASURED_WORDS.oom}
        </Message>
      ) : null}
      {m.cut_short ? (
        <Message tone="note" dismissible={false}>
          {MEASURED_WORDS.cutShort}
        </Message>
      ) : null}
      {m.key ? <Text tone="muted">{keyLine(m.key)}</Text> : null}
    </Stack>
  );
}

/** CPU and memory of a job a crsp runner ran: two trend charts and the peak and p95 as text (UX-17). */
export function ResourcesPane({ jobId }: { jobId: number }) {
  const { result, reload } = useJobResources(jobId);
  return (
    <Load result={result} what="CPU and memory" reload={reload}>
      {(r) => (
        <Section title="CPU and memory">
          {r.samples.length === 0 && !r.measured ? (
            <Empty
              title={r.unknown_reason ?? 'No CPU and memory were measured for this job.'}
            />
          ) : (
            <Stack>
              {r.measured ? <MeasuredBlock m={r.measured} /> : null}
              {r.samples.length === 0 ? null : (
                <>
                  <Facts
                    items={[
                      {
                        name: 'CPU, peak',
                        value: `${formatNumber(r.cpu_peak_cores ?? 0, 1)} cores`,
                      },
                      {
                        name: 'CPU, slow end (p95)',
                        value: `${formatNumber(r.cpu_p95_cores ?? 0, 1)} cores`,
                      },
                      {
                        name: 'Memory, peak',
                        value: `${formatNumber(gb(r.memory_peak_bytes ?? 0), 1)} GB`,
                      },
                      {
                        name: 'Memory, slow end (p95)',
                        value: `${formatNumber(gb(r.memory_p95_bytes ?? 0), 1)} GB`,
                      },
                    ]}
                  />
                  <TrendChart
                    label="CPU cores over the job"
                    unit="count"
                    emptyText="No samples."
                    series={[
                      {
                        id: 'cpu',
                        label: 'CPU cores',
                        points: r.samples.map((s) => ({
                          x: Date.parse(s.at),
                          y: s.cpu_cores,
                        })),
                      },
                    ]}
                  />
                  <TrendChart
                    label="Memory in GB over the job"
                    unit="count"
                    emptyText="No samples."
                    series={[
                      {
                        id: 'mem',
                        label: 'Memory in GB',
                        points: r.samples.map((s) => ({
                          x: Date.parse(s.at),
                          y: gb(s.memory_bytes),
                        })),
                      },
                    ]}
                  />
                </>
              )}
            </Stack>
          )}
        </Section>
      )}
    </Load>
  );
}

const WASTE_TITLE: Record<Schemas['RunWaste']['kind'], string> = {
  superseded: 'This run was superseded',
  duplicate_event: 'This commit ran twice',
  retested_tree: 'This code was tested again',
  fail_fast: 'This run went on after its first failure',
};

/** Savings of a run: up to 3 lines, and a Note for superseded, twice-run or past-first-failure runs (UX-26). */
export function SavingsPane({ runId }: { runId: number }) {
  const { result, reload } = useRunSavings(runId);
  return (
    <Load result={result} what="the savings" reload={reload}>
      {(s) => (
        <Section title="Savings">
          <Stack>
            {s.waste.map((w) => (
              <Message key={w.kind} tone="note" dismissible={false}>
                {`${WASTE_TITLE[w.kind]}: ${w.words}`}
              </Message>
            ))}
            {s.savings.length === 0 ? (
              <Empty title={s.reason ?? 'No change would end this run sooner.'} />
            ) : (
              s.savings.slice(0, 3).map((x) => <Text key={x.job}>{x.words}</Text>)
            )}
          </Stack>
        </Section>
      )}
    </Load>
  );
}
