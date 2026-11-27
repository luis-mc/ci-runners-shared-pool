// R5.20: the overview shows machines with load bars, the queue, the running jobs, live; it has no command.
import { act, cleanup, screen, within } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import {
  FakeEventSource,
  apiError,
  machine,
  overview,
  renderApp,
  signedIn as signedInBase,
} from '../../testing/mock';

/** The routes the Overview draws besides the overview itself: the slot timeline (LOOK61). */
const signedIn = () =>
  signedInBase().on('GET', '/api/v1/timeline', {
    from: '2026-11-13T10:00:00Z',
    to: '2026-11-14T10:00:00Z',
    more_before: false,
    rows: [],
    bars: [],
  });

const facts = { threads: 6, cores: 6, memory_bytes: 9 * 1024 ** 3 };

afterEach(cleanup);

describe('Overview (R5.20)', () => {
  it('shows a card per machine: pool size in the title, CPU and Memory in cores and GiB, a link to Pool > Machines', async () => {
    const m = machine({
      facts,
      load: {
        cpu_pct: 50,
        memory_pct: 100,
        cpu_known: true,
        memory_known: true,
        sampled_at: 'x',
        stale: false,
      },
    });
    const api = signedIn()
      .on('GET', '/api/v1/overview', overview({ machines: [m] }))
      .on('GET', '/api/v1/machines', { items: [m] });
    renderApp(api, '/');
    const card = await screen.findByRole('region', { name: 'build-1 6 CPU / 9 GiB' });
    expect(
      within(card).getByRole('meter', { name: 'CPU' }).getAttribute('aria-valuetext'),
    ).toBe('3 of 6');
    expect(
      within(card).getByRole('meter', { name: 'Memory' }).getAttribute('aria-valuetext'),
    ).toBe('9 of 9 GiB');
    expect(within(card).queryByRole('meter', { name: 'Slots' })).toBeNull();
    expect(
      within(card)
        .getByRole('link', { name: 'Open in Pool > Machines' })
        .getAttribute('href'),
    ).toBe('/pool/machines/m1');
  });

  it('falls back to percent while the machine facts are not known', async () => {
    renderApp(signedIn(), '/');
    const card = await screen.findByRole('region', { name: 'build-1' });
    expect(
      within(card).getByRole('meter', { name: 'Memory' }).getAttribute('aria-valuetext'),
    ).toBe('40%');
  });

  it('puts the tiles first, then Best next change, then the machines (LOOK61)', async () => {
    const api = signedIn();
    renderApp(api, '/');
    await screen.findByRole('region', { name: 'build-1' });
    const main = screen.getByRole('main');
    const order = Array.from(main.querySelectorAll('section.ds-pane, .ds-stat-grid')).map(
      (e) => e.getAttribute('aria-label') ?? e.className,
    );
    expect(order.indexOf('ds-stat-grid')).toBeLessThan(order.indexOf('build-1'));
    expect(order.indexOf('Next to start')).toBeGreaterThan(order.indexOf('build-1'));
    expect(order.indexOf('Routing labels')).toBeGreaterThan(
      order.indexOf('Next to start'),
    );
    expect(order.indexOf('Jobs over time')).toBeGreaterThan(
      order.indexOf('Routing labels'),
    );
    expect(
      screen.getByText('The pool at a glance. Open a card for the detail.'),
    ).toBeTruthy();
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
    const next = await screen.findByRole('region', { name: 'Next to start' });
    expect(within(next).getByText('unit tests')).toBeTruthy();
    act(() =>
      es.emit({
        id: '3',
        type: 'job.started',
        at: 'x',
        job: { ...job, state: 'running', runner_name: 'crsp-linux-1' },
      }),
    );
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
    ).toBe('60%');
  });

  it('lists the first waiting jobs in start order under Next to start, with a word for why', async () => {
    const base = {
      repository: 'octo/app',
      run_id: 1,
      run_attempt: 1,
      github_job_id: 1,
      labels: ['crsp-small', 'crsp-rust'],
      state: 'queued' as const,
    };
    const api = signedIn().on(
      'GET',
      '/api/v1/overview',
      overview({
        queued_count: 4,
        queued: [
          {
            ...base,
            id: 1,
            job_name: 'unit',
            wait_reason: 'free_slot',
            queue_position: 2,
          },
          {
            ...base,
            id: 2,
            job_name: 'lint',
            wait_reason: 'repository_parallel_limit',
            queue_position: 3,
          },
          {
            ...base,
            id: 3,
            job_name: 'e2e',
            wait_reason: 'no_runner_with_labels',
            queue_position: 1,
            labels: ['crsp-e2e'],
          },
          {
            ...base,
            id: 4,
            job_name: 'docs',
            wait_reason: 'free_slot',
            queue_position: 4,
          },
        ],
      }),
    );
    renderApp(api, '/', { stream: false });
    const pane = await screen.findByRole('region', { name: 'Next to start' });
    const codes = Array.from(pane.querySelectorAll('code')).map((c) => c.textContent);
    expect(codes).toEqual(['e2e', 'unit', 'lint']);
    expect(within(pane).getByText('No runner fits')).toBeTruthy();
    expect(within(pane).getByText('Waiting for room')).toBeTruthy();
    expect(within(pane).getByText('Behind another job')).toBeTruthy();
    expect(
      within(pane)
        .getByRole('link', { name: 'See the whole plan in Pool > Queue Analysis (4)' })
        .getAttribute('href'),
    ).toBe('/pool/queue');
    const labels = screen.getByRole('region', { name: 'Routing labels' });
    expect(
      Array.from(labels.querySelectorAll('.ds-tag')).map((t) => t.textContent),
    ).toEqual(['crsp-e2e', 'crsp-rust', 'crsp-small']);
    expect(
      within(labels).getByRole('link', { name: 'See all 3 labels in Pool > Overview' }),
    ).toBeTruthy();
  });

  it('shows a timer alert for a job whose timer ran out (scheduling R5.100)', async () => {
    const job = {
      id: 7,
      repository: 'octo/app',
      run_id: 1,
      run_attempt: 1,
      github_job_id: 1,
      job_name: 'rust',
      labels: [],
      state: 'running' as const,
    };
    const inGrace = { ...job, timer_deadline: '2026-11-27T10:00:20Z' };
    // A running job that is not in its grace has no deadline, and its timer is not asked for (audit R3-10).
    const other = { ...job, id: 8, job_name: 'lint' };
    const api = signedIn()
      .on('GET', '/api/v1/overview', overview({ running: [inGrace, other] }))
      .on('GET', '/api/v1/jobs/7/timer', {
        job_id: 7,
        state: 'grace',
        text: 'Job timer ran out at 30:00; stopping in 20 s',
        can_stop_now: true,
        can_extend: true,
        extended: false,
        covered_by_run: false,
      });
    renderApp(api, '/', { stream: false });
    const alert = await screen.findByRole('alert');
    expect(alert.textContent).toContain('Job timer ran out at 30:00; stopping in 20 s');
    expect(
      within(alert)
        .getByRole('link', { name: 'Open rust in Pool > Jobs' })
        .getAttribute('href'),
    ).toBe('/pool/jobs/7');
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
    const h2 = Array.from(main.querySelectorAll('h2, h3'));
    expect(h2.length).toBeGreaterThanOrEqual(4);
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
    ).toBe('/setup?wizard=1');
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
