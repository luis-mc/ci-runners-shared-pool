// R5.20: the overview shows machines with load bars, the queue, the running jobs, live; it has no command.
import { act, cleanup, screen, within } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import {
  FakeEventSource,
  apiError,
  machine,
  overview,
  renderApp,
  signedIn,
} from '../../testing/mock';

afterEach(cleanup);

describe('Overview (R5.20)', () => {
  it('shows a card per machine with a state word and three level bars', async () => {
    renderApp(signedIn(), '/');
    const card = await screen.findByRole('region', { name: 'build-1' });
    expect(within(card).getByText('online')).toBeTruthy();
    expect(
      within(card).getByRole('meter', { name: 'Memory' }).getAttribute('aria-valuetext'),
    ).toBe('40% of 80%');
    expect(within(card).getByRole('meter', { name: 'CPU' })).toBeTruthy();
    expect(
      within(card).getByRole('meter', { name: 'Slots' }).getAttribute('aria-valuetext'),
    ).toBe('1 of 4');
    expect(within(card).getByRole('link', { name: 'build-1' }).getAttribute('href')).toBe(
      '/setup/machines/m1',
    );
  });

  it('names an offline machine with a word, not only a colour (UX-58)', async () => {
    const api = signedIn()
      .on('GET', '/api/v1/overview', overview({ machines: [machine({ online: false })] }))
      .on('GET', '/api/v1/machines', { items: [machine({ online: false })] });
    renderApp(api, '/');
    const card = await screen.findByRole('region', { name: 'build-1' });
    expect(within(card).getByText('offline')).toBeTruthy();
    expect(card.textContent).toContain('✕');
  });

  it('follows the live stream: load, queue and running jobs', async () => {
    renderApp(signedIn(), '/');
    await screen.findByRole('region', { name: 'build-1' });
    const es = FakeEventSource.latest();
    act(() => es.emit({ id: '1', type: 'snapshot', at: 'x', snapshot: overview() }));
    const job = {
      id: 5,
      repository: 'octo/app',
      run_id: 1,
      run_attempt: 1,
      github_job_id: 1,
      job_name: 'unit tests',
      labels: [],
      state: 'queued' as const,
    };
    act(() => es.emit({ id: '2', type: 'job.queued', at: 'x', job }));
    expect(await screen.findByText('Queued jobs')).toBeTruthy();
    expect(screen.getByText('Queued jobs').nextElementSibling?.textContent).toBe('1');
    act(() =>
      es.emit({
        id: '3',
        type: 'job.started',
        at: 'x',
        job: { ...job, state: 'running', runner_name: 'crsp-linux-1' },
      }),
    );
    expect(await screen.findByText('unit tests')).toBeTruthy();
    act(() =>
      es.emit({
        id: '4',
        type: 'machine.load',
        at: 'x',
        load: {
          machine_id: 'm1',
          load: {
            memory_pct: 60,
            memory_known: true,
            cpu_known: false,
            sampled_at: 'x',
            stale: false,
          },
          slots_used: 2,
        },
      }),
    );
    const card = screen.getByRole('region', { name: 'build-1' });
    expect(
      within(card).getByRole('meter', { name: 'Memory' }).getAttribute('aria-valuetext'),
    ).toBe('60% of 80%');
  });

  it('lists each waiting job with the first reason in words (scheduling R5.48)', async () => {
    const base = {
      repository: 'octo/app',
      run_id: 1,
      run_attempt: 1,
      github_job_id: 1,
      labels: [],
      state: 'queued' as const,
    };
    const api = signedIn().on(
      'GET',
      '/api/v1/overview',
      overview({
        queued_count: 3,
        queued: [
          { ...base, id: 1, job_name: 'unit', wait_reason: 'load_not_measured' },
          { ...base, id: 2, job_name: 'lint', wait_reason: 'free_slot' },
          { ...base, id: 3, job_name: 'e2e', wait_reason: 'load_over_budget' },
        ],
        waiting: [
          {
            job_id: 1,
            repository: 'octo/app',
            job_name: 'unit',
            reason: 'load_not_measured',
            words:
              'Load not measured yet. The machine cannot read its load, so the job waits.',
          },
          {
            job_id: 3,
            repository: 'octo/app',
            job_name: 'e2e',
            reason: 'load_over_budget',
            words: 'Over the load budget: memory 91% > 85%.',
          },
        ],
      }),
    );
    renderApp(api, '/', { stream: false });
    const table = await screen.findByRole('table', { name: 'Waiting jobs' });
    expect(within(table).getByText(/^Load not measured yet\./)).toBeTruthy();
    expect(
      within(table).getByText('Over the load budget: memory 91% > 85%.'),
    ).toBeTruthy();
    // no server words for this job: its own reason, in words
    expect(within(table).getByText('Waits for a free slot.')).toBeTruthy();
    expect(within(table).getAllByRole('row')).toHaveLength(4);
  });

  it('says what to do when there is no machine yet', async () => {
    const api = signedIn()
      .on('GET', '/api/v1/overview', overview({ machines: [] }))
      .on('GET', '/api/v1/machines', { items: [] });
    renderApp(api, '/');
    expect(
      await screen.findByText(
        'No machine is in the pool yet. Add a machine so jobs have somewhere to run.',
      ),
    ).toBeTruthy();
    expect(screen.getByRole('link', { name: 'Add a machine' })).toBeTruthy();
  });

  it('shows the machines of the machine list even when the live overview has none (finding 2)', async () => {
    const api = signedIn().on('GET', '/api/v1/overview', overview({ machines: [] }));
    renderApp(api, '/');
    expect(await screen.findByRole('region', { name: 'build-1' })).toBeTruthy();
    expect(screen.queryByText(/No machine is in the pool yet/)).toBeNull();
  });

  it('has no command button (UX-52)', async () => {
    renderApp(signedIn(), '/');
    await screen.findByRole('region', { name: 'build-1' });
    const main = screen.getByRole('main');
    // The ? beside a term explains a word; it is not a command.
    expect(
      within(main)
        .queryAllByRole('button')
        .filter((b) => !b.hasAttribute('data-term-help')),
    ).toHaveLength(0);
  });

  it('puts every section in a pane (UX-10)', async () => {
    renderApp(signedIn(), '/');
    await screen.findByRole('region', { name: 'build-1' });
    const main = screen.getByRole('main');
    const h2 = Array.from(main.querySelectorAll('h2'));
    expect(h2.length).toBeGreaterThanOrEqual(3);
    expect(h2.every((h) => h.closest('section.ds-pane') !== null)).toBe(true);
    expect(screen.getAllByRole('heading', { level: 1 })).toHaveLength(1);
  });

  it('keeps the h1 and a pane around the failed state (UX-17)', async () => {
    const api = signedIn().on('GET', '/api/v1/overview', () =>
      apiError(503, 'unavailable', 'The controller is starting.'),
    );
    renderApp(api, '/', { stream: false });
    await screen.findByText(/Could not load the overview/);
    expect(screen.getByRole('heading', { level: 1, name: 'Overview' })).toBeTruthy();
    expect(screen.getByRole('region', { name: 'The pool' })).toBeTruthy();
  });

  it('says it could not load when the controller does not answer', async () => {
    const api = signedIn().on('GET', '/api/v1/overview', () =>
      apiError(503, 'unavailable', 'The controller is starting.'),
    );
    renderApp(api, '/', { stream: false });
    expect(await screen.findByText(/Could not load the overview/)).toBeTruthy();
  });
});

