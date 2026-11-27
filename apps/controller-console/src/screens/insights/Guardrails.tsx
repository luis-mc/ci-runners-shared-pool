// Run insights, Guardrails (idea 6): one row per project, one column per guardrail G1 to G7, the source, a
// sparkline of the known-list count and Open to the project's Repository page (UX-58).
import {
  Empty,
  Section,
  Sparkline,
  Stack,
  Table,
  formatShortDateTime,
  type Column,
} from '@crsp/ui';
import { Load } from '../common';
import { useGuardrails } from './api';
import { useRepositoryFilter } from './InsightsLayout';
import { guardrailWords } from './words';
import type { Schemas } from '../../api/api';

type Row = Schemas['GuardrailStatus'];
const ALL = ['G1', 'G2', 'G3', 'G4', 'G5', 'G6', 'G7'] as const;

export const guardrailColumns: Column<Row>[] = [
  {
    id: 'repo',
    header: 'Project',
    cell: (r) => r.repository,
    sort: (r) => r.repository,
    kind: 'grow',
    grow: 2,
  },
  ...ALL.map<Column<Row>>((g) => ({
    id: g,
    header: g,
    cell: (r) => {
      const s = r.guardrails.find((x) => x.guardrail === g);
      return s ? guardrailWords(s) : '◷ unknown';
    },
    kind: 'grow',
  })),
  {
    id: 'source',
    header: 'Source',
    cell: (r) => {
      const s = r.guardrails[0];
      return s?.source === 'guard_report' && s.reported_at
        ? `guard report of ${formatShortDateTime(s.reported_at)}`
        : 'crsp check';
    },
    secondary: true,
    kind: 'grow',
  },
  {
    id: 'known',
    header: 'Known list, 30 days',
    cell: (r) =>
      r.known_count_by_day?.length ? (
        <Sparkline
          label={`Known list of ${r.repository} over 30 days`}
          values={r.known_count_by_day.map((d) => d.value)}
          unit="count"
          markLast
        />
      ) : (
        'not known'
      ),
    secondary: true,
  },
];

export function Guardrails() {
  const repository = useRepositoryFilter();
  const { result, reload } = useGuardrails();
  return (
    <Load result={result} what="the guardrails" reload={reload}>
      {(page) => {
        const rows = page.items.filter((r) => !repository || r.repository === repository);
        return (
          <Stack>
            <Section
              title="Guardrails"
              count={rows.length}
              hint="G1 to G7 of the test guardrails, per project."
            >
              {rows.length === 0 ? (
                <Empty title="No project reports its guardrails yet." />
              ) : (
                <Table
                  caption="Guardrails by project"
                  columns={guardrailColumns}
                  rows={rows}
                  rowKey={(r) => String(r.repository_id)}
                  rowLabel={(r) => r.repository}
                  action={{
                    label: 'Open',
                    to: (r) => `/setup/pool/${r.repository}`,
                    name: (r) => r.repository,
                  }}
                />
              )}
            </Section>
          </Stack>
        );
      }}
    </Load>
  );
}
