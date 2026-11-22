// R5.22: the machines screen lists machines, edits budget and slots, drains, resumes and removes.
import { cleanup, fireEvent, screen, waitFor, within } from '@testing-library/react';
import { afterEach, describe, expect, it } from 'vitest';
import { apiError, machine, renderApp, signedIn, viewer } from '../../testing/mock';
import { currentChoice } from './PoolSize';

afterEach(cleanup);

const running = [
  {
    id: 9,
    repository: 'octo/app',
    run_id: 1,
    run_attempt: 1,
    github_job_id: 1,
    job_name: 'e2e',
    labels: [],
    state: 'running' as const,
  },
];

describe('Machines list', () => {
  it('lists machines with state word, backends, load and slots', async () => {
    const api = signedIn().on('GET', '/api/v1/machines', {
      items: [
        machine(),
        machine({ id: 'm2', name: 'win-1', os: 'windows', online: false }),
      ],
    });
    renderApp(api, '/pool/machines');
    const table = await screen.findByRole('table', { name: 'Machines' });
    expect(within(table).getByText('online')).toBeTruthy();
    expect(within(table).getByText('offline')).toBeTruthy();
    expect(within(table).getAllByText('1 of 4').length).toBe(2);
    expect(within(table).getAllByText('docker').length).toBe(2);
    expect(within(table).getAllByText(/memory 40%, CPU 20%/).length).toBe(2);
  });

  it('says "Windows, runs Linux containers" for a Windows machine with Linux containers', async () => {
    const api = signedIn().on('GET', '/api/v1/machines', {
      items: [machine({ id: 'm2', name: 'win-1', os: 'windows', container_os: 'linux' })],
    });
    renderApp(api, '/pool/machines');
    const table = await screen.findByRole('table', { name: 'Machines' });
    expect(within(table).getByText('Windows, runs Linux containers')).toBeTruthy();
  });

  it('searches and filters, and says how many are hidden', async () => {
    const api = signedIn().on('GET', '/api/v1/machines', {
      items: [machine(), machine({ id: 'm2', name: 'win-1', state: 'drained' })],
    });
    renderApp(api, '/pool/machines');
    await screen.findByRole('table', { name: 'Machines' });
    fireEvent.change(screen.getByRole('textbox', { name: 'Search machines' }), {
      target: { value: 'win' },
    });
    expect(screen.queryByRole('link', { name: 'build-1' })).toBeNull();
    expect(screen.getByRole('link', { name: 'win-1' })).toBeTruthy();
    fireEvent.change(screen.getByRole('textbox', { name: 'Search machines' }), {
      target: { value: 'zzz' },
    });
    expect(screen.getByText(/2 machines are hidden/)).toBeTruthy();
    fireEvent.click(screen.getByRole('button', { name: 'Clear search and filter' }));
    expect(
      screen.getByRole<HTMLInputElement>('textbox', { name: 'Search machines' }).value,
    ).toBe('');
  });

  it('says what to do when there is none, and offers Add a machine to an admin', async () => {
    renderApp(signedIn().on('GET', '/api/v1/machines', { items: [] }), '/pool/machines');
    expect(
      await screen.findByText('No machines yet. Use Add a machine above to start.'),
    ).toBeTruthy();
    // The next action is written once (UX-1): the header's one primary.
    expect(screen.getAllByRole('button', { name: 'Add a machine' }).length).toBe(1);
  });

  it('shows Add a machine disabled with its reason to a viewer (UX-38)', async () => {
    renderApp(
      signedIn(viewer).on('GET', '/api/v1/machines', { items: [machine()] }),
      '/pool/machines',
    );
    const button = await screen.findByRole<HTMLButtonElement>('button', {
      name: 'Add a machine',
    });
    expect(button.disabled).toBe(true);
    expect(screen.getByText('Only an admin can do this.')).toBeTruthy();
  });

  it('puts the search, filters and table inside one pane titled Machines', async () => {
    const api = signedIn().on('GET', '/api/v1/machines', { items: [machine()] });
    renderApp(api, '/pool/machines');
    const pane = await screen.findByRole('region', { name: 'Machines' });
    expect(within(pane).getByRole('textbox', { name: 'Search machines' })).toBeTruthy();
    expect(within(pane).getByRole('table', { name: 'Machines' })).toBeTruthy();
  });

  it('says could not load, why and what next', async () => {
    renderApp(
      signedIn().on('GET', '/api/v1/machines', () =>
        apiError(503, 'unavailable', 'The database is busy.'),
      ),
      '/pool/machines',
    );
    expect(await screen.findByText(/Could not load machines\./)).toBeTruthy();
    expect(screen.getByText(/The database is busy\. Try again\./)).toBeTruthy();
    expect(screen.getByRole('button', { name: 'Try again' })).toBeTruthy();
  });
});