describe('App health banner (github-integration R5.6)', () => {
  it('says the App is gone, that running jobs keep running, and offers the wizard', async () => {
    const api = signedIn().on('GET', '/api/v1/github/app-health', {
      gone: true,
      reason: 'GitHub does not know this App any more.',
      checked_at: '2026-11-16T08:00:00Z',
    });
    renderApp(api, '/');
    const alert = await screen.findByRole('alert');
    expect(alert.textContent).toContain('The GitHub App is gone.');
    expect(alert.textContent).toContain('GitHub does not know this App any more.');
    expect(alert.textContent).toContain('Jobs that are running keep running.');
    expect(
      within(alert)
        .getByRole('link', { name: 'Run the setup wizard again' })
        .getAttribute('href'),
    ).toBe('/setup');
  });

  it('draws nothing while the App answers', async () => {
    const api = signedIn().on('GET', '/api/v1/github/app-health', { gone: false });
    renderApp(api, '/');
    await screen.findByRole('region', { name: 'build-1' });
    await vi.waitFor(() => expect(api.count('GET', '/api/v1/github/app-health')).toBe(1));
    expect(screen.queryByRole('alert')).toBeNull();
  });

  it('draws nothing when the answer is not known', async () => {
    renderApp(signedIn(), '/');
    await screen.findByRole('region', { name: 'build-1' });
    expect(screen.queryByRole('alert')).toBeNull();
  });
});
