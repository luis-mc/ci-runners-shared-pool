// R5.113, R5.114, R5.115 (OPS34): the Job page: branch, task and worktree, steps as a waterfall (names as text,
// never markup), "steps are no longer kept", the forecast as a range with its basis, the queue place and the history.
import { cleanup, screen, within } from '@testing-library/react';
import { afterEach, describe, expect, it } from 'vitest';
import { apiError, renderApp } from '../../testing/mock';
import { job, jobDetail, jobsApi } from '../../testing/jobsData';

afterEach(cleanup);

describe('Job page', () => {
  it('shows branch, task and, only when reported, the worktree', async () => {
    renderApp(jobsApi({ detail: jobDetail() }), '/pool/jobs/12');
    expect(await screen.findByRole('heading', { level: 1, name: 'test' })).toBeTruthy();
    const section = screen.getByRole('region', { name: 'Project and code' });
    expect(within(section).getByText('OPS34-jobs-section')).toBeTruthy();
    expect(within(section).getByText('OPS34')).toBeTruthy();
    expect(within(section).getByText('not reported')).toBeTruthy();
    expect(within(section).getByText('a1b2c3d')).toBeTruthy();
  });

  it('shows the worktree the job reported', async () => {
    renderApp(jobsApi({ detail: jobDetail({ worktree: 'wt-ops34' }) }), '/pool/jobs/12');
    expect(await screen.findByText('wt-ops34')).toBeTruthy();
  });

  it('shows no task when the branch has none', async () => {
    renderApp(
      jobsApi({ detail: jobDetail({ branch: 'main', task_id: undefined }) }),
      '/pool/jobs/12',
    );
    expect(await screen.findByText('none in the branch name')).toBeTruthy();
  });

  it('draws the steps as a waterfall and shows a step name as text, never as markup', async () => {
    renderApp(jobsApi({ detail: jobDetail() }), '/pool/jobs/12');
    const steps = await screen.findByRole('region', { name: 'Steps' });
    expect(within(steps).getAllByText(/2\. <b>Run tests<\/b>/).length).toBeGreaterThan(0);
    expect(steps.querySelector('b')).toBeNull();
    expect(within(steps).getByText(/Step times are from GitHub/)).toBeTruthy();
  });

  it('says a job with no stored steps reported none', async () => {
    renderApp(jobsApi({ detail: jobDetail({ steps: [] }) }), '/pool/jobs/12');
    const steps = await screen.findByRole('region', { name: 'Steps' });
    expect(within(steps).getByText('This job reported no steps.')).toBeTruthy();
  });

  it('says steps are no longer kept for a job older than 30 days', async () => {
    renderApp(
      jobsApi({
        detail: jobDetail({ steps: [], completed_at: '2026-09-01T09:05:00Z' }),
      }),
      '/pool/jobs/12',
    );
    const steps = await screen.findByRole('region', { name: 'Steps' });
    expect(
      within(steps).getByText('Steps are no longer kept for a job this old.'),
    ).toBeTruthy();
  });

  it('shows the forecast as a range with its basis, and where the time landed', async () => {
    renderApp(jobsApi({ detail: jobDetail() }), '/pool/jobs/12');
    const forecast = await screen.findByRole('region', { name: 'Forecast' });
    expect(
      within(forecast).getByText(
        'about 4 to 6 min, median of the last 20 runs on all machines',
      ),
    ).toBeTruthy();
    expect(within(forecast).getByText('4 min 30 s, inside the range')).toBeTruthy();
  });

  it('says there is not enough history yet and names the workflow time as a stand-in', async () => {
    renderApp(
      jobsApi({
        detail: jobDetail({
          forecast: { p50: 240, lo: 200, hi: 300, basis: 'workflow', samples: 3 },
        }),
      }),
      '/pool/jobs/12',
    );
    const forecast = await screen.findByRole('region', { name: 'Forecast' });
    expect(
      within(forecast).getByText(
        /not enough history yet; the workflow's typical time is about 3 to 5 min/,
      ),
    ).toBeTruthy();
  });

  it('makes up no number for a job nothing is known about', async () => {
    renderApp(
      jobsApi({
        detail: jobDetail({
          forecast: { p50: 0, lo: 0, hi: 0, basis: 'not_enough_history', samples: 1 },
        }),
      }),
      '/pool/jobs/12',
    );
    const forecast = await screen.findByRole('region', { name: 'Forecast' });
    expect(
      within(forecast).getByText(/^not enough history yet, 1 of 5 runs needed/),
    ).toBeTruthy();
  });

  it('shows a queued job with its place, why it waits and a start range', async () => {
    renderApp(
      jobsApi({
        detail: jobDetail({
          state: 'queued',
          result: undefined,
          steps: [],
          queue_position: 3,
          wait_reason: 'free_slot',
          wait_text: 'waits for a free slot on build-1',
          queue_eta: { p50: 300, lo: 240, hi: 420 },
          queued_at: new Date(Date.now() - 60_000).toISOString(),
          runner: undefined,
        }),
      }),
      '/pool/jobs/12',
    );
    const queue = await screen.findByRole('region', { name: 'Queue' });
    expect(within(queue).getByText('number 3')).toBeTruthy();
    expect(within(queue).getByText('waits for a free slot on build-1')).toBeTruthy();
    expect(
      within(queue).getByText(/in about 4 to 7 min \(a forecast, not a promise\)/),
    ).toBeTruthy();
  });

  it('shows the runner, the history of the job and the link to GitHub', async () => {
    renderApp(jobsApi({ detail: jobDetail() }), '/pool/jobs/12');
    const runner = await screen.findByRole('region', { name: 'Runner' });
    expect(within(runner).getByText('runner-1')).toBeTruthy();
    expect(within(runner).getByText('build-1')).toBeTruthy();
    expect(screen.getByRole('region', { name: 'History of this job' })).toBeTruthy();
    expect(screen.getByRole('button', { name: 'Open the run on GitHub' })).toBeTruthy();
  });

  it('breadcrumbs back to the run', async () => {
    renderApp(
      jobsApi({ detail: jobDetail({ ...job({ run_id: 100 }) }) }),
      '/pool/jobs/12',
    );
    await screen.findByRole('heading', { level: 1, name: 'test' });
    const crumbs = screen.getByRole('navigation', { name: 'You are here' });
    expect(within(crumbs).getByText('Run #100, attempt 1')).toBeTruthy();
  });

  it('says it could not load the job', async () => {
    const api = jobsApi().on(
      'GET',
      '/api/v1/jobs/:jobId',
      apiError(404, 'not_found', 'No such job.'),
    );
    renderApp(api, '/pool/jobs/99');
    expect(await screen.findByText(/Could not load the job/)).toBeTruthy();
  });
});
