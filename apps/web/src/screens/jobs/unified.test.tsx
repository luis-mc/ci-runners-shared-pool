// The one list of Live, Done and Run elsewhere (owner decision 20 Nov 2026): the state filter All, Queue, Running and
// Done in one table, the search with its field words, the details panel, focus in the search box, and Group by.
import { cleanup, fireEvent, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { afterEach, describe, expect, it } from 'vitest';
import { renderApp } from '../../testing/mock';
import { job, jobsApi, runSummary } from '../../testing/jobsData';

afterEach(cleanup);

const queuedJob = job({
  id: 1,
  run_id: 200,
  state: 'queued',
  result: undefined,
  job_name: 'wait-job',
  runner: undefined,
  queue_position: 1,
  wait_reason: 'free_slot',
  wait_text: 'waits for a free slot on build-1',
  queue_eta: { p50: 300, lo: 240, hi: 420 },
});
const runningJob = job({
  id: 2,
  run_id: 201,
  state: 'running',
  result: undefined,
  job_name: 'run-job',
  completed_at: undefined,
});
const runs = [
  runSummary({
    id: 201,
    state: 'running',
    result: undefined,
    completed_at: undefined,
    jobs_done: 1,
  }),
  runSummary({ id: 100, repository: 'octo/web', branch: 'fix-login' }),
];
const data = { jobs: [queuedJob, runningJob], runs };
const rows = (t: HTMLElement) => within(t).getAllByRole('row').slice(1);

describe('the unified list', () => {
  it('lists waiting, running and finished runs in one table, in that order', async () => {
    renderApp(jobsApi(data), '/pool/jobs');
    const table = await screen.findByRole('table', { name: 'All runs' });
    const r = rows(table);
    expect(r).toHaveLength(3);
    expect(r[0]?.textContent).toContain('waiting');
    expect(r[1]?.textContent).toContain('running');
    expect(r[2]?.textContent).toContain('success');
    expect(screen.getByRole('button', { name: 'All' }).getAttribute('aria-pressed')).toBe(
      'true',
    );
  });

  it('opens the Done tab on the Done filter, and the filter changes the list', async () => {
    renderApp(jobsApi(data), '/pool/jobs?state=done');
    const done = await screen.findByRole('table', { name: 'Done runs' });
    expect(rows(done)).toHaveLength(1);
    fireEvent.click(screen.getByRole('button', { name: 'Queue' }));
    const queue = await screen.findByRole('table', { name: 'Queue runs' });
    expect(rows(queue)).toHaveLength(1);
  });

  it('starts with focus in the search box and narrows by words and field words', async () => {
    const user = userEvent.setup();
    renderApp(jobsApi(data), '/pool/jobs');
    await screen.findByRole('table', { name: 'All runs' });
    // The table is drawn again when the list refreshes, so ask for it each time.
    const now = () => rows(screen.getByRole('table', { name: /runs$/ }));
    const box = screen.getByRole('textbox', { name: /Search/ });
    await waitFor(() => expect(document.activeElement).toBe(box));
    await user.keyboard('repo:octo/web');
    await waitFor(() => expect(now()).toHaveLength(1));
    await user.clear(box);
    await user.keyboard('state:wait');
    await waitFor(() => expect(now()).toHaveLength(1));
    expect(now()[0]?.textContent).toContain('waiting');
    await user.clear(box);
    await user.keyboard('nothing-like-this');
    expect(await screen.findByText(/Nothing matches/)).toBeTruthy();
  });

  it('shows the details of the chosen row in a panel', async () => {
    renderApp(jobsApi(data), '/pool/jobs');
    await screen.findByRole('table', { name: 'All runs' });
    fireEvent.click(screen.getByRole('button', { name: 'Show details' }));
    const side = await screen.findByRole('complementary');
    expect(within(side).getByText('waits for a free slot on build-1')).toBeTruthy();
    expect(screen.getByRole('button', { name: 'Hide details' })).toBeTruthy();
  });

  it('lists jobs of every state in the Jobs view', async () => {
    renderApp(
      jobsApi({
        ...data,
        jobs: [queuedJob, runningJob, job({ id: 3, job_name: 'done-job' })],
      }),
      '/pool/jobs?view=jobs',
    );
    const table = await screen.findByRole('table', { name: 'All jobs' });
    expect(rows(table)).toHaveLength(3);
  });
});

describe('Group by on Done', () => {
  it('replaces the list with the groups when one is chosen, and None brings the list back', async () => {
    renderApp(jobsApi(data), '/pool/jobs?state=done');
    await screen.findByRole('table', { name: 'Done runs' });
    fireEvent.click(
      within(screen.getByRole('group', { name: 'Group by' })).getByRole('button', {
        name: 'Repository',
      }),
    );
    await screen.findAllByText(/No jobs yet|Repositories/);
    expect(screen.queryByRole('table', { name: 'Done runs' })).toBeNull();
    fireEvent.click(screen.getByRole('button', { name: 'None' }));
    expect(await screen.findByRole('table', { name: 'Done runs' })).toBeTruthy();
  });
});
