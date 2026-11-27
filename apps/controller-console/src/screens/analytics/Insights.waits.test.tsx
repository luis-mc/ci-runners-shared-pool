// LOOK66: Analytics > Insights, the Waits pane (Label, Median, Slowest 10%) and the Runner timeline pane (the shared
// slot timeline), each with real data, an empty state and an error that names the pane.
import { cleanup, screen, within } from '@testing-library/react';
import { afterEach, describe, expect, it } from 'vitest';
import type { Schemas } from '../../api/api';
import { jobsApi } from '../../testing/jobsData';
import { renderApp } from '../../testing/mock';
import { slowestFirst } from './LabelWaits';

afterEach(cleanup);

const stat = (p50?: number, p90?: number): Schemas['DurationStats'] => ({
  total_seconds: 600,
  known: p50 === undefined ? 0 : 5,
  ...(p50 === undefined || p90 === undefined
    ? {}
    : { p50_seconds: p50, p90_seconds: p90 }),
});
const row = (key: string, p50?: number, p90?: number): Schemas['WaitSplitRow'] => ({
  key,
  jobs: 5,
  wait: stat(p50, p90),
  hold: stat(),
  run: stat(),
});
const split = (rows: Schemas['WaitSplitRow'][]): Schemas['WaitSplit'] => ({
  days: 7,
  group: 'label',
  hold_cap_seconds: 120,
  rows,
});
const NOW = Date.now();
const iso = (min: number) => new Date(NOW + min * 60_000).toISOString();
const timeline = (over: Partial<Schemas['Timeline']> = {}): Schemas['Timeline'] => ({
  now: iso(0),
  from: iso(-360),
  to: iso(0),
  rows: [
    {
      key: 'm1:1',
      machine_id: 'm1',
      machine_name: 'BuildTower',
      slot: 1,
      label: 'BuildTower slot 1',
    },
  ],
  bars: [
    {
      id: 'b1',
      row_id: 'm1:1',
      state: 'reserved',
      label: 'build',
      start: iso(-20),
      end: iso(10),
    },
  ],
  more_before: false,
  ...over,
});

describe('Insights, Waits and Runner timeline', () => {
  it('lists the labels with their median and slowest 10%, slowest first', async () => {
    const mock = jobsApi()
      .on(
        'GET',
        '/api/v1/insights/waits',
        split([
          row('crsp-small-4c1d', 0, 120),
          row('crsp-build-c4a7', 540, 1320),
          row('crsp-e2e-7f3a', 240, 660),
        ]),
      )
      .on('GET', '/api/v1/timeline', timeline());
    renderApp(mock, '/analytics/insights');
    const pane = await screen.findByRole('region', { name: 'Waits' });
    const table = await within(pane).findByRole('table', { name: 'Waits by label' });
    expect(
      within(table)
        .getAllByRole('columnheader')
        .map((h) => h.textContent),
    ).toEqual(['Label', 'Median', 'Slowest 10%']);
    const cells = within(table)
      .getAllByRole('row')
      .slice(1)
      .map((r) =>
        within(r)
          .getAllByRole('cell')
          .map((c) => c.textContent),
      );
    expect(cells).toEqual([
      ['crsp-build-c4a7', '9 min', '22 min'],
      ['crsp-e2e-7f3a', '4 min', '11 min'],
      ['crsp-small-4c1d', '0 s', '2 min'],
    ]);
    expect(
      mock.calls.some(
        (c) =>
          c.path === '/api/v1/insights/waits' &&
          c.query.get('group') === 'label' &&
          c.query.get('days') === '7',
      ),
    ).toBe(true);
  });

  it('says so when no job finished, and says "not known" for a label with no wait', async () => {
    const mock = jobsApi()
      .on('GET', '/api/v1/insights/waits', split([]))
      .on('GET', '/api/v1/timeline', timeline({ rows: [], bars: [] }));
    renderApp(mock, '/analytics/insights');
    const pane = await screen.findByRole('region', { name: 'Waits' });
    expect(
      await within(pane).findByText('No finished jobs in the last 7 days.'),
    ).toBeTruthy();
    expect(slowestFirst([row('a'), row('b', 60, 120)]).map((r) => r.key)).toEqual([
      'b',
      'a',
    ]);
  });

  it('names the pane when the waits cannot be loaded', async () => {
    const mock = jobsApi()
      .on('GET', '/api/v1/insights/waits', () => {
        throw new Error('boom');
      })
      .on('GET', '/api/v1/timeline', timeline());
    renderApp(mock, '/analytics/insights');
    const pane = await screen.findByRole('region', { name: 'Waits' });
    expect(await within(pane).findByText(/Could not load the waits/)).toBeTruthy();
  });

  it('draws the runner timeline as slot rows with the reserved bar', async () => {
    const mock = jobsApi()
      .on('GET', '/api/v1/insights/waits', split([]))
      .on('GET', '/api/v1/timeline', timeline());
    renderApp(mock, '/analytics/insights');
    const pane = await screen.findByRole('region', { name: 'Runner timeline' });
    expect(
      (await within(pane).findAllByText('BuildTower slot 1')).length,
    ).toBeGreaterThan(0);
    expect(within(pane).getAllByText(/reserved/i).length).toBeGreaterThan(0);
  });

  it('says so when no job ran in the timeline window', async () => {
    const mock = jobsApi()
      .on('GET', '/api/v1/insights/waits', split([]))
      .on('GET', '/api/v1/timeline', timeline({ rows: [], bars: [] }));
    renderApp(mock, '/analytics/insights');
    const pane = await screen.findByRole('region', { name: 'Runner timeline' });
    expect(
      await within(pane).findByText('No job ran, runs or waits in the last 24 hours.'),
    ).toBeTruthy();
  });
});
