// The numbers of the Jobs Overview (UX-75): each tile compares the last 7 days with the 7 days before.
import { formatDuration, type StatChange } from '@crsp/ui';
import type { Schemas } from '../../api/api';

type Day = Schemas['DayStat'];

export interface Period {
  jobs: number;
  failed: number;
  /** The mean of the daily slow-end waits of the days that had jobs, in seconds; undefined with no jobs. */
  waitP90: number | undefined;
  /** One value for each of the days (oldest first). */
  waitSeries: number[];
  passSeries: number[];
}

const sum = (xs: readonly number[]) => xs.reduce((a, b) => a + b, 0);

function period(days: readonly Day[]): Period {
  const busy = days.filter((d) => d.jobs > 0);
  return {
    jobs: sum(days.map((d) => d.jobs)),
    failed: sum(days.map((d) => d.failed)),
    waitP90: busy.length ? sum(busy.map((d) => d.wait_p90)) / busy.length : undefined,
    waitSeries: days.map((d) => d.wait_p90),
    passSeries: days.map((d) => (d.jobs ? ((d.jobs - d.failed) / d.jobs) * 100 : 100)),
  };
}

/** The last `span` days and the `span` before them, from the daily stats in any order. */
export function periods(days: readonly Day[], span = 7): { last: Period; prev: Period } {
  const sorted = [...days].sort((a, b) => (a.day < b.day ? -1 : 1));
  const last = sorted.slice(-span);
  const prev = sorted.slice(-span * 2, -span);
  return { last: period(last), prev: period(prev) };
}

export const passRate = (p: Pick<Period, 'jobs' | 'failed'>): number | undefined =>
  p.jobs ? ((p.jobs - p.failed) / p.jobs) * 100 : undefined;

/** Whether a number that should go down went down: a change under 10 per cent is the same (UX-58, no Problem tone). */
export function judge(now: number, before: number, lowerIsBetter: boolean): StatChange {
  if (before === 0 || Math.abs(now - before) / before < 0.1) return 'same';
  return now < before === lowerIsBetter ? 'better' : 'worse';
}

const AGAINST = 'vs the previous 7 days';

export function waitChange(
  last: Period,
  prev: Period,
): { change: StatChange; text: string } | undefined {
  if (last.waitP90 === undefined || prev.waitP90 === undefined) return undefined;
  const diff = Math.round(last.waitP90 - prev.waitP90);
  const change = judge(last.waitP90, prev.waitP90, true);
  if (change === 'same') return { change, text: `about the same ${AGAINST}` };
  return {
    change,
    text: `${diff > 0 ? 'up' : 'down'} ${formatDuration(Math.abs(diff))} ${AGAINST}`,
  };
}

export function passChange(
  last: Period,
  prev: Period,
): { change: StatChange; text: string } | undefined {
  const a = passRate(last);
  const b = passRate(prev);
  if (a === undefined || b === undefined) return undefined;
  const diff = Math.round(a - b);
  if (Math.abs(diff) < 1) return { change: 'same', text: `about the same ${AGAINST}` };
  return {
    change: diff > 0 ? 'better' : 'worse',
    text: `${diff > 0 ? 'up' : 'down'} ${Math.abs(diff)} points ${AGAINST}`,
  };
}
