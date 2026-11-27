// The insight panes of a Repository page (spec 4.4) and the Demand section of Size and backends (spec 4.3).
// CI checks lists the workflow file checks of the latest default-branch commit; Guardrails shows G1 to G7 for the
// project; Job slots words the advice of R5.18 with Review the change as a normal button (the page's one primary
// stays Save, UX-4, UX-71).
import {
  Empty,
  LinkButton,
  Section,
  Stack,
  Table,
  Text,
  TrendChart,
  formatPercent,
  formatShortDateTime,
  Pane,
  type Column,
} from '@crsp/ui';
import { Load } from '../common';
import { useFindings, useGuardrails, usePoolSizing, useWorkflowChecks } from './api';
import { changePath } from './Finding';
import { guardrailWords, secondsWords } from './words';
import type { Schemas } from '../../api/api';

type Check = Schemas['WorkflowCheck'];
type Demand = Schemas['LabelSetDemand'];

const STATUS_WORDS: Record<Check['status'], string> = {
  ok: '✓ pass',
  fail: '✕ fix',
  skip: '– skipped',
};

const checkColumns: Column<Check>[] = [
  {
    id: 'status',
    header: 'Status',
    cell: (c) => STATUS_WORDS[c.status],
    sort: (c) => c.status,
  },
  {
    id: 'check',
    header: 'Rule',
    cell: (c) => c.check_id.replace('workflow.', '').replaceAll('_', ' '),
    kind: 'grow',
  },
  {
    id: 'job',
    header: 'Job',
    cell: (c) => `${c.workflow}${c.job ? ` / ${c.job}` : ''}`,
    kind: 'grow',
    grow: 2,
  },
  {
    id: 'line',
    header: 'Line',
    cell: (c) => (c.line ? String(c.line) : 'none'),
    secondary: true,
  },
  {
    id: 'fix',
    header: 'Fix',
    cell: (c) => c.detail ?? 'nothing to fix',
    kind: 'grow',
    grow: 2,
  },
];

export function CiChecksPane({ repositoryId }: { repositoryId: number }) {
  const { result, reload } = useWorkflowChecks(repositoryId);
  return (
    <Load result={result} what="the CI checks" reload={reload}>
      {(l) => (
        <Pane title="CI checks" count={l.items.length}>
          {l.items.length === 0 ? (
            <Empty title="No workflow file was checked yet." />
          ) : (
            <Stack>
              <Table
                caption="Workflow file checks"
                columns={checkColumns}
                rows={l.items}
                rowKey={(c) => `${c.workflow}-${c.check_id}-${c.job ?? ''}`}
              />
              {l.checked_at ? (
                <Text tone="muted">{`Checked ${formatShortDateTime(l.checked_at)}.`}</Text>
              ) : null}
            </Stack>
          )}
        </Pane>
      )}
    </Load>
  );
}

export function GuardrailsPane({ fullName }: { fullName: string }) {
  const { result, reload } = useGuardrails();
  return (
    <Load result={result} what="the guardrails" reload={reload}>
      {(l) => {
        const row = l.items.find((r) => r.repository === fullName);
        return (
          <Pane title="Guardrails">
            {row ? (
              <Table
                caption="Guardrails of this project"
                columns={[
                  {
                    id: 'g',
                    header: 'Guardrail',
                    cell: (g: Schemas['GuardrailState']) => g.guardrail,
                  },
                  {
                    id: 'state',
                    header: 'State',
                    cell: (g: Schemas['GuardrailState']) => guardrailWords(g),
                    kind: 'grow',
                  },
                ]}
                rows={row.guardrails}
                rowKey={(g) => g.guardrail}
              />
            ) : (
              <Empty title="This project does not report its guardrails yet." />
            )}
          </Pane>
        );
      }}
    </Load>
  );
}

export function JobSlotsPane({ repositoryId }: { repositoryId: number }) {
  const { result, reload } = useFindings(repositoryId);
  return (
    <Load result={result} what="the job slots advice" reload={reload}>
      {(l) => {
        const advice = l.items.find((f) => f.kind === 'repo.slots');
        return (
          <Pane title="Job slots">
            {advice ? (
              <Stack>
                <Text>{advice.fix}</Text>
                <Text tone="muted">{`About ${advice.minutes_saved_week} min a week (estimate).`}</Text>
                <LinkButton to={changePath(advice)}>Review the change</LinkButton>
              </Stack>
            ) : (
              <Text>
                No advice: the limit of this repository did not hold jobs back while slots
                were free.
              </Text>
            )}
          </Pane>
        );
      }}
    </Load>
  );
}

const demandColumns: Column<Demand>[] = [
  {
    id: 'labels',
    header: 'Labels',
    cell: (d) => d.labels.join(', '),
    kind: 'grow',
    grow: 2,
  },
  { id: 'slots', header: 'Slots', cell: (d) => String(d.slots) },
  {
    id: 'use',
    header: 'Busy',
    cell: (d) => formatPercent(Math.round(d.utilisation * 100)),
  },
  {
    id: 'rate',
    header: 'Jobs an hour',
    cell: (d) => d.jobs_per_hour.toFixed(1),
    secondary: true,
  },
  {
    id: 'p50',
    header: 'Wait, middle',
    cell: (d) => secondsWords(d.p50_wait_seconds),
    secondary: true,
  },
  { id: 'p95', header: 'Wait, slow end', cell: (d) => secondsWords(d.p95_wait_seconds) },
];

/** Demand above the machine rows: per label set, and the wait with 1 to 4 more slots (an estimate). */
export function DemandSection() {
  const { result, reload } = usePoolSizing(7);
  return (
    <Load result={result} what="the demand" reload={reload}>
      {(p) => (
        <Section
          title="Demand"
          count={p.label_sets.length}
          hint="From the last 7 days. The waits with more slots are estimates."
        >
          {p.label_sets.length === 0 ? (
            <Empty title="No finished jobs in the last 7 days, so there is no demand to show." />
          ) : (
            <Stack>
              <Table
                caption="Demand by label set"
                columns={demandColumns}
                rows={p.label_sets}
                rowKey={(d) => d.labels.join(',')}
              />
              {p.label_sets.map((d) => (
                <Stack key={d.labels.join(',')}>
                  {d.min_slots_needed ? (
                    <Text>{`The queue grows: at least ${d.min_slots_needed} more slots are needed.`}</Text>
                  ) : null}
                  {d.estimates.map((e) => (
                    <Text key={e.extra_slots}>
                      {`${d.labels.join(', ')}: with ${e.extra_slots} more ${e.extra_slots === 1 ? 'slot' : 'slots'} the p95 wait would be about ${secondsWords(Math.round(e.p95_wait_seconds ?? 0))} (now ${secondsWords(Math.round(d.p95_wait_seconds ?? 0))}). An estimate from the last 7 days.`}
                    </Text>
                  ))}
                  <TrendChart
                    label={`p95 wait per day, ${d.labels.join(', ')}`}
                    unit="seconds"
                    emptyText="No waits in this period."
                    series={[
                      {
                        id: 'p95',
                        label: 'Wait, slow end',
                        points: (d.p95_wait_by_day ?? []).map((v) => ({
                          x: Date.parse(`${v.day}T00:00:00Z`),
                          y: v.value,
                        })),
                      },
                    ]}
                  />
                </Stack>
              ))}
            </Stack>
          )}
        </Section>
      )}
    </Load>
  );
}
