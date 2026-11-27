// CRSP256: minutes and money saved on screen (observability R5.15 to R5.19, R5.34): the Overview tiles, the Analytics
// Savings report with its ranges, the Compare view, and the editable rates in Settings.
import { cleanup, fireEvent, screen, waitFor, within } from '@testing-library/react';
import { afterEach, describe, expect, it } from 'vitest';
import type { Schemas } from '../../api/api';
import { jobsApi } from '../../testing/jobsData';
import { renderApp } from '../../testing/mock';

afterEach(cleanup);

const row = (
  key: string,
  minutes: number,
  money: number,
  pub = 0,
): Schemas['SavingsRow'] => ({
  key,
  jobs: 2,
  minutes,
  money,
  public_minutes: pub,
});

const report = (
  over: Partial<Schemas['SavingsReport']> = {},
): Schemas['SavingsReport'] => ({
  range: 'today',
  from: '2026-11-25T00:00:00Z',
  to: '2026-11-25T15:00:00Z',
  total: row('all', 15, 0.12, 5),
  by_repository: [row('o/app', 10, 0.12), row('o/public', 5, 0, 5)],
  by_os: [row('linux', 15, 0.12)],
  by_machine: [row('box', 15, 0.12)],
  not_priced: 0,
  note: 'Estimate. Jobs of public repositories count their minutes but save no money: GitHub-hosted standard runners are free there.',
  source_url: 'https://docs.github.com/en/billing/reference/actions-runner-pricing',
  copied_on: '2026-11-15',
  rates_changed: false,
  ...over,
});

const rates = (over: Partial<Schemas['SavingsRates']> = {}): Schemas['SavingsRates'] => ({
  source_url: 'https://docs.github.com/en/billing/reference/actions-runner-pricing',
  copied_on: '2026-11-15',
  changed: false,
  rates: [
    { os: 'linux', size: 'arm', per_minute: 0.005 },
    { os: 'linux', size: 'standard', per_minute: 0.008 },
  ],
  machine_sizes: {},
  ...over,
});

describe('minutes and money saved', () => {
  it('Overview shows the minutes tile and the money tile with its estimate and rate date', async () => {
    const mock = jobsApi().on('GET', '/api/v1/savings', report());
    renderApp(mock, '/');
    await waitFor(() =>
      expect(
        screen.getByRole('link', { name: /Minutes saved today/ }).textContent,
      ).toContain('15'),
    );
    await waitFor(() => {
      const money =
        screen.getByRole('link', { name: /Money saved today/ }).textContent ?? '';
      expect(money).toContain('$0.120');
      expect(money).toContain('estimate');
      expect(money).toContain('2026-11-15');
    });
  });

  it('Analytics > Savings lists repository, system and machine, and says public repositories save no money', async () => {
    const mock = jobsApi().on('GET', '/api/v1/savings', report());
    renderApp(mock, '/analytics/savings');
    await screen.findByRole('table', { name: 'By repository' });
    const repoRows = within(
      screen.getByRole('table', { name: 'By repository' }),
    ).getAllByRole('row');
    expect(repoRows.map((r) => r.textContent)).toEqual(
      expect.arrayContaining([expect.stringContaining('o/public')]),
    );
    expect(screen.getByRole('table', { name: 'By system' })).toBeTruthy();
    expect(screen.getByRole('table', { name: 'By machine' })).toBeTruthy();
    expect(
      screen.getByText(/Public repositories count their minutes but save no money/),
    ).toBeTruthy();
    expect(
      screen.getByText(/Rates copied from https:\/\/docs\.github\.com/),
    ).toBeTruthy();
  });

  it('asks for 7 days, 30 days and a range', async () => {
    const mock = jobsApi().on('GET', '/api/v1/savings', report());
    renderApp(mock, '/analytics/savings');
    await screen.findByRole('table', { name: 'By repository' });
    fireEvent.click(screen.getByRole('button', { name: '7 days' }));
    await waitFor(() =>
      expect(
        mock.calls.some(
          (c) => c.path === '/api/v1/savings' && c.query.get('range') === '7d',
        ),
      ).toBe(true),
    );
    fireEvent.click(screen.getByRole('button', { name: '30 days' }));
    await waitFor(() =>
      expect(
        mock.calls.some(
          (c) => c.path === '/api/v1/savings' && c.query.get('range') === '30d',
        ),
      ).toBe(true),
    );
    fireEvent.click(screen.getByRole('button', { name: 'A range' }));
    await waitFor(() =>
      expect(
        mock.calls.some(
          (c) =>
            c.path === '/api/v1/savings' &&
            c.query.get('range') === 'custom' &&
            Boolean(c.query.get('from')) &&
            Boolean(c.query.get('to')),
        ),
      ).toBe(true),
    );
  });

  it('says so when no job ran on the pool', async () => {
    const mock = jobsApi().on(
      'GET',
      '/api/v1/savings',
      report({
        total: { ...row('all', 0, 0), jobs: 0 },
        by_repository: [],
        by_os: [],
        by_machine: [],
      }),
    );
    renderApp(mock, '/analytics/savings');
    expect(await screen.findByText('No job ran on the pool in this range.')).toBeTruthy();
  });

  it('Compare reads the repository and shows the two sides, never a number the data lacks', async () => {
    const side = (n: number, med: number) => ({
      queue: { count: n, median_seconds: 1, p95_seconds: 2 },
      run: { count: n, median_seconds: med, p95_seconds: med },
    });
    const mock = jobsApi().on('GET', '/api/v1/compare', {
      repository: 'o/app',
      days: 14,
      since: '2026-11-11T00:00:00Z',
      excluded: false,
      rows: [
        {
          job: 'build',
          crsp: side(3, 120),
          hosted: side(3, 300),
          minutes_saved: 3,
          cost_avoided_usd: 0.04,
        },
      ],
      unmatched_jobs: 0,
      total_minutes_saved: 3,
      total_cost_avoided_usd: 0.04,
    });
    renderApp(mock, '/analytics/compare');
    const compare = await screen.findByRole('button', { name: 'Compare' });
    expect(
      compare.hasAttribute('disabled') ||
        compare.getAttribute('aria-disabled') === 'true',
    ).toBe(true);
    fireEvent.change(screen.getByLabelText('Repository'), { target: { value: 'o/app' } });
    fireEvent.click(screen.getByRole('button', { name: 'Compare' }));
    const table = await screen.findByRole('table', {
      name: 'crsp against GitHub-hosted runners',
    });
    expect(within(table).getByText('build')).toBeTruthy();
    expect(
      mock.calls.some(
        (c) => c.path === '/api/v1/compare' && c.query.get('repository') === 'o/app',
      ),
    ).toBe(true);
  });

  it('Settings shows the rate table with its source and date and a reset that is off while the rates are shipped', async () => {
    const mock = jobsApi().on('GET', '/api/v1/savings/rates', rates());
    renderApp(mock, '/settings/repositories');
    expect(
      await screen.findByText(
        /Rates copied from https:\/\/docs\.github\.com.*2026-11-15/,
      ),
    ).toBeTruthy();
    expect(screen.getByText('linux standard, dollars per minute')).toBeTruthy();
    const reset = screen.getByRole('button', { name: 'Reset to shipped rates' });
    expect(
      reset.hasAttribute('disabled') || reset.getAttribute('aria-disabled') === 'true',
    ).toBe(true);
  });
});
