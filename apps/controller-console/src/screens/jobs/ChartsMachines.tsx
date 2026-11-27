// Analytics > Charts, the two charts above the rest (LOOK65): the CPU of each machine today, and how a reservation frees
// room for a big job. Both are drawn by the shared trend chart; this file passes data only (UX-41).
import { Empty, Section, Stack, Text, TrendChart } from '@crsp/ui';
import { unwrap, useClient } from '../../api/api';
import { useLoad } from '../../api/hooks';
import { useMachines } from '../../api/machines';
import { Load } from '../common';

export const CPU_WORDS = {
  title: (name: string) => `${name}: CPU today`,
  series: 'CPU in use',
  empty: 'No load measured on this machine today.',
  none: 'No machine yet, so there is no CPU to draw.',
  hint: 'Percent of the CPU the machine gives the pool, measured every 15 seconds since midnight.',
} as const;

const startOfToday = (): Date => {
  const d = new Date();
  d.setHours(0, 0, 0, 0);
  return d;
};

function MachineCpu({ id, name }: { id: string; name: string }) {
  const client = useClient();
  const { result, reload } = useLoad(
    () =>
      unwrap(
        client.GET('/api/v1/machines/{machineId}/load', {
          params: {
            path: { machineId: id },
            query: { from: startOfToday().toISOString(), to: new Date().toISOString() },
          },
        }),
      ),
    [client, id],
  );
  return (
    <Section title={CPU_WORDS.title(name)}>
      <Load result={result} what="the CPU of this machine" reload={reload}>
        {(h) => (
          <TrendChart
            label={CPU_WORDS.title(name)}
            unit="percent"
            emptyText={CPU_WORDS.empty}
            series={[
              {
                id: 'cpu',
                label: CPU_WORDS.series,
                points: h.items
                  .filter((p) => p.cpu_pct !== undefined)
                  .map((p) => ({ x: Date.parse(p.at), y: p.cpu_pct as number })),
              },
            ]}
          />
        )}
      </Load>
    </Section>
  );
}

/** One pane per machine: its CPU since midnight. */
export function MachineCpuToday() {
  const { result, reload } = useMachines();
  return (
    <Load result={result} what="the machines" reload={reload}>
      {(list) =>
        list.items.length === 0 ? (
          <Section title="CPU today">
            <Empty title={CPU_WORDS.none} />
          </Section>
        ) : (
          <Stack>
            <Text tone="muted">{CPU_WORDS.hint}</Text>
            {list.items.map((m) => (
              <MachineCpu key={m.id} id={m.id} name={m.name || m.id} />
            ))}
          </Stack>
        )
      }
    </Load>
  );
}

export const RESERVATION_WORDS = {
  title: 'Free CPU, with and without a reservation',
  hint: 'A worked example, not your pool: a job that needs 8 CPU, on a machine where small jobs keep taking the CPU that frees up.',
  without: 'Without a reservation',
  withRes: 'With a reservation',
  free: 'Free CPU',
  needs: 'What the big job needs',
  empty: 'No example to draw.',
  minute: (n: number) => `minute ${n}`,
} as const;

/** The worked example: free CPU by minute, the 8 CPU the job needs, and the reservation from minute 10. */
const FREE = [5, 3, 2, 4, 6, 3, 2, 5, 3, 2, 4, 3, 2, 4, 3, 2, 5, 3, 2, 4, 3];
const NEED = 8;
const RESERVED_FROM = 10;
const STARTS_AT = 12;
export const exampleWithout = (): number[] => FREE.slice();
export const exampleWith = (): number[] =>
  FREE.map((v, minute) => {
    if (minute < RESERVED_FROM) return v;
    if (minute < STARTS_AT) return Math.min(NEED, v + (minute - RESERVED_FROM + 1) * 2);
    if (minute === STARTS_AT) return NEED;
    return 0; // the big job runs and holds the CPU
  });

const series = (free: number[], id: string, label: string) => [
  {
    id,
    label,
    points: free.map((y, minute) => ({ x: RESERVATION_WORDS.minute(minute), y })),
  },
  {
    id: `${id}-need`,
    label: RESERVATION_WORDS.needs,
    points: free.map((_, minute) => ({ x: RESERVATION_WORDS.minute(minute), y: NEED })),
  },
];

/** The two charts of the worked example, without a pane: Pool > Queue Analysis puts them in its Reservation pane (LOOK70). */
export function ReservationCharts() {
  return (
    <Stack>
      <TrendChart
        label={RESERVATION_WORDS.without}
        unit="count"
        emptyText={RESERVATION_WORDS.empty}
        series={series(exampleWithout(), 'without', RESERVATION_WORDS.free)}
      />
      <TrendChart
        label={RESERVATION_WORDS.withRes}
        unit="count"
        emptyText={RESERVATION_WORDS.empty}
        series={series(exampleWith(), 'with', RESERVATION_WORDS.free)}
      />
    </Stack>
  );
}

export function ReservationExample() {
  return (
    <Section title={RESERVATION_WORDS.title} hint={RESERVATION_WORDS.hint}>
      <ReservationCharts />
    </Section>
  );
}
