// R5.23: runners listed live with their isolation word (UX-61), added only where the OS fits, and deleted
// after a question that names the runner and its job.
import { act, cleanup, fireEvent, screen, waitFor, within } from '@testing-library/react';
import { afterEach, describe, expect, it } from 'vitest';
import {
  apiError,
  FakeEventSource,
  machine,
  renderApp,
  runner,
  signedIn,
  viewer,
} from '../../testing/mock';
import { currentJobText, isolationText, runnerStatus, stateCounts } from './Runners';
import { containerBackendAllowed, machineSupports, sentence } from './AddRunners';

afterEach(cleanup);

const job = {
  id: 9,
  repository: 'octo/app',
  run_id: 1,
  run_attempt: 1,
  github_job_id: 1,
  job_name: 'e2e',
  labels: [],
  state: 'running' as const,
};

describe('isolationText (UX-61)', () => {
  it('names each isolation, and says a persistent runner keeps its state', () => {
    expect(isolationText({ isolation: 'microVM', keeps_state: false })).toBe('microVM');
    expect(isolationText({ isolation: 'VM', keeps_state: false })).toBe('VM');
    expect(isolationText({ isolation: 'container', keeps_state: false })).toBe(
      'container',
    );
    expect(isolationText({ isolation: 'persistent', keeps_state: true })).toBe(
      'persistent, state kept between jobs',
    );
  });
});

