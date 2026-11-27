// Run insights, Tests (ideas 7, 8): Slowest, Flaky, Duplicated and No assertions, one table each. A test name is
// shown as text (UX-3).
import {
  Empty,
  Section,
  Segmented,
  Stack,
  Table,
  formatShortDateTime,
  type Column,
} from '@crsp/ui';
import { useState } from 'react';
import { Load } from '../common';
import { useTests } from './api';
import { useRepositoryFilter } from './InsightsLayout';
import type { Schemas } from '../../api/api';

type Test = Schemas['TestInsight'];
const VIEWS = [
  { value: 'slowest', label: 'Slowest' },
  { value: 'flaky', label: 'Flaky' },
  { value: 'duplicated', label: 'Duplicated' },
  { value: 'no_assertions', label: 'No assertions' },
] as const;
type View = (typeof VIEWS)[number]['value'];

const sec = (ms: number | undefined): string =>
  ms === undefined ? 'not known' : `${(ms / 1000).toFixed(1)} s`;

const columns: Column<Test>[] = [
  {
    id: 'name',
    header: 'Test',
    cell: (t) => t.name,
    sort: (t) => t.name,
    kind: 'grow',
    grow: 3,
  },
  {
    id: 'project',
    header: 'Project',
    cell: (t) => t.repository,
    sort: (t) => t.repository,
    kind: 'grow',
  },
  {
    id: 'job',
    header: 'Job',
    cell: (t) => (t.job_idents ?? []).join(', ') || 'not known',
    secondary: true,
    kind: 'grow',
  },
  { id: 'runs', header: 'Runs', cell: (t) => String(t.runs), sort: (t) => t.runs },
  {
    id: 'p50',
    header: 'Middle time',
    cell: (t) => sec(t.p50_ms),
    sort: (t) => t.p50_ms ?? 0,
    secondary: true,
  },
  {
    id: 'p95',
    header: 'Slow-end time',
    cell: (t) =>
      t.over_limit_seconds
        ? `${sec(t.p95_ms)}, over the ${t.kind ?? 'test'} limit of ${t.over_limit_seconds} s`
        : sec(t.p95_ms),
    sort: (t) => t.p95_ms ?? 0,
    kind: 'grow',
  },
  {
    id: 'fail',
    header: 'Failures',
    cell: (t) => String(t.failures ?? 0),
    sort: (t) => t.failures ?? 0,
    secondary: true,
  },
  {
    id: 'seen',
    header: 'Last seen',
    cell: (t) => (t.last_seen ? formatShortDateTime(t.last_seen) : 'not known'),
    secondary: true,
  },
];

export function Tests() {
  const [view, setView] = useState<View>('slowest');
  const repository = useRepositoryFilter();
  const { result, reload } = useTests(view);
  return (
    <Stack>
      <Segmented
        label="Kind of test finding"
        options={VIEWS}
        value={view}
        allLabel={null}
        onChange={(v) => setView(VIEWS.find((x) => x.value === v)?.value ?? 'slowest')}
      />
      <Load result={result} what="the tests" reload={reload}>
        {(page) => {
          const rows = page.items.filter(
            (t) => !repository || t.repository === repository,
          );
          return (
            <Section
              title={VIEWS.find((v) => v.value === view)?.label ?? 'Tests'}
              count={rows.length}
            >
              {!page.reports_found ? (
                <Empty title="No test reports found. crsp reads JUnit, Playwright, vitest, go test, nextest and TRX reports your CI uploads." />
              ) : rows.length === 0 ? (
                <Empty title="No test matches this view." />
              ) : (
                <Table
                  caption="Tests"
                  columns={columns}
                  rows={rows}
                  rowKey={(t) => t.test_id}
                />
              )}
            </Section>
          );
        }}
      </Load>
    </Stack>
  );
}
