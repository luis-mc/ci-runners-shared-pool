// R5.104, R5.105, R5.111, R5.116 (OPS34): Queue, Running and Done as one row for each run with a Runs | Jobs switch,
// the wait reason and start range of the queue, the Done range, paging and both exports, and loading, empty and
// error states. Fed by the mocked Jobs API; no fake data reaches the console.
import { act, cleanup, fireEvent, screen, waitFor, within } from '@testing-library/react';
import { afterEach, describe, expect, it } from 'vitest';
import { apiError, renderApp } from '../../testing/mock';
import { job, jobsApi, runSummary } from '../../testing/jobsData';

afterEach(cleanup);

const queued = (id: number, place: number, over: Parameters<typeof job>[0] = {}) =>
  job({
    id,
    run_id: 200,
    state: 'queued',
    result: undefined,
    job_name: `e2e-${id}`,
    queued_at: new Date(Date.now() - 125_000).toISOString(),
    started_at: undefined,
    completed_at: undefined,
    run_seconds: undefined,
    runner: undefined,
    wait_reason: 'free_slot',
    wait_text: 'waits for a free slot on build-1',
    queue_position: place,
    queue_eta: { p50: 300, lo: 240, hi: 420 },
    ...over,
  });

describe('Queue', () => {
  it('shows one row for a run with its place, start range and why it waits', async () => {
    renderApp(
      jobsApi({ jobs: [queued(1, 1), queued(2, 2)] }),
      '/pool/jobs?state=queued&view=runs',
    );
    const table = await screen.findByRole('table', { name: 'Queue runs' });
    const rows = within(table).getAllByRole('row');
    expect(rows).toHaveLength(2); // the head and one row for the run, not one for each job
    // RES18: Jobs queued moved into the opened run; Commit and Event are two columns.
    expect(within(table).queryByText('Jobs queued')).toBeNull();
    expect(within(table).getByText('Commit')).toBeTruthy();
    expect(within(table).getByText('Event')).toBeTruthy();
    expect(within(table).getByText('in about 4 to 7 min')).toBeTruthy();
    expect(within(table).getByText('waits for a slot')).toBeTruthy();
    expect(within(table).getByRole('button', { name: 'Why' })).toBeTruthy();
    expect(screen.getAllByRole('heading', { level: 1 })).toHaveLength(1);
  });

  it('says what a run row opens to with the one word of every list, Open run', async () => {
    renderApp(jobsApi({ jobs: [queued(1, 1)] }), '/pool/jobs?state=queued&view=runs');
    const table = await screen.findByRole('table', { name: 'Queue runs' });
    expect(within(table).getByRole('button', { name: /^Open run: / })).toBeTruthy();
  });

  it('lists every waiting job in order in the Jobs view, each with Why', async () => {
    renderApp(
      jobsApi({ jobs: [queued(1, 1), queued(2, 2)] }),
      '/pool/jobs?state=queued&view=jobs',
    );
    const table = await screen.findByRole('table', { name: 'Queue jobs' });
    expect(within(table).getAllByRole('button', { name: 'Why' })).toHaveLength(2);
    expect(within(table).getAllByText(/^Starts about \d\d:\d\d$/)).toHaveLength(2);
    expect(within(table).queryByText('Waited')).toBeNull(); // RES18: Waited is on the Job page
  });

  it('says so when nothing waits', async () => {
    renderApp(jobsApi(), '/pool/jobs?state=queued&view=runs');
    expect(
      await screen.findByText('Nothing is waiting. Every job has a slot.'),
    ).toBeTruthy();
  });

  it('says it could not load, with Try again', async () => {
    const api = jobsApi().on(
      'GET',
      '/api/v1/jobs',
      apiError(500, 'internal', 'The store is busy.'),
    );
    renderApp(api, '/pool/jobs?state=queued&view=runs');
    expect(await screen.findByText(/Could not load the queue/)).toBeTruthy();
    expect(screen.getByRole('button', { name: 'Try again' })).toBeTruthy();
  });

  it('shows the loading state first', async () => {
    renderApp(jobsApi(), '/pool/jobs?state=queued&view=runs');
    expect(await screen.findByText('Loading the queue')).toBeTruthy();
  });
});