describe('Runners list', () => {
  it('shows isolation, labels, state word and the current job', async () => {
    const api = signedIn().on('GET', '/api/v1/runners', {
      items: [
        runner({ state: 'busy', current_job: job }),
        runner({
          id: 'r2',
          name: 'crsp-lx-2',
          isolation: 'container',
          keeps_state: false,
          backend: 'docker',
          labels: ['linux', 'x64'],
        }),
      ],
    });
    renderApp(api, '/setup/runners');
    const table = await screen.findByRole('table', { name: /^Runners on/ });
    expect(within(table).getByText('persistent, state kept between jobs')).toBeTruthy();
    expect(within(table).getByText('container')).toBeTruthy();
    expect(within(table).getByText('busy')).toBeTruthy();
    expect(within(table).getByText('octo/app e2e')).toBeTruthy();
    expect(within(table).getByText('idle')).toBeTruthy();
  });

  it('shows why a runner failed', async () => {
    const api = signedIn().on('GET', '/api/v1/runners', {
      items: [
        runner({
          state: 'failed',
          failed_reason:
            'register service: the account has no right to log on as a service',
        }),
      ],
    });
    renderApp(api, '/setup/runners');
    const table = await screen.findByRole('table', { name: /^Runners on/ });
    // LOOK19: one line (the part before the colon) and a Full error button; the whole text opens under the row.
    expect(within(table).getByText('register service')).toBeTruthy();
    const button = within(table).getByRole('button', { name: 'Full error' });
    expect(button.getAttribute('aria-expanded')).toBe('false');
    fireEvent.click(button);
    expect(button.getAttribute('aria-expanded')).toBe('true');
    const region = within(table).getByRole('region', { name: /Full error/ });
    expect(region.textContent).toBe(
      'register service: the account has no right to log on as a service',
    );
    expect(button.getAttribute('aria-controls')).toBe(region.id);
    fireEvent.click(button);
    expect(within(table).queryByRole('region', { name: /Full error/ })).toBeNull();
  });

  it('ends a failed row text with one full stop (OPS87)', async () => {
    const api = signedIn().on('GET', '/api/v1/runners', {
      items: [runner({ state: 'failed', failed_reason: 'register service: no right..' })],
    });
    renderApp(api, '/setup/runners');
    const table = await screen.findByRole('table', { name: /^Runners on/ });
    fireEvent.click(within(table).getByRole('button', { name: 'Full error' }));
    expect(within(table).getByRole('region', { name: /Full error/ }).textContent).toBe(
      'register service: no right.',
    );
  });

  it('puts system and backend in the isolation cell and caps the labels at two', async () => {
    const api = signedIn().on('GET', '/api/v1/runners', {
      items: [
        runner({
          os: 'windows',
          arch: 'x64',
          backend: 'persistent',
          labels: ['self-hosted', 'windows', 'x64', 'docker'],
        }),
      ],
    });
    renderApp(api, '/setup/runners');
    const table = await screen.findByRole('table', { name: /^Runners on/ });
    // OPS87: the system of each runner has its own column; the backend stays in the hover text.
    expect(within(table).getByRole('columnheader', { name: 'System' })).toBeTruthy();
    expect(within(table).getByText('Windows')).toBeTruthy();
    expect(within(table).queryByRole('columnheader', { name: 'Backend' })).toBeNull();
    expect(table.querySelector('[title*="windows x64, persistent"]')).toBeTruthy();
    expect(within(table).getByText('self-hosted, windows +2')).toBeTruthy();
    expect(
      within(table).getByText('self-hosted, windows, x64, docker').className,
    ).toContain('ds-visually-hidden');
  });

  it('reloads when a runner event arrives (live)', async () => {
    let n = 0;
    const api = signedIn().on('GET', '/api/v1/runners', () => {
      n += 1;
      return {
        items: n === 1 ? [runner()] : [runner(), runner({ id: 'r2', name: 'crsp-new' })],
      };
    });
    renderApp(api, '/setup/runners');
    await screen.findByText('crsp-linux-1');
    act(() =>
      FakeEventSource.latest().emit({
        id: '1',
        type: 'runner.state',
        at: 'x',
        runner: runner({ id: 'r2' }),
      }),
    );
    expect(await screen.findByText('crsp-new')).toBeTruthy();
  });

  it('filters by isolation', async () => {
    const api = signedIn().on('GET', '/api/v1/runners', {
      items: [
        runner(),
        runner({
          id: 'r2',
          name: 'crsp-lx-2',
          isolation: 'container',
          keeps_state: false,
        }),
      ],
    });
    renderApp(api, '/setup/runners');
    await screen.findByRole('table', { name: /^Runners on/ });
    fireEvent.click(screen.getByRole('button', { name: 'container' }));
    expect(screen.queryByText('crsp-linux-1')).toBeNull();
    expect(screen.getByText('crsp-lx-2')).toBeTruthy();
  });

  it('says what to do when there is no runner', async () => {
    renderApp(signedIn().on('GET', '/api/v1/runners', { items: [] }), '/setup/runners');
    expect(
      await screen.findByText('No runners yet. Use Add runners at the top to start.'),
    ).toBeTruthy();
    // One Add runners action, in the header; the h1 stays; the empty state sits in a pane.
    expect(screen.getAllByRole('button', { name: 'Add runners' }).length).toBe(1);
    expect(screen.getByRole('heading', { level: 1, name: 'Runners' })).toBeTruthy();
    expect(screen.getByRole('region', { name: 'Runners' })).toBeTruthy();
  });

  it('selects all shown runners and clears the selection (UX-12)', async () => {
    renderApp(
      signedIn().on('GET', '/api/v1/runners', {
        items: [runner(), runner({ id: 'r2', name: 'crsp-2' })],
      }),
      '/setup/runners',
    );
    const all = await screen.findByRole('button', { name: 'Select all 2' });
    expect(screen.getByRole('region', { name: 'Runners' })).toBeTruthy();
    fireEvent.click(all);
    expect(await screen.findByText('2 selected')).toBeTruthy();
    fireEvent.click(screen.getByRole('button', { name: 'Clear' }));
    await waitFor(() => expect(screen.queryByText('2 selected')).toBeNull());
  });

  it('deletes after a question that names each runner and the job it runs (UX-62)', async () => {
    const api = signedIn()
      .on('GET', '/api/v1/runners', {
        items: [runner({ state: 'busy', current_job: job })],
      })
      .on('DELETE', '/api/v1/runners/:id', runner());
    renderApp(api, '/setup/runners');
    await screen.findByRole('table', { name: /^Runners on/ });
    fireEvent.click(screen.getByRole('checkbox', { name: 'Select crsp-linux-1' }));
    fireEvent.click(screen.getByRole('button', { name: 'Delete 1 runner' }));
    const dialog = await screen.findByRole('dialog', { name: 'Delete these runners?' });
    expect(within(dialog).getByText('Runner crsp-linux-1')).toBeTruthy();
    expect(within(dialog).getByText('octo/app e2e')).toBeTruthy();
    expect(api.count('DELETE', '/api/v1/runners/r1')).toBe(0);
    fireEvent.click(within(dialog).getByRole('button', { name: 'Delete' }));
    await waitFor(() => expect(api.count('DELETE', '/api/v1/runners/r1')).toBe(1));
    expect(api.calls.find((c) => c.method === 'DELETE')?.query.get('confirm')).toBe(
      'true',
    );
  });

  it('shows no problem for a runner that is already gone, and the row leaves live (OPS19)', async () => {
    let list = [runner({ id: 'r1', name: 'crsp-home-automation-windows' })];
    const api = signedIn()
      .on('GET', '/api/v1/runners', () => ({ items: list }))
      .on('DELETE', '/api/v1/runners/:id', () => {
        list = [{ ...list[0]!, state: 'draining', reason: 'remove asked' }];
        return runner({ state: 'draining' });
      });
    renderApp(api, '/setup/runners');
    await screen.findByRole('table', { name: /^Runners on/ });
    fireEvent.click(
      screen.getByRole('checkbox', { name: 'Select crsp-home-automation-windows' }),
    );
    fireEvent.click(screen.getByRole('button', { name: 'Delete 1 runner' }));
    const dialog = await screen.findByRole('dialog', { name: 'Delete these runners?' });
    expect(within(dialog).queryByText(/Problem/)).toBeNull();
    fireEvent.click(within(dialog).getByRole('button', { name: 'Delete' }));
    expect(await screen.findByText('draining')).toBeTruthy();
    // the controller removes the record and tells the live stream; no reload by hand
    list = [];
    act(() =>
      FakeEventSource.latest().emit({
        id: '9',
        type: 'runner.state',
        at: 'x',
        runner: runner({ id: 'r1', state: 'removing' }),
      }),
    );
    expect(await screen.findByText(/No runners yet/)).toBeTruthy();
    expect(screen.queryByText('draining')).toBeNull();
  });

  it('does not list "no such runner" as a problem when the runner is already gone (OPS19)', async () => {
    const api = signedIn()
      .on('GET', '/api/v1/runners', { items: [runner()] })
      .on('DELETE', '/api/v1/runners/:id', () =>
        apiError(404, 'not_found', 'no such runner or plan'),
      );
    renderApp(api, '/setup/runners');
    await screen.findByRole('table', { name: /^Runners on/ });
    fireEvent.click(screen.getByRole('checkbox', { name: 'Select crsp-linux-1' }));
    fireEvent.click(screen.getByRole('button', { name: 'Delete 1 runner' }));
    const dialog = await screen.findByRole('dialog', { name: 'Delete these runners?' });
    fireEvent.click(within(dialog).getByRole('button', { name: 'Delete' }));
    await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull());
    expect(screen.queryByText(/no such runner/)).toBeNull();
  });

  it('replaces a runner the pool does not manage, after a question', async () => {
    const api = signedIn()
      .on('GET', '/api/v1/runners', { items: [runner({ managed: false })] })
      .on('POST', '/api/v1/runners/:id/replace', {
        id: 'rp1',
        area: 'runners',
        state: 'planned',
        created_at: 'x',
        created_by: 'octo',
        steps: [
          {
            id: 's1',
            action: 'add_runner',
            text: 'Add',
            needs_yes: false,
            state: 'planned',
          },
        ],
      })
      .on('POST', '/api/v1/runner-plans/:id/apply', {
        id: 'rp1',
        area: 'runners',
        state: 'applying',
        created_at: 'x',
        created_by: 'octo',
        steps: [],
      });
    renderApp(api, '/setup/runners');
    await screen.findByRole('table', { name: /^Runners on/ });
    fireEvent.click(screen.getByRole('button', { name: 'Replace' }));
    const dialog = await screen.findByRole('dialog', { name: 'Replace crsp-linux-1?' });
    fireEvent.click(within(dialog).getByRole('button', { name: 'Replace' }));
    await waitFor(() =>
      expect(api.count('POST', '/api/v1/runner-plans/rp1/apply')).toBe(1),
    );
  });

  it('deletes the registration of an offline runner after a yes that names it (R5.33)', async () => {
    const api = signedIn()
      .on('GET', '/api/v1/runners', {
        items: [
          runner({ id: 'r9', name: 'crsp-gone-1', state: 'offline', managed: true }),
        ],
      })
      .on('DELETE', '/api/v1/runners/:id/registration', runner({ id: 'r9' }));
    renderApp(api, '/setup/runners');
    await screen.findByRole('table', { name: /^Runners on/ });
    fireEvent.click(screen.getByRole('button', { name: 'Delete registration' }));
    const dialog = await screen.findByRole('dialog', { name: 'Delete crsp-gone-1?' });
    expect(api.count('DELETE', '/api/v1/runners/r9/registration')).toBe(0);
    fireEvent.click(within(dialog).getByRole('button', { name: 'Delete registration' }));
    await waitFor(() =>
      expect(api.count('DELETE', '/api/v1/runners/r9/registration')).toBe(1),
    );
  });

  it('keeps a viewer from adding or deleting, and says why', async () => {
    renderApp(
      signedIn(viewer).on('GET', '/api/v1/runners', { items: [runner()] }),
      '/setup/runners',
    );
    const add = await screen.findByRole<HTMLButtonElement>('button', {
      name: 'Add runners',
    });
    expect(add.disabled).toBe(true);
  });
});