describe('Machine detail', () => {
  const base = () =>
    signedIn().on('GET', '/api/v1/machines/:id', machine({ running_jobs: running }));

  it('shows facts, backends and the budget', async () => {
    renderApp(base(), '/pool/machines/m1');
    expect(
      await screen.findByRole('heading', { level: 1, name: 'build-1' }),
    ).toBeTruthy();
    expect(screen.getByRole('checkbox', { name: /docker/ })).toBeTruthy();
    expect(screen.getByRole('button', { name: /Memory budget/ }).textContent).toContain(
      '80%',
    );
  });

  it('writes the reason a backend cannot be used once, not twice', async () => {
    const why = 'WSL is installed but not ready; run wsl --install';
    const api = signedIn().on(
      'GET',
      '/api/v1/machines/:id',
      machine({
        backends: [
          { name: 'wsl2', present: true, usable: false, allowed: false, reason: why },
        ],
      }),
    );
    renderApp(api, '/pool/machines/m1');
    await screen.findByRole('heading', { level: 1, name: 'build-1' });
    expect(await screen.findAllByText(why)).toHaveLength(1);
  });

  it('asks before a drain, names the running jobs, and drains on yes (UX-62)', async () => {
    const api = base().on('POST', '/api/v1/machines/:id/drain', {
      id: 'c1',
      machine_id: 'm1',
      kind: 'drain',
      state: 'waiting',
      started_at: 'x',
      waiting_for: [],
    });
    renderApp(api, '/pool/machines/m1');
    fireEvent.click(await screen.findByRole('button', { name: 'Stop new jobs' }));
    const dialog = await screen.findByRole('dialog', {
      name: 'Stop new jobs on build-1?',
    });
    expect(within(dialog).getByText('octo/app e2e')).toBeTruthy();
    expect(api.count('POST', '/api/v1/machines/m1/drain')).toBe(0);
    fireEvent.click(within(dialog).getByRole('button', { name: 'Stop new jobs' }));
    await waitFor(() => expect(api.count('POST', '/api/v1/machines/m1/drain')).toBe(1));
  });

  it('does nothing when the question is cancelled', async () => {
    const api = base();
    renderApp(api, '/pool/machines/m1');
    fireEvent.click(await screen.findByRole('button', { name: 'Stop new jobs' }));
    const dialog = await screen.findByRole('dialog');
    fireEvent.click(within(dialog).getByRole('button', { name: 'Cancel' }));
    await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull());
    expect(api.count('POST', '/api/v1/machines/m1/drain')).toBe(0);
  });

  it('removes crsp from a machine only after Keep my data or Delete my data too is chosen', async () => {
    const api = base()
      .on('POST', '/api/v1/remove', {})
      .on('GET', '/api/v1/machines', { items: [] });
    renderApp(api, '/pool/machines/m1');
    fireEvent.click(await screen.findByRole('button', { name: 'Remove crsp' }));
    const dialog = await screen.findByRole('dialog', { name: 'Remove crsp' });
    const confirm = within(dialog).getByRole('button', { name: 'Remove crsp' });
    expect(confirm.hasAttribute('disabled')).toBe(true);
    fireEvent.click(within(dialog).getByRole('radio', { name: /Delete my data too/ }));
    fireEvent.click(confirm);
    await waitFor(() => expect(api.count('POST', '/api/v1/remove')).toBe(1));
    expect(api.calls.find((c) => c.path === '/api/v1/remove')?.body).toEqual({
      machine_id: 'm1',
      delete_data: true,
    });
  });

  it('shows Resume for a drained machine and resumes without a question', async () => {
    const api = signedIn()
      .on('GET', '/api/v1/machines/:id', machine({ state: 'drained' }))
      .on('POST', '/api/v1/machines/:id/resume', machine());
    renderApp(api, '/pool/machines/m1');
    fireEvent.click(await screen.findByRole('button', { name: 'Resume' }));
    await waitFor(() => expect(api.count('POST', '/api/v1/machines/m1/resume')).toBe(1));
    expect(screen.queryByRole('dialog')).toBeNull();
  });

  it('saves the budget, and asks first when runners restart', async () => {
    let first = true;
    const api = base().on('PATCH', '/api/v1/machines/:id', () => {
      if (first) {
        first = false;
        return apiError(409, 'needs_confirmation', 'Runners restart.', [
          { kind: 'job', id: '9', text: 'octo/app e2e' },
        ]);
      }
      return machine();
    });
    renderApp(api, '/pool/machines/m1');
    await screen.findByRole('heading', { name: 'build-1' });
    fireEvent.click(screen.getAllByRole('checkbox')[0]!);
    fireEvent.click(screen.getByRole('button', { name: 'Save' }));
    const dialog = await screen.findByRole('dialog', { name: 'Save these settings?' });
    expect(within(dialog).getByText('octo/app e2e')).toBeTruthy();
    fireEvent.click(within(dialog).getByRole('button', { name: 'Save' }));
    await waitFor(() => expect(api.count('PATCH', '/api/v1/machines/m1')).toBe(2));
    const last = api.calls.filter((c) => c.method === 'PATCH').pop()?.body as {
      confirm?: boolean;
      budget_memory_pct: number;
    };
    expect(last.confirm).toBe(true);
    expect(last.budget_memory_pct).toBe(80);
  });

  it('shows the viewer the form with Save disabled and why', async () => {
    renderApp(
      signedIn(viewer).on('GET', '/api/v1/machines/:id', machine()),
      '/pool/machines/m1',
    );
    await screen.findByRole('heading', { name: 'build-1' });
    expect(screen.getByRole<HTMLButtonElement>('button', { name: 'Save' }).disabled).toBe(
      true,
    );
    expect(
      screen.getByRole<HTMLButtonElement>('button', { name: 'Stop new jobs' }).disabled,
    ).toBe(true);
  });
});