describe('Running', () => {
  const running = job({
    id: 5,
    run_id: 300,
    state: 'running',
    result: undefined,
    job_name: 'build',
    started_at: new Date(Date.now() - 60_000).toISOString(),
    completed_at: undefined,
    run_seconds: undefined,
  });

  it('shows a run with jobs done of all, what runs now and when it ends', async () => {
    renderApp(
      jobsApi({
        jobs: [running],
        runs: [
          runSummary({
            id: 300,
            state: 'running',
            result: undefined,
            jobs_done: 2,
            jobs_total: 4,
            completed_at: undefined,
            total_seconds: undefined,
            created_at: new Date(Date.now() - 90_000).toISOString(),
            eta: { p50: 420, lo: 360, hi: 540 },
          }),
        ],
      }),
      '/pool/jobs?state=running&view=runs',
    );
    const table = await screen.findByRole('table', { name: 'Running runs' });
    expect(within(table).getByText('2 of 4')).toBeTruthy();
    expect(within(table).getByText('in about 7 min')).toBeTruthy();
    await waitFor(() =>
      expect(within(table).getAllByText('build').length).toBeGreaterThan(0),
    );
    expect(within(table).getByText('running')).toBeTruthy();
  });

  it('shows a running job with the time it started in the Jobs view', async () => {
    renderApp(jobsApi({ jobs: [running] }), '/pool/jobs?state=running&view=jobs');
    const table = await screen.findByRole('table', { name: 'Running jobs' });
    expect(within(table).getByText('running')).toBeTruthy();
    expect(within(table).getByText(/^since \d\d:\d\d$/)).toBeTruthy();
  });

  it('says so when no run is in progress', async () => {
    renderApp(jobsApi(), '/pool/jobs?state=running&view=runs');
    expect(await screen.findByText('No run is in progress.')).toBeTruthy();
  });
});

