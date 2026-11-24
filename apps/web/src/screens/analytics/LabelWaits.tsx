// Analytics > Insights, the Waits pane (LOOK66): how long jobs waited for a runner, by runner label, over the last 7
// days. Median and slowest 10% come from GET /api/v1/insights/waits (group label); a value the data does not give is
// said so, never shown as 0 (UX-17).
import { Empty, Table, formatDuration, type Column } from '@crsp/ui';
import { unwrap, useClient, type Schemas } from '../../api/api';
import { useLoad } from '../../api/hooks';
import { Load } from '../common';

type Row = Schemas['WaitSplitRow'];

const words = (seconds: number | undefined): string =>
  seconds === undefined ? 'not known' : formatDuration(seconds);

const columns: Column<Row>[] = [
  { id: 'label', header: 'Label', cell: (r) => r.key, sort: (r) => r.key, kind: 'grow' },
  { id: 'median', header: 'Median', cell: (r) => words(r.wait.p50_seconds) },
  { id: 'slow', header: 'Slowest 10%', cell: (r) => words(r.wait.p90_seconds) },
];

/** The rows of a split, the longest slow end first; a label with no known wait goes last. */
export const slowestFirst = (rows: readonly Row[]): Row[] =>
  [...rows].sort(
    (a, b) =>
      (b.wait.p90_seconds ?? -1) - (a.wait.p90_seconds ?? -1) ||
      a.key.localeCompare(b.key),
  );

export function LabelWaits() {
  const client = useClient();
  const waits = useLoad(
    () =>
      unwrap(
        client.GET('/api/v1/insights/waits', {
          params: { query: { days: 7, group: 'label' } },
        }),
      ),
    [client],
  );
  return (
    <Load result={waits.result} what="the waits" reload={waits.reload}>
      {(d) =>
        d.rows.length === 0 ? (
          <Empty title="No finished jobs in the last 7 days." />
        ) : (
          <Table
            caption="Waits by label"
            columns={columns}
            rows={slowestFirst(d.rows)}
            rowKey={(r) => r.key}
          />
        )
      }
    </Load>
  );
}