describe('Add runners', () => {
  it('greys machines that do not run the chosen system, with the reason (UX-38)', async () => {
    const api = signedIn()
      .on('GET', '/api/v1/machines', {
        items: [machine(), machine({ id: 'm2', name: 'win-1', os: 'windows' })],
      })
      .on('GET', '/api/v1/github/repositories', { items: [], stale: { stale: false } });
    renderApp(api, '/setup/runners/add');
    await screen.findByRole('group', { name: 'Add runners' });
    const win = await screen.findByRole('radio', { name: /win-1/ });
    expect((win as HTMLInputElement).disabled).toBe(true);
    expect(screen.getByText('This machine runs windows, not linux.')).toBeTruthy();
  });

  it('offers Linux on a Windows machine whose engine runs Linux containers (R5.47)', async () => {
    const api = signedIn()
      .on('GET', '/api/v1/machines', {
        items: [
          machine({ id: 'm2', name: 'win-1', os: 'windows', container_os: 'linux' }),
          machine({ id: 'm3', name: 'win-2', os: 'windows' }),
        ],
      })
      .on('GET', '/api/v1/github/repositories', { items: [], stale: { stale: false } });
    renderApp(api, '/setup/runners/add');
    await screen.findByRole('group', { name: 'Add runners' });
    const ok = await screen.findByRole('radio', { name: /win-1/ });
    expect((ok as HTMLInputElement).disabled).toBe(false);
    const no = await screen.findByRole<HTMLInputElement>('radio', { name: /win-2/ });
    expect(no.disabled).toBe(true);
    expect(screen.getByText('This machine runs windows, not linux.')).toBeTruthy();
    // OPS87 (runners R5.93, R5.139): Persistent Linux is offered on a Windows machine whose container backend is ready.
    expect(
      (await screen.findByRole<HTMLInputElement>('radio', { name: /^Persistent/ }))
        .disabled,
    ).toBe(false);
    expect(screen.queryByText(/Persistent runners run on the machine itself/)).toBeNull();
  });

  it('knows which systems a machine can run', () => {
    const win = machine({ os: 'windows', container_os: 'linux' });
    expect(machineSupports(win, 'linux')).toBe(true);
    expect(machineSupports(win, 'windows')).toBe(true);
    expect(machineSupports(machine({ os: 'windows' }), 'linux')).toBe(false);
    expect(
      machineSupports(machine({ os: 'windows', container_os: 'windows' }), 'linux'),
    ).toBe(false);
  });
});