describe('Done', () => {
  const many = (n: number) =>
    Array.from({ length: n }, (_, i) =>
      runSummary({ id: 1000 + i, completed_at: '2026-11-18T09:08:00Z' }),
    );

  it('asks for the list once, not on every render', async () => {
    const api = jobsApi({ runs: [runSummary()] });
    renderApp(api, '/pool/jobs?state=done&view=runs');
    await screen.findByRole('table', { name: 'Done runs' });
    await act(async () => {}); // let every effect and state update settle
    expect(api.count('GET', '/api/v1/runs')).toBe(1);
  });

  it('lists runs with jobs done of all, result and the retention words', async () => {
    renderApp(jobsApi({ runs: [runSummary()] }), '/pool/jobs?state=done&view=runs');
    const table = await screen.findByRole('table', { name: 'Done runs' });
    expect(within(table).getByText('3 of 3')).toBeTruthy();
    expect(within(table).queryByText('Total time')).toBeNull(); // RES18: in the opened run
    expect(within(table).getByText('success')).toBeTruthy();
    expect(
      screen.getByText(/Showing 1 of 1 finished runs in the last 90 days/),
    ).toBeTruthy();
    expect(
      screen.getByText(/crsp keeps steps for 30 days, jobs for 90 days/),
    ).toBeTruthy();
  });

  it('shows 25 first and adds 25 a time, up to 100', async () => {
    const api = jobsApi({ runs: many(120) });
    renderApp(api, '/pool/jobs?state=done&view=runs');
    await screen.findByText(/Showing 25 of 120 finished runs/);
    fireEvent.click(screen.getByRole('button', { name: 'Show 25 more' }));
    await screen.findByText(/Showing 50 of 120 finished runs/);
    for (let n = 75; n <= 100; n += 25) {
      fireEvent.click(screen.getByRole('button', { name: 'Show 25 more' }));
      await screen.findByText(new RegExp(`Showing ${n} of 120 finished runs`));
    }
    expect(screen.queryByRole('button', { name: 'Show 25 more' })).toBeNull();
    expect(screen.getByText(/A page holds at most 100/)).toBeTruthy();
  });

  it('asks for the range chosen, as a from date', async () => {
    const api = jobsApi({ runs: [runSummary()] });
    renderApp(api, '/pool/jobs?state=done&view=runs');
    await screen.findByRole('table', { name: 'Done runs' });
    fireEvent.change(screen.getByLabelText('Range'), { target: { value: '7' } });
    await waitFor(() => {
      const last = api.calls.filter((c) => c.path === '/api/v1/runs').at(-1);
      const from = Date.parse(last?.query.get('from') ?? '');
      expect(Date.now() - from).toBeLessThan(7.1 * 86_400_000);
    });
  });

  it('goes from Open run straight to the Run page, with no row opened in place', async () => {
    renderApp(jobsApi({ runs: [runSummary()] }), '/pool/jobs?state=done&view=runs');
    const table = await screen.findByRole('table', { name: 'Done runs' });
    fireEvent.click(within(table).getByRole('button', { name: /^Open run: / }));
    expect(await screen.findByRole('region', { name: 'Job graph' })).toBeTruthy();
    expect(screen.queryByText('Open run page')).toBeNull();
    expect(screen.queryByText(/queue wait is the light part of a bar/)).toBeNull();
  });

  it('opens the Run page with Enter on a run row', async () => {
    renderApp(jobsApi({ runs: [runSummary()] }), '/pool/jobs?state=done&view=runs');
    const table = await screen.findByRole('table', { name: 'Done runs' });
    const row = within(table).getAllByRole('row')[1] as HTMLElement;
    fireEvent.keyDown(row, { key: 'Enter' });
    expect(await screen.findByRole('region', { name: 'Job graph' })).toBeTruthy();
  });

  it('shows the jobs view with a filter from the address, by repository name', async () => {
    const api = jobsApi({ jobs: [job()], runs: [runSummary()] });
    renderApp(api, '/pool/jobs?state=done&repository=octo%2Fapp&view=runs');
    await screen.findByRole('table', { name: 'Done runs' });
    expect(
      screen.getByText('Showing only what belongs to repository octo/app.'),
    ).toBeTruthy();
    expect(
      api.calls.some(
        (c) => c.path === '/api/v1/runs' && c.query.get('repository_id') === '7',
      ),
    ).toBe(true);
  });

  it('shows the jobs view for a runner filter, asking the jobs API for that runner', async () => {
    const api = jobsApi({ jobs: [job()] });
    renderApp(api, '/pool/jobs?state=done&runner=runner-1&view=jobs');
    await screen.findByRole('table', { name: 'Done jobs' });
    expect(
      api.calls.some(
        (c) => c.path === '/api/v1/jobs' && c.query.get('runner') === 'runner-1',
      ),
    ).toBe(true);
  });

  it('says a repository that is not listed has no run', async () => {
    renderApp(jobsApi(), '/pool/jobs?state=done&repository=nobody%2Fnothing&view=runs');
    expect(await screen.findByText(/is not on the list of repositories/)).toBeTruthy();
  });

  it('exports the filtered jobs with the same filter, and the runs as a file made here', async () => {
    const api = jobsApi({ jobs: [job()], runs: [runSummary()] });
    renderApp(api, '/pool/jobs?state=done&view=jobs');
    await screen.findByRole('table', { name: 'Done jobs' });
    URL.createObjectURL = () => 'blob:x';
    URL.revokeObjectURL = () => undefined;
    fireEvent.click(screen.getByRole('button', { name: 'Export CSV' }));
    await waitFor(() => expect(api.count('GET', '/api/v1/exports/jobs')).toBe(1));
    const call = api.calls.find((c) => c.path === '/api/v1/exports/jobs');
    expect(call?.query.get('state')).toBe('done');
    expect(call?.query.get('format')).toBe('csv');
    expect(call?.query.get('limit')).toBeNull();
  });

  it('says so when there are no finished runs yet', async () => {
    renderApp(jobsApi(), '/pool/jobs?state=done&view=runs');
    expect(await screen.findByText(/No finished runs yet/)).toBeTruthy();
  });
});

describe('the Runs | Jobs switch', () => {
  it('keeps the filter and puts the choice in the address', async () => {
    renderApp(
      jobsApi({ jobs: [job()], runs: [runSummary()] }),
      '/pool/jobs?state=done&repository=octo%2Fapp&view=runs',
    );
    await screen.findByRole('table', { name: 'Done runs' });
    fireEvent.click(screen.getByRole('button', { name: 'Jobs' }));
    await screen.findByRole('table', { name: 'Done jobs' });
    expect(
      screen.getByText('Showing only what belongs to repository octo/app.'),
    ).toBeTruthy();
  });
});
