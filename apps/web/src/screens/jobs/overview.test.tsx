// Analytics > Insights (UX-75): four tiles with their change and the cards that link.
import { cleanup, screen, within } from '@testing-library/react';
import { afterEach, describe, expect, it } from 'vitest';
import { renderApp } from '../../testing/mock';
import { job, jobsApi } from '../../testing/jobsData';
import { judge, passChange, periods, waitChange } from './overviewStats';

afterEach(cleanup);

const day = (n: number, jobs: number, failed: number, wait: number) => ({
  day: `2026-11-${String(n).padStart(2, '0')}`,
  jobs,
  failed,
  wait_p50: wait / 2,
  wait_p90: wait,
  run_p50: 60,
  run_p90: 120,
});
const days = [
  ...[1, 2, 3, 4, 5, 6, 7].map((n) => day(n, 10, 1, 60)),
  ...[8, 9, 10, 11, 12, 13, 14].map((n) => day(n, 10, 3, 180)),
];

describe('the numbers', () => {
  it('compares the last 7 days with the 7 before', () => {
    const { last, prev } = periods(days);
    expect(last.jobs).toBe(70);
    expect(prev.failed).toBe(7);
    expect(waitChange(last, prev)).toEqual({
      change: 'worse',
      text: 'up 2 min vs the previous 7 days',
    });
    expect(passChange(last, prev)?.change).toBe('worse');
  });
  it('calls a change under ten per cent the same', () => {
    expect(judge(104, 100, true)).toBe('same');
    expect(judge(80, 100, true)).toBe('better');
  });
  it('says nothing without history', () => {
    const { last, prev } = periods([]);
    expect(waitChange(last, prev)).toBeUndefined();
  });
});

describe('the Overview', () => {
  it('shows four tiles that lead to a list, the cards', async () => {
    renderApp(
      jobsApi({
        stats: { days, hours: [], forecast_points: [] },
        jobs: [job({ id: 1, state: 'queued', result: undefined })],
        groups: [
          {
            name: 'octo/app',
            filter: 'repository=octo/app',
            running: 0,
            queued: 1,
            done: 9,
            failed: 1,
            wait_p50: 10,
            wait_p90: 20,
            run_p50: 30,
            run_p90: 60,
            trend: [],
          },
        ],
      }),
      '/analytics/insights',
    );
    const waiting = await screen.findByRole('link', { name: /^Waiting now 1 job/ });
    expect(waiting.getAttribute('href')).toBe('/pool/jobs?state=queued');
    expect(
      screen.getByRole('link', { name: /^Wait slow end \(p90\) 3 min up 2 min/ }),
    ).toBeTruthy();
    expect(screen.getByRole('link', { name: /^Jobs that passed 70%/ })).toBeTruthy();
    expect(screen.getByRole('link', { name: /^Pool busy/ })).toBeTruthy();
    const bars = await screen.findByRole('list', { name: 'Busiest repositories' });
    expect(
      within(bars).getByRole('link', { name: /Open the jobs of octo\/app/ }),
    ).toBeTruthy();
  });
});