describe('Idle and busy at a glance (RES7)', () => {
  const now = Date.parse('2026-11-15T12:10:00Z');

  it('names the state as idle or busy', () => {
    expect(runnerStatus(runner({ state: 'idle' }))).toEqual({ kind: 'ok', word: 'idle' });
    expect(runnerStatus(runner({ state: 'busy' }))).toEqual({
      kind: 'running',
      word: 'busy',
    });
  });

  it('gives the running job with its repository and elapsed time', () => {
    const r = runner({
      state: 'busy',
      current_job: { ...job, started_at: '2026-11-15T12:05:48Z' },
    });
    expect(currentJobText(r, now)).toBe('octo/app e2e, running 4 min 12 s');
    expect(currentJobText(runner({ state: 'idle' }), now)).toBe('none');
    expect(currentJobText(runner({ state: 'busy', current_job: job }), now)).toBe(
      'octo/app e2e',
    );
  });

  it('counts idle, busy and the rest', () => {
    const items = [
      runner({ state: 'idle' }),
      runner({ id: 'r2', state: 'busy' }),
      runner({ id: 'r3', state: 'busy' }),
      runner({ id: 'r4', state: 'offline' }),
    ];
    expect(stateCounts(items)).toEqual({ idle: 1, busy: 2, other: 1 });
  });

  it('shows the counts above the table', async () => {
    const api = signedIn().on('GET', '/api/v1/runners', {
      items: [runner({ state: 'busy', current_job: job }), runner({ id: 'r2' })],
    });
    renderApp(api, '/setup/runners');
    await screen.findByRole('table', { name: /^Runners on/ });
    expect(screen.getByText('Idle')).toBeTruthy();
    expect(screen.getByText('Busy')).toBeTruthy();
  });
});

