// R5.110 (OPS34; UX-33, UX-41): Charts: 14 days first with a switch to 7 or 30, each chart with a text alternative,
// the share of jobs inside their forecast, the busiest hours, the daily summaries export, and the retention words.
import { cleanup, fireEvent, screen, waitFor } from '@testing-library/react';
import { afterEach, describe, expect, it } from 'vitest';
import { apiError, renderApp } from '../../testing/mock';
import { jobsApi } from '../../testing/jobsData';
import { exampleWith, exampleWithout } from './ChartsMachines';

afterEach(cleanup);

const stats = {
  days: [
    {
      day: '2026-11-17',
      jobs: 10,
      failed: 1,
      wait_p50: 10,
      wait_p90: 30,
      run_p50: 200,
      run_p90: 300,
    },
    {
      day: '2026-11-18',
      jobs: 12,
      failed: 0,
      wait_p50: 12,
      wait_p90: 20,
      run_p50: 210,
      run_p90: 280,
    },
  ],
  hours: [{ weekday: 2, hour: 9, count: 5 }],
  forecast_points: [
    {
      job_id: 1,
      forecast_seconds: 200,
      actual_seconds: 210,
      lo: 180,
      hi: 240,
      basis: 'job_key',
    },
    {
      job_id: 2,
      forecast_seconds: 200,
      actual_seconds: 400,
      lo: 180,
      hi: 240,
      basis: 'job_key',
    },
  ],
};

describe('Charts, LOOK65 first two charts', () => {
  it('shows the CPU of each machine and the reservation example above the run charts', async () => {
    renderApp(jobsApi({ stats }), '/analytics/charts');
    await screen.findByRole('region', { name: 'Run time and wait per day' });
    const example = await screen.findByRole('region', {
      name: 'Free CPU, with and without a reservation',
    });
    const names = screen
      .getAllByRole('region')
      .map((r) => r.getAttribute('aria-label') ?? '');
    expect(names.indexOf('Free CPU, with and without a reservation')).toBeLessThan(
      names.indexOf('Run time and wait per day'),
    );
    expect(example.textContent).toContain('Without a reservation');
    expect(example.textContent).toContain('With a reservation');
    expect(await screen.findByRole('region', { name: /: CPU today$/ })).toBeTruthy();
  });

  it('the example never reaches 8 CPU without a reservation and does with one, then the job holds the CPU', () => {
    expect(Math.max(...exampleWithout())).toBeLessThan(8);
    const withRes = exampleWith();
    expect(withRes[12]).toBe(8);
    expect(withRes.slice(13).every((v) => v === 0)).toBe(true);
  });
});

describe('Charts', () => {
  it('asks for 14 days first and switches to 7 and 30', async () => {
    const api = jobsApi({ stats });
    renderApp(api, '/analytics/charts');
    await screen.findByRole('region', { name: 'Busiest hours' });
    expect(
      api.calls.find((c) => c.path === '/api/v1/jobs/stats')?.query.get('days'),
    ).toBe('14');
    fireEvent.click(screen.getByRole('button', { name: '7 days' }));
    await waitFor(() =>
      expect(
        api.calls
          .filter((c) => c.path === '/api/v1/jobs/stats')
          .at(-1)
          ?.query.get('days'),
      ).toBe('7'),
    );
    fireEvent.click(screen.getByRole('button', { name: '30 days' }));
    await waitFor(() =>
      expect(
        api.calls
          .filter((c) => c.path === '/api/v1/jobs/stats')
          .at(-1)
          ?.query.get('days'),
      ).toBe('30'),
    );
  });

  it('says how many jobs ran inside their forecast, and gives every chart a table view', async () => {
    renderApp(jobsApi({ stats }), '/analytics/charts');
    expect(
      await screen.findByText(/50% of 2 finished jobs ran inside their forecast range/),
    ).toBeTruthy();
    expect(screen.getAllByText(/Show as table/i).length).toBeGreaterThan(0);
    expect(screen.getAllByRole('heading', { level: 1 })).toHaveLength(1);
    expect(screen.getByText(/crsp keeps steps for 30 days/)).toBeTruthy();
  });

  it('says there is nothing to draw with no finished jobs', async () => {
    renderApp(jobsApi(), '/analytics/charts');
    expect(await screen.findByText(/No finished jobs in this range/)).toBeTruthy();
  });

  it('exports the daily summaries of the range from the controller', async () => {
    const api = jobsApi({ stats });
    renderApp(api, '/analytics/charts');
    await screen.findByRole('region', { name: 'Busiest hours' });
    URL.createObjectURL = () => 'blob:x';
    URL.revokeObjectURL = () => undefined;
    fireEvent.click(screen.getByRole('button', { name: 'Export daily summaries (CSV)' }));
    await waitFor(() =>
      expect(api.count('GET', '/api/v1/exports/job-summaries')).toBe(1),
    );
    expect(
      api.calls
        .find((c) => c.path === '/api/v1/exports/job-summaries')
        ?.query.get('from'),
    ).toBeTruthy();
  });

  it('says it could not load the charts', async () => {
    renderApp(
      jobsApi().on('GET', '/api/v1/jobs/stats', apiError(500, 'internal', 'busy')),
      '/analytics/charts',
    );
    expect(await screen.findByText(/Could not load the charts/)).toBeTruthy();
  });
});
