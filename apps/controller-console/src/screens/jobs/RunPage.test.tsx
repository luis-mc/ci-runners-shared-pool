// R5.106, R5.112 (OPS34): the Run page: the run's facts, its jobs as a waterfall and as a graph drawn from the
// `needs` of the workflow file, "expands at run time" for a matrix, the timing fallback with its note, attempts.
import { cleanup, fireEvent, screen, waitFor, within } from '@testing-library/react';
import { afterEach, describe, expect, it } from 'vitest';
import { apiError, renderApp } from '../../testing/mock';
import { jobsApi, runItem } from '../../testing/jobsData';

afterEach(cleanup);

describe('Run page', () => {
  it('shows the facts, the breadcrumb and the one h1', async () => {
    renderApp(jobsApi({ run: runItem() }), '/pool/runs/100?attempt=1');
    expect(await screen.findByText('ci.yml on OPS34-jobs-section')).toBeTruthy();
    expect(screen.getAllByRole('heading', { level: 1 })).toHaveLength(1);
    const crumbs = screen.getByRole('navigation', { name: 'You are here' });
    expect(within(crumbs).getByText('octo/app')).toBeTruthy();
    expect(within(crumbs).getByText('Run #100, attempt 1')).toBeTruthy();
    expect(screen.getByText('OPS34')).toBeTruthy(); // the task id the controller read from the branch
    expect(screen.getByText('not reported')).toBeTruthy(); // the worktree is never guessed
  });

  it('asks for the attempt named in the address', async () => {
    const api = jobsApi({ run: runItem({ attempt: 2, attempts: [1, 2] }) });
    renderApp(api, '/pool/runs/100?attempt=2');
    await screen.findByText('ci.yml on OPS34-jobs-section');
    const call = api.calls.find((c) => c.path === '/api/v1/runs/100');
    expect(call?.query.get('attempt')).toBe('2');
    expect(screen.getByRole('tab', { name: 'Attempt 2' })).toBeTruthy();
  });

  it('draws the jobs as a graph from the workflow file and marks a matrix as expanding at run time', async () => {
    renderApp(jobsApi({ run: runItem() }), '/pool/runs/100');
    await screen.findByText('ci.yml on OPS34-jobs-section');
    expect(
      screen.getByText(/Arrows come from the workflow file at commit a1b2c3d/),
    ).toBeTruthy();
    expect(screen.getAllByText(/expands at run time/).length).toBeGreaterThan(0);
    expect(screen.getAllByText('lint').length).toBeGreaterThan(0);
  });

  it('falls back to the overlap in time and says it is a guess', async () => {
    renderApp(
      jobsApi({
        run: runItem({
          edges: [],
          critical_path: [],
          graph: { source: 'timing_overlap', dynamic_nodes: [] },
        }),
      }),
      '/pool/runs/100',
    );
    expect(
      await screen.findByText(/could not be read, so no arrows are drawn/),
    ).toBeTruthy();
    expect(screen.getByText(/a guess from timing, not from the workflow/)).toBeTruthy();
  });

  it('shows the end of an open run as a range with its basis', async () => {
    renderApp(
      jobsApi({
        run: runItem({
          state: 'running',
          result: undefined,
          completed_at: undefined,
          eta: { p50: 420, lo: 360, hi: 540 },
        }),
      }),
      '/pool/runs/100',
    );
    expect(
      await screen.findByText(/in about 6 to 9 min, along the longest chain of 2 jobs/),
    ).toBeTruthy();
  });

  it('opens a job of the run on the Job page', async () => {
    renderApp(jobsApi({ run: runItem() }), '/pool/runs/100');
    await screen.findByText('ci.yml on OPS34-jobs-section');
    const timeline = screen.getByRole('region', { name: 'Timeline' });
    fireEvent.click(
      within(timeline).getAllByRole('button', { name: /lint/ })[0] as HTMLElement,
    );
    await waitFor(() =>
      expect(screen.getAllByRole('heading', { level: 1 }).length).toBe(1),
    );
  });

  it('says it could not load the run, with Try again', async () => {
    const api = jobsApi().on(
      'GET',
      '/api/v1/runs/:runId',
      apiError(404, 'not_found', 'No such run.'),
    );
    renderApp(api, '/pool/runs/9');
    expect(await screen.findByText(/Could not load the run/)).toBeTruthy();
    expect(screen.getByRole('button', { name: 'Try again' })).toBeTruthy();
  });

  it('says a run with no jobs has none', async () => {
    renderApp(
      jobsApi({ run: runItem({ jobs: [], edges: [], critical_path: [] }) }),
      '/pool/runs/100',
    );
    expect((await screen.findAllByText('This run has no jobs.')).length).toBeGreaterThan(
      0,
    );
  });

  it('lists every job once in one Jobs table that the graph and the timeline point to', async () => {
    renderApp(jobsApi({ run: runItem() }), '/pool/runs/100');
    await screen.findByText('ci.yml on OPS34-jobs-section');
    expect(screen.queryByText('Show as table')).toBeNull();
    const jobs = screen.getByRole('region', { name: 'Jobs' });
    const table = within(jobs).getByRole('table', { name: 'Jobs of run 100' });
    const heads = within(table)
      .getAllByRole('columnheader')
      .map((h) => h.textContent);
    expect(heads).toEqual(
      expect.arrayContaining([
        'Job',
        'Stage',
        'Needs',
        'Status',
        'Runner',
        'Waited',
        'Ran',
        'Forecast',
        'Critical path',
      ]),
    );
    expect(within(table).getAllByRole('row')).toHaveLength(4); // head and 3 jobs
    expect(within(table).getAllByText('critical path').length).toBeGreaterThan(0);
    const holder = table.closest('[id]');
    expect(holder?.id).toBeTruthy();
    const charts = screen
      .getAllByRole('group', { name: /^Jobs of run 100/ })
      .filter((g) => g.getAttribute('aria-describedby') === holder?.id);
    expect(charts).toHaveLength(2); // the graph and the timeline
  });

  it('says "1 job" for a run of one job', async () => {
    const one = runItem();
    renderApp(
      jobsApi({
        run: runItem({ jobs: [one.jobs[0] as (typeof one.jobs)[number]], edges: [] }),
      }),
      '/pool/runs/100',
    );
    expect(await screen.findByText(/This run has 1 job;/)).toBeTruthy();
    expect(screen.queryByText(/1 jobs/)).toBeNull();
  });

  it('shows the pull request only under Pull request and hides a worktree equal to the branch', async () => {
    renderApp(
      jobsApi({
        run: runItem({ pull_request: 52, worktree: 'OPS34-jobs-section' }),
      }),
      '/pool/runs/100',
    );
    await screen.findByText('ci.yml on OPS34-jobs-section');
    expect(screen.getAllByText('#52')).toHaveLength(1);
    expect(screen.getByText('a1b2c3d, push')).toBeTruthy();
    expect(screen.queryByText('Worktree')).toBeNull();
  });
});