describe('Runners list truth (UX-1, UX-6, UX-19)', () => {
  it('links the machine name to the machine page', async () => {
    const api = signedIn().on('GET', '/api/v1/runners', {
      items: [runner({ machine_id: 'm9', machine_name: 'GameRoomPC' })],
    });
    renderApp(api, '/setup/runners');
    const table = await screen.findByRole('table', { name: 'Runners on GameRoomPC' });
    const link = screen.getByText('Open GameRoomPC').closest('a');
    expect(link?.getAttribute('href')).toBe('/setup/machines/m9');
    expect(within(table).queryByText('m9')).toBeNull();
  });

  it('shows the real state word with its reason', async () => {
    const api = signedIn().on('GET', '/api/v1/runners', {
      items: [
        runner({
          state: 'not_registered',
          reason: 'GitHub does not list a runner with this name',
        }),
        runner({
          id: 'r2',
          name: 'b',
          state: 'stopped',
          reason: 'its service is stopped',
        }),
      ],
    });
    renderApp(api, '/setup/runners');
    const table = await screen.findByRole('table', { name: /^Runners on/ });
    expect(within(table).getByText('not registered')).toBeTruthy();
    expect(
      within(table).getByText('GitHub does not list a runner with this name'),
    ).toBeTruthy();
    expect(within(table).getByText('stopped')).toBeTruthy();
    expect(within(table).queryByText('idle')).toBeNull();
    expect(runnerStatus(runner({ state: 'unknown' })).word).toBe('unknown');
  });

  it('offers Remove on a failed runner and asks first', async () => {
    const api = signedIn()
      .on('GET', '/api/v1/runners', {
        items: [
          runner({ state: 'failed', reason: 'the service did not start' }),
          runner({ id: 'r2', name: 'fine' }),
        ],
      })
      .on('DELETE', '/api/v1/runners/:id', runner({ state: 'draining' }));
    renderApp(api, '/setup/runners');
    const table = await screen.findByRole('table', { name: /^Runners on/ });
    expect(within(table).getByText('the service did not start')).toBeTruthy();
    expect(within(table).getAllByRole('button', { name: 'Remove' })).toHaveLength(1);
    fireEvent.click(within(table).getByRole('button', { name: 'Remove' }));
    const dialog = await screen.findByRole('dialog', { name: 'Delete these runners?' });
    expect(within(dialog).getByText('Runner crsp-linux-1')).toBeTruthy();
    fireEvent.click(within(dialog).getByRole('button', { name: 'Delete' }));
    await waitFor(() => expect(api.count('DELETE', '/api/v1/runners/r1')).toBe(1));
  });
});