describe('Machine detail, one page', () => {
  const base = () =>
    signedIn().on('GET', '/api/v1/machines/:id', machine({ running_jobs: running }));
  it('has one primary (Save; Stop new jobs is secondary), one foot with Cancel and Save, and Remove in its pane', async () => {
    const api = base();
    renderApp(api, '/pool/machines/m1');
    await screen.findByRole('heading', { level: 1, name: 'build-1' });
    const primaries = Array.from(document.querySelectorAll('button[data-primary]')).map(
      (b) => b.textContent,
    );
    expect(primaries).not.toContain('Stop new jobs');
    expect(
      screen.getByText('Running jobs finish first. Resume takes jobs again.'),
    ).toBeTruthy();
    expect(primaries).toEqual(['Save']);
    expect(screen.getAllByRole('button', { name: 'Save' }).length).toBe(1);
    expect(screen.getAllByRole('button', { name: 'Cancel' }).length).toBe(1);
    const remove = screen.getByRole('region', { name: 'Remove crsp' });
    expect(within(remove).getByRole('button', { name: 'Remove crsp' })).toBeTruthy();
  });

  it('keeps the h1 when the machine could not load', async () => {
    const api = signedIn().on('GET', '/api/v1/machines/:id', () =>
      apiError(500, 'internal', 'The controller could not answer.'),
    );
    renderApp(api, '/pool/machines/m1');
    expect(
      await screen.findByRole('heading', { level: 1, name: 'Machine' }),
    ).toBeTruthy();
    expect(await screen.findByText(/Could not load the machine/)).toBeTruthy();
  });
});

describe('Machine detail, a drain that waits', () => {
  it('names the job that holds the drain, how long it runs, and what to do when it is far past its usual time', async () => {
    const job = {
      id: 9,
      github_job_id: 9,
      job_name: 'build',
      repository: 'octo/app',
      run_id: 7,
      run_attempt: 1,
      labels: [],
      state: 'running' as const,
      runner_name: 'runner-1',
    };
    const api = signedIn().on(
      'GET',
      '/api/v1/machines/:id',
      machine({
        state: 'draining',
        pending_change: {
          id: 'c1',
          machine_id: 'm1',
          kind: 'drain',
          state: 'waiting',
          started_at: 'x',
          waiting_for: [job],
          holder: { job, running_seconds: 2700, usual_seconds: 600, over_usual: true },
          choices: ['wait_longer', 'cancel'],
        },
      }),
    );
    renderApp(api, '/pool/machines/m1');
    expect(await screen.findByText('octo/app build on runner-1')).toBeTruthy();
    expect(screen.getByText('45 min')).toBeTruthy();
    expect(screen.getByText('10 min')).toBeTruthy();
    expect(screen.getByText(/never stops a job/)).toBeTruthy();
  });
});

