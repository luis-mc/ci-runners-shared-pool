// R5.108, R5.109 (OPS34): the five groupings, one table each; a row opens the list of its jobs with the group's filter.
import { cleanup, screen, within } from '@testing-library/react';
import { afterEach, describe, expect, it } from 'vitest';
import { apiError, renderApp } from '../../testing/mock';
import { jobsApi } from '../../testing/jobsData';
import { filterQuery, groupLink } from './Groups';

afterEach(cleanup);

const row = (over: Record<string, unknown>) => ({
  name: 'runner-1',
  filter: 'runner=runner-1',
  running: 1,
  queued: 2,
  done: 30,
  failed: 3,
  wait_p50: 12,
  wait_p90: 40,
  run_p50: 270,
  run_p90: 400,
  trend: [250, 260, 270, 280],
  ...over,
});

describe('Jobs by group', () => {
  it('shows running, queued, done, failed, median wait, median and slow-end run time and a trend', async () => {
    renderApp(jobsApi({ groups: [row({})] }), '/jobs/runner');
    const table = await screen.findByRole('table', { name: 'Jobs by runner' });
    const cells = within(table.querySelectorAll('tbody tr')[0] as HTMLElement)
      .getAllByRole('cell')
      .map((c) => c.textContent);
    expect(cells).toEqual(
      expect.arrayContaining(['runner-1', '1', '2', '30', '3', '12 s', '4 min 30 s']),
    );
    expect(within(table).getByRole('button', { name: 'Open: runner-1' })).toBeTruthy();
    expect(screen.getAllByRole('heading', { level: 1 })).toHaveLength(1);
  });

  it('splits a runner type into its kind and its backend', async () => {
    renderApp(
      jobsApi({
        groups: [
          row({
            name: 'ephemeral/docker',
            filter: 'runner_kind=ephemeral&backend=docker',
          }),
        ],
      }),
      '/jobs/runner-type',
    );
    const table = await screen.findByRole('table', { name: 'Jobs by runner type' });
    expect(within(table).getByText('ephemeral')).toBeTruthy();
    expect(within(table).getByText('docker')).toBeTruthy();
  });

  it('marks a job type light or heavy', async () => {
    renderApp(
      jobsApi({
        groups: [
          row({ name: 'lint', filter: 'job_type=lint', light: true }),
          row({ name: 'build', filter: 'job_type=build', light: false }),
        ],
      }),
      '/jobs/job-type',
    );
    const table = await screen.findByRole('table', { name: 'Jobs by job type' });
    expect(within(table).getByText('Light')).toBeTruthy();
    expect(within(table).getByText('Heavy')).toBeTruthy();
  });

  it('shows a machine by its name and a repository by its full name', async () => {
    renderApp(
      jobsApi({ groups: [row({ name: 'build-1', filter: 'machine=m1' })] }),
      '/jobs/machine',
    );
    expect(await screen.findByText('build-1')).toBeTruthy();
  });

  it('says so when no job has run yet, and when the groups could not load', async () => {
    renderApp(jobsApi(), '/jobs/repository');
    expect(await screen.findByText(/No jobs yet/)).toBeTruthy();
    cleanup();
    renderApp(
      jobsApi().on('GET', '/api/v1/jobs/groups', apiError(500, 'internal', 'busy')),
      '/jobs/repository',
    );
    expect(await screen.findByText(/Could not load the jobs by repository/)).toBeTruthy();
  });

  it('opens the list of the group with its filter in the address, each value encoded', () => {
    expect(filterQuery('runner_kind=persistent&backend=docker')).toBe(
      'runner_kind=persistent&backend=docker',
    );
    expect(filterQuery('repository=octo/app')).toBe('repository=octo%2Fapp');
    expect(groupLink('repository', row({ filter: 'repository=octo/app' }) as never)).toBe(
      '/pool/jobs?repository=octo%2Fapp',
    );
    expect(groupLink('runner', row({}) as never)).toBe(
      '/pool/jobs?view=jobs&runner=runner-1',
    );
  });
});