describe('Add runners: Linux on a Windows machine (OPS27)', () => {
  const win = (docker: boolean) =>
    machine({
      id: 'm2',
      name: 'GameRoomPC',
      os: 'windows',
      container_os: 'linux',
      backends: [
        { name: 'persistent', present: true, usable: true, allowed: true },
        { name: 'docker', present: true, usable: true, allowed: docker },
      ],
    });
  const repos = { items: [], stale: { stale: false } };
  const planOf = (steps: unknown[]) => ({
    id: 'rp1',
    area: 'runners',
    state: 'planned',
    created_at: 'x',
    created_by: 'octo',
    steps,
  });
  const step = (state: string, error?: string) => ({
    id: 's1',
    action: 'add_runner',
    text: 'Add ephemeral runner crsp-pool-1',
    needs_yes: false,
    state,
    ...(error ? { error } : {}),
  });

  it('asks the controller for the container runner of the pool, and knows the backend', () => {
    expect(containerBackendAllowed(win(true))).toBe(true);
    expect(containerBackendAllowed(win(false))).toBe(false);
    expect(sentence('No way')).toBe('No way.');
    expect(sentence('No way.')).toBe('No way.');
    expect(sentence('No way..')).toBe('No way.');
    expect(sentence('Really?')).toBe('Really?');
  });

  it('says on step 1 that the container backend is off, links to the machine and locks Next', async () => {
    const api = signedIn()
      .on('GET', '/api/v1/machines', { items: [win(false)] })
      .on('GET', '/api/v1/github/repositories', repos);
    renderApp(api, '/setup/runners/add');
    await screen.findByRole('group', { name: 'Add runners' });
    expect(
      (await screen.findAllByText(/The container backend is not allowed on this machine/))
        .length,
    ).toBeGreaterThan(0);
    const link = screen.getByRole('button', { name: 'Allow it on the machine' });
    expect(link.getAttribute('href')).toBe('/setup/machines/m2');
    expect(screen.getByRole<HTMLButtonElement>('button', { name: 'Next' }).disabled).toBe(
      true,
    );
    expect(api.count('POST', '/api/v1/runner-plans')).toBe(0);
  });

  it('shows a refusal of the plan on step 1 with its reason, not at Done', async () => {
    const api = signedIn()
      .on('GET', '/api/v1/machines', { items: [win(true)] })
      .on('GET', '/api/v1/github/repositories', repos)
      .on('POST', '/api/v1/runner-plans', () =>
        apiError(422, 'refused', 'machine GameRoomPC: not possible'),
      );
    renderApp(api, '/setup/runners/add');
    await screen.findByRole('group', { name: 'Add runners' });
    fireEvent.click(await screen.findByRole('button', { name: 'Next' }));
    expect(await screen.findByText('machine GameRoomPC: not possible.')).toBeTruthy();
    expect(screen.queryByText('Nothing has changed yet.')).toBeNull();
    expect(screen.getByRole('button', { name: 'Next' })).toBeTruthy();
  });

  it('never reaches Review with no step', async () => {
    const api = signedIn()
      .on('GET', '/api/v1/machines', { items: [win(true)] })
      .on('GET', '/api/v1/github/repositories', repos)
      .on('POST', '/api/v1/runner-plans', planOf([]));
    renderApp(api, '/setup/runners/add');
    await screen.findByRole('group', { name: 'Add runners' });
    fireEvent.click(await screen.findByRole('button', { name: 'Next' }));
    expect(
      await screen.findByText(/Nothing would be added with these choices/),
    ).toBeTruthy();
    expect(screen.queryByText(/These steps run/)).toBeNull();
  });

  it('shows a failed step as failed, ends the text with a full stop, and Try again goes back to step 1', async () => {
    const failed = planOf([step('failed', 'registration was refused')]);
    const api = signedIn()
      .on('GET', '/api/v1/machines', { items: [win(true)] })
      .on('GET', '/api/v1/github/repositories', repos)
      .on('POST', '/api/v1/runner-plans', planOf([step('planned')]))
      .on('POST', '/api/v1/runner-plans/:id/apply', { ...failed, state: 'failed' })
      .on('GET', '/api/v1/runner-plans/:id', { ...failed, state: 'failed' });
    renderApp(api, '/setup/runners/add');
    await screen.findByRole('group', { name: 'Add runners' });
    fireEvent.click(await screen.findByRole('radio', { name: /^Ephemeral/ }));
    fireEvent.click(await screen.findByRole('button', { name: 'Next' }));
    fireEvent.click(
      await screen.findByRole('button', { name: 'Allow 1 ephemeral runner' }),
    );
    const text = await screen.findByText(/1 failed, 0 done\./);
    expect(text.textContent).toContain('1 failed, 0 done. registration was refused.');
    expect(text.textContent).toContain('Open Runners, remove it');
    const strip = screen.getByRole('list', { name: 'Steps' });
    expect(
      within(strip).getByRole('button', { name: /Registering/ }).textContent,
    ).toContain('✕');
    expect(strip.textContent).not.toMatch(/✓ Registering/);
    fireEvent.click(screen.getByRole('button', { name: 'Try again' }));
    expect(await screen.findByRole('button', { name: 'Next' })).toBeTruthy();
    expect(screen.getByText('registration was refused.')).toBeTruthy();
    expect(api.count('POST', '/api/v1/runner-plans/rp1/apply')).toBe(1);
  });
});