// machines R5.58, LOOK22: the pool size is a plain section with a choice of three, a live check line and the page's one Save.
describe('Machine detail, the pool size', () => {
  const fits = {
    known: true,
    ok: true,
    needs_shrink_confirm: false,
    nearest: [],
    total_gib: 32,
    used_outside_gib: 8,
    memory_budget_gib: 27.2,
    room_gib: 19.2,
    cpu_budget: 4,
    decided: 'fits',
    current_memory_gib: 0,
    current_cpus: 0,
    recommended_half: { memory_gib: 9, cpus: 2 },
    recommended_three_quarters: { memory_gib: 14, cpus: 3 },
  };

  /** Type a value on the number keypad of a field (the first key replaces the value). */
  async function typeNumber(field: RegExp, keys: string[]) {
    fireEvent.click(await screen.findByRole('button', { name: field }));
    const dialog = await screen.findByRole('dialog');
    for (const k of keys)
      fireEvent.click(within(dialog).getByRole('button', { name: k }));
    fireEvent.click(within(dialog).getByRole('button', { name: 'Done' }));
    await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull());
  }

  const api = (
    check: Record<string, unknown> | ((c: { body?: unknown }) => unknown),
    m = machine(),
  ) =>
    signedIn()
      .on('GET', '/api/v1/machines/:id', m)
      .on('POST', '/api/v1/machines/:id/pool-size-check', check)
      .on('PATCH', '/api/v1/machines/:id', m);

  const saveButton = () =>
    screen.getByRole<HTMLButtonElement>('button', { name: 'Save' });
  const choose = async (name: RegExp) => {
    // The row is drawn again once the room is known, so ask for the enabled one.
    await waitFor(() =>
      expect(screen.getByRole<HTMLInputElement>('radio', { name }).disabled).toBe(false),
    );
    fireEvent.click(screen.getByRole('radio', { name }));
  };

  it('is a section, not a wizard: facts, three choices with their words, no step row', async () => {
    renderApp(api(fits), '/pool/machines/m1');
    expect(await screen.findByText('Room for the pool')).toBeTruthy();
    expect(screen.getAllByText('CPU budget').length).toBeGreaterThan(0);
    expect(screen.getByText('not set')).toBeTruthy();
    await screen.findByRole('radio', {
      name: /Recommended: half of the free room \(9 GiB, 2 CPUs\)/,
    });
    expect(
      screen.getByRole('radio', {
        name: /Recommended: three quarters of the free room \(14 GiB, 3 CPUs\)/,
      }),
    ).toBeTruthy();
    expect(screen.getByRole('radio', { name: /Custom/ })).toBeTruthy();
    // Each recommended label says how it was computed.
    expect(
      screen.getByText(/Half of the 19.2 GiB free for the pool, rounded down/),
    ).toBeTruthy();
    expect(
      screen.getByText(/Three quarters of the 19.2 GiB free for the pool/),
    ).toBeTruthy();
    expect(screen.queryByRole('button', { name: 'Review' })).toBeNull();
    // No Save of its own: the page has the one Save, disabled until a change.
    expect(screen.getAllByRole('button', { name: /^Save/ })).toHaveLength(1);
    expect(saveButton().disabled).toBe(true);
  });

  it('a recommended choice fills the size, runs the live check and saves with the page Save', async () => {
    const a = api(fits);
    renderApp(a, '/pool/machines/m1');
    await choose(/half of the free room/);
    expect(
      await screen.findByText(/This size fits the budget of the machine/),
    ).toBeTruthy();
    expect(screen.getByText('Unsaved changes (1)')).toBeTruthy();
    expect(a.count('PATCH', '/api/v1/machines/m1')).toBe(0);
    await waitFor(() => expect(saveButton().disabled).toBe(false));
    fireEvent.click(saveButton());
    await screen.findByText('Settings saved.');
    // One Save sends the size; nothing else changed, so no second request.
    const patches = a.calls.filter((c) => c.method === 'PATCH');
    expect(patches).toHaveLength(1);
    expect(patches[0]?.body).toEqual({ pool_memory_gib: 9, pool_cpus: 2 });
  });

  it('the three quarters choice sends its own size', async () => {
    const a = api(fits);
    renderApp(a, '/pool/machines/m1');
    await choose(/three quarters/);
    await screen.findByText(/This size fits the budget of the machine/);
    await waitFor(() => expect(saveButton().disabled).toBe(false));
    fireEvent.click(saveButton());
    await screen.findByText('Settings saved.');
    expect(a.calls.find((c) => c.method === 'PATCH')?.body).toEqual({
      pool_memory_gib: 14,
      pool_cpus: 3,
    });
  });

  it('Custom shows the memory and CPU fields, marks them and checks 250 ms after the last edit', async () => {
    const a = api(fits);
    renderApp(a, '/pool/machines/m1');
    await choose(/Custom/);
    const before = a.count('POST', '/api/v1/machines/m1/pool-size-check');
    await typeNumber(/Pool memory/, ['1', '2', '.', '5']);
    await typeNumber(/Pool CPUs/, ['4']);
    // The check does not run per key: one call after the last edit.
    expect(a.count('POST', '/api/v1/machines/m1/pool-size-check')).toBe(before);
    expect(
      await screen.findByText(/This size fits the budget of the machine/),
    ).toBeTruthy();
    expect(a.count('POST', '/api/v1/machines/m1/pool-size-check')).toBe(before + 1);
    expect(document.querySelectorAll('[data-changed]').length).toBeGreaterThanOrEqual(3);
    await waitFor(() => expect(saveButton().disabled).toBe(false));
    fireEvent.click(saveButton());
    await screen.findByText('Settings saved.');
    expect(a.calls.find((c) => c.method === 'PATCH')?.body).toEqual({
      pool_memory_gib: 12.5,
      pool_cpus: 4,
    });
  });

  it('a size that leaves little for the PC is a warning, still savable', async () => {
    renderApp(api({ ...fits, room_gib: 18 }), '/pool/machines/m1');
    await choose(/Custom/);
    await typeNumber(/Pool memory/, ['1', '6']);
    expect(await screen.findByText(/fits, but leaves 2 GiB for this PC/)).toBeTruthy();
    await waitFor(() => expect(saveButton().disabled).toBe(false));
  });

  it('too much: names the room, offers the size that fits, and does not save', async () => {
    const a = api((c: { body?: unknown }) =>
      (c.body as { pool_memory_gib: number }).pool_memory_gib > 19
        ? {
            ...fits,
            ok: false,
            reason: '30 GiB is above the memory room',
            short: 'memory',
            nearest: ['19 GiB fits 4 CPUs'],
          }
        : fits,
    );
    renderApp(a, '/pool/machines/m1');
    await choose(/Custom/);
    await typeNumber(/Pool memory/, ['3', '0']);
    expect(
      await screen.findByText(
        /30 GiB is too much: only 19.2 GiB is free for the pool right now \(other programs use 8 GiB\)\. Use 19 GiB and 4 CPUs\?/,
      ),
    ).toBeTruthy();
    expect(
      screen.getByText('The room moves with what else runs on this PC.'),
    ).toBeTruthy();
    expect(
      screen.getByText('This size does not fit. Change the pool size.'),
    ).toBeTruthy();
    expect(saveButton().disabled).toBe(true);
    expect(a.count('PATCH', '/api/v1/machines/m1')).toBe(0);
    fireEvent.click(screen.getByRole('button', { name: 'Use 19 GiB and 4 CPUs' }));
    // 19 GiB of 19.2 fits, but leaves almost nothing for the PC: a warning, and it can be saved.
    expect(
      await screen.findByText(/This size fits, but leaves 0.2 GiB for this PC/),
    ).toBeTruthy();
    expect(a.count('PATCH', '/api/v1/machines/m1')).toBe(0);
  });

  it('needs its own yes to lower the size and sends confirm_shrink (R5.58)', async () => {
    const m = machine({
      settings: { ...machine().settings, pool_memory_gib: 16, pool_cpus: 4 },
    });
    const a = api(
      { ...fits, needs_shrink_confirm: true, current_memory_gib: 16, current_cpus: 4 },
      m,
    );
    renderApp(a, '/pool/machines/m1');
    await choose(/Custom/);
    await typeNumber(/Pool memory/, ['8']);
    expect(await screen.findByText(/This lowers the pool size/)).toBeTruthy();
    await waitFor(() =>
      expect(
        screen.getByText('Tick the box to confirm lowering the pool size.'),
      ).toBeTruthy(),
    );
    expect(saveButton().disabled).toBe(true);
    fireEvent.click(screen.getByRole('checkbox', { name: 'Yes, lower the pool size' }));
    await waitFor(() => expect(saveButton().disabled).toBe(false));
    fireEvent.click(saveButton());
    await screen.findByText('Settings saved.');
    expect(a.calls.find((c) => c.method === 'PATCH')?.body).toEqual({
      pool_memory_gib: 8,
      pool_cpus: 4,
      confirm_shrink: true,
    });
  });

  it('stops without saving when the size changed while it was edited', async () => {
    let n = 0;
    const a = signedIn()
      .on('GET', '/api/v1/machines/:id', machine())
      .on('POST', '/api/v1/machines/:id/pool-size-check', () =>
        ++n <= 2 ? fits : { ...fits, current_memory_gib: 12, current_cpus: 3 },
      )
      .on('PATCH', '/api/v1/machines/:id', machine());
    renderApp(a, '/pool/machines/m1');
    await choose(/half of the free room/);
    await screen.findByText(/This size fits the budget of the machine/);
    await waitFor(() => expect(saveButton().disabled).toBe(false));
    fireEvent.click(saveButton());
    expect(
      await screen.findByText(/changed to 12 GiB and 3 CPUs while you edited/),
    ).toBeTruthy();
    expect(a.count('PATCH', '/api/v1/machines/m1')).toBe(0);
  });

  it('Cancel puts the choice back to what is saved', async () => {
    renderApp(api(fits), '/pool/machines/m1');
    await choose(/half of the free room/);
    fireEvent.click(await screen.findByRole('button', { name: 'Cancel' }));
    await waitFor(() => expect(screen.queryByText('Unsaved changes (1)')).toBeNull());
    expect(
      screen.getByRole<HTMLInputElement>('radio', { name: /half of the free room/ })
        .checked,
    ).toBe(false);
  });

  const sized = (mem: number, cpus: number) =>
    machine({
      settings: { ...machine().settings, pool_memory_gib: mem, pool_cpus: cpus },
    });
  const radio = (name: RegExp) => screen.getByRole<HTMLInputElement>('radio', { name });

  it('preselects half of the free room when it is the size now (OPS47)', async () => {
    renderApp(
      api({ ...fits, current_memory_gib: 9, current_cpus: 2 }, sized(9, 2)),
      '/pool/machines/m1',
    );
    await waitFor(() => expect(radio(/half of the free room/).checked).toBe(true));
    expect(radio(/three quarters/).checked).toBe(false);
    expect(radio(/Custom/).checked).toBe(false);
    expect(screen.queryByText('Unsaved changes (1)')).toBeNull();
    expect(saveButton().disabled).toBe(true);
  });

  it('preselects three quarters when it is the size now (OPS47)', async () => {
    renderApp(
      api({ ...fits, current_memory_gib: 14, current_cpus: 3 }, sized(14, 3)),
      '/pool/machines/m1',
    );
    await waitFor(() => expect(radio(/three quarters/).checked).toBe(true));
    expect(radio(/half of the free room/).checked).toBe(false);
  });

  it('preselects Custom with the current values when no recommended size matches (OPS47)', async () => {
    renderApp(
      api({ ...fits, current_memory_gib: 12, current_cpus: 3 }, sized(12, 3)),
      '/pool/machines/m1',
    );
    await waitFor(() => expect(radio(/Custom/).checked).toBe(true));
    expect(radio(/half of the free room/).checked).toBe(false);
    expect(radio(/three quarters/).checked).toBe(false);
    expect(screen.getByRole('button', { name: /Pool memory/ }).textContent).toContain(
      '12',
    );
    expect(screen.getByRole('button', { name: /Pool CPUs/ }).textContent).toContain('3');
    expect(saveButton().disabled).toBe(true);
  });

  it('currentChoice: none when no size is set, Custom when the room is unknown', () => {
    expect(currentChoice(fits, 0, 0)).toBeUndefined();
    expect(currentChoice(fits, 9, 2)).toBe('half');
    expect(currentChoice(fits, 14, 3)).toBe('three');
    expect(currentChoice(undefined, 9, 2)).toBe('custom');
    expect(currentChoice({ ...fits, known: false }, 9, 2)).toBe('custom');
  });

  it('lets a viewer read the section but not change it (UX-38)', async () => {
    renderApp(
      signedIn(viewer)
        .on('GET', '/api/v1/machines/:id', machine())
        .on('POST', '/api/v1/machines/:id/pool-size-check', fits),
      '/pool/machines/m1',
    );
    await screen.findByText('Room for the pool');
    expect(saveButton().disabled).toBe(true);
  });
});
