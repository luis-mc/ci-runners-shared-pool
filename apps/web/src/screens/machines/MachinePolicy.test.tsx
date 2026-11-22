// R5.61 to R5.64: the Machines screen shows and saves the mode, the battery pause and the CPU cap, and says why a machine takes no job.
import { cleanup, fireEvent, screen, waitFor, within } from '@testing-library/react';
import { afterEach, describe, expect, it } from 'vitest';
import { machine, renderApp, signedIn, viewer } from '../../testing/mock';

afterEach(cleanup);

const withSettings = (over: Record<string, unknown>, rest = {}) => {
  const m = machine(rest);
  return { ...m, settings: { ...m.settings, ...over } };
};

const detail = (m = machine()) =>
  signedIn().on('GET', '/api/v1/machines/:id', m).on('PATCH', '/api/v1/machines/:id', m);

const lastPatch = (api: ReturnType<typeof detail>) =>
  api.calls.filter((c) => c.method === 'PATCH').pop()?.body as Record<string, unknown>;

describe('Machine policy section', () => {
  it('shows the mode, the battery switch and the CPU cap of the machine', async () => {
    renderApp(
      detail(withSettings({ mode: 'always', pause_on_battery: false, cpu_cap_pct: 50 })),
      '/setup/machines/m1',
    );
    await screen.findByRole('heading', { name: 'build-1' });
    expect(screen.getByRole<HTMLInputElement>('radio', { name: /Always/ }).checked).toBe(
      true,
    );
    expect(screen.getByRole('switch', { name: 'Pause on battery' }).ariaChecked).toBe(
      'false',
    );
    expect(screen.getByRole<HTMLInputElement>('radio', { name: '50%' }).checked).toBe(
      true,
    );
    expect(screen.getByText('Always ignores the battery.')).toBeTruthy();
  });

  it('saves a mode at once, without Save', async () => {
    const api = detail();
    renderApp(api, '/setup/machines/m1');
    await screen.findByRole('heading', { name: 'build-1' });
    fireEvent.click(screen.getByRole('radio', { name: /Paused/ }));
    await waitFor(() => expect(api.count('PATCH', '/api/v1/machines/m1')).toBe(1));
    expect(lastPatch(api)).toEqual({ mode: 'paused' });
  });

  it('saves the battery switch and the CPU cap at once', async () => {
    const api = detail();
    renderApp(api, '/setup/machines/m1');
    await screen.findByRole('heading', { name: 'build-1' });
    fireEvent.click(screen.getByRole('switch', { name: 'Pause on battery' }));
    await waitFor(() => expect(api.count('PATCH', '/api/v1/machines/m1')).toBe(1));
    expect(lastPatch(api)).toEqual({ pause_on_battery: false });
    fireEvent.click(screen.getByRole('radio', { name: '25%' }));
    await waitFor(() => expect(api.count('PATCH', '/api/v1/machines/m1')).toBe(2));
    expect(lastPatch(api)).toEqual({ cpu_cap_pct: 25 });
  });

  it('says why a machine takes no new job', async () => {
    renderApp(
      detail(
        machine({
          accepting: false,
          pause_reason: 'on_battery',
          taking: { taking_jobs: false, reason: 'on_battery' },
          policy: { on_battery: true, battery_known: true },
        }),
      ),
      '/setup/machines/m1',
    );
    await screen.findByRole('heading', { name: 'build-1' });
    expect(screen.getAllByText('On battery').length).toBeGreaterThan(0);
    expect(screen.getByText(/takes no new job while it runs on battery/)).toBeTruthy();
  });

  it('names the pause from the tray and Resume ends it', async () => {
    const api = detail(
      withSettings(
        { pause_until: '2026-11-15T13:00:00Z' },
        {
          accepting: false,
          pause_reason: 'paused',
          taking: { taking_jobs: false, reason: 'tray_paused' },
        },
      ),
    );
    renderApp(api, '/setup/machines/m1');
    await screen.findByRole('heading', { name: 'build-1' });
    expect(screen.getByText('Paused until')).toBeTruthy();
    fireEvent.click(screen.getByRole('button', { name: 'Resume' }));
    await waitFor(() => expect(api.count('PATCH', '/api/v1/machines/m1')).toBe(1));
    expect(lastPatch(api)).toEqual({ pause: 'none' });
  });

  it('Resume also returns the Paused mode to Automatic', async () => {
    const api = detail(
      withSettings(
        { mode: 'paused' },
        {
          accepting: false,
          pause_reason: 'paused',
          taking: { taking_jobs: false, reason: 'mode_paused' },
        },
      ),
    );
    renderApp(api, '/setup/machines/m1');
    await screen.findByRole('heading', { name: 'build-1' });
    fireEvent.click(screen.getByRole('button', { name: 'Resume' }));
    await waitFor(() => expect(api.count('PATCH', '/api/v1/machines/m1')).toBe(1));
    expect(lastPatch(api)).toEqual({ pause: 'none', mode: 'automatic' });
  });

  it('says the CPU cap is not supported on macOS and offers no value', async () => {
    renderApp(
      detail(
        machine({
          os: 'macos',
          policy: {
            on_battery: false,
            battery_known: false,
            cpu_cap_state: 'not_supported',
          },
        }),
      ),
      '/setup/machines/m1',
    );
    await screen.findByRole('heading', { name: 'build-1' });
    expect(screen.getAllByText(/Not supported on macOS yet/).length).toBeGreaterThan(0);
    expect(screen.getByRole<HTMLInputElement>('radio', { name: /^50%/ }).disabled).toBe(
      true,
    );
  });

  it('follows the chosen cap: 100 fails, 50 waits, then settles', async () => {
    const pol = (state: string, detail?: string) => ({
      on_battery: false,
      battery_known: true,
      cpu_cap_state: state,
      ...(detail ? { cpu_cap_detail: detail } : {}),
    });
    const failed = withSettings(
      { cpu_cap_pct: 100 },
      { policy: pol('failed', 'access denied') },
    );
    const api = detail(failed);
    renderApp(api, '/machines/m1');
    expect(
      await screen.findByText(/The CPU cap was not set\. access denied/),
    ).toBeTruthy();
    // The controller answers applying (no old error) until the agent reports 50.
    const applying = withSettings({ cpu_cap_pct: 50 }, { policy: pol('applying') });
    api
      .on('GET', '/api/v1/machines/:id', applying)
      .on('PATCH', '/api/v1/machines/:id', applying);
    fireEvent.click(screen.getByRole('radio', { name: '50%' }));
    expect(await screen.findByText('Setting the CPU cap.')).toBeTruthy();
    expect(screen.queryByText(/The CPU cap was not set/)).toBeNull();
    // The page reads again by itself and shows the result for 50.
    api.on(
      'GET',
      '/api/v1/machines/:id',
      withSettings({ cpu_cap_pct: 50 }, { policy: pol('applied') }),
    );
    await waitFor(() => expect(screen.queryByText('Setting the CPU cap.')).toBeNull(), {
      timeout: 5000,
    });
    expect(screen.queryByText(/The CPU cap was not set/)).toBeNull();
  });

  it('shows a cap that failed and why', async () => {
    renderApp(
      detail(
        machine({
          policy: {
            on_battery: false,
            battery_known: true,
            cpu_cap_state: 'failed',
            cpu_cap_detail: '1 of 2 runner services could not be capped',
          },
        }),
      ),
      '/setup/machines/m1',
    );
    expect(
      await screen.findByText(/The CPU cap was not set\. 1 of 2 runner services/),
    ).toBeTruthy();
  });

  it('shows a viewer the values and the reason the controls are off (UX-38)', async () => {
    renderApp(
      signedIn(viewer).on('GET', '/api/v1/machines/:id', machine()),
      '/setup/machines/m1',
    );
    await screen.findByRole('heading', { name: 'build-1' });
    expect(
      screen.getByRole<HTMLButtonElement>('switch', { name: 'Pause on battery' })
        .disabled,
    ).toBe(true);
    expect(screen.getByRole<HTMLInputElement>('radio', { name: /Paused/ }).disabled).toBe(
      true,
    );
  });
});

describe('Machines list: takes jobs', () => {
  it('says taking jobs, or why not, in a word (UX-58)', async () => {
    const api = signedIn().on('GET', '/api/v1/machines', {
      items: [
        machine(),
        machine({
          id: 'm2',
          name: 'laptop',
          accepting: false,
          pause_reason: 'on_battery',
          taking: { taking_jobs: false, reason: 'on_battery' },
        }),
        machine({
          id: 'm3',
          name: 'desk',
          accepting: false,
          pause_reason: 'paused',
          taking: { taking_jobs: false, reason: 'mode_paused' },
        }),
      ],
    });
    renderApp(api, '/setup/machines');
    const table = await screen.findByRole('table', { name: 'Machines' });
    expect(within(table).getByText('Taking jobs')).toBeTruthy();
    expect(within(table).getByText('On battery')).toBeTruthy();
    expect(within(table).getByText('Paused')).toBeTruthy();
  });
});

// OPS29: the State fact, the Now line, its note and the header button read one computed state (`taking`).
describe('One state for taking jobs', () => {
  const cases = [
    ['stopped', 'Not taking jobs', /You stopped new jobs from the console/, 'Resume'],
    ['mode_paused', 'Paused', /The mode is Paused/, 'Stop new jobs'],
    ['tray_paused', 'Paused', /paused from the tray/, 'Stop new jobs'],
    ['on_battery', 'On battery', /while it runs on battery/, 'Stop new jobs'],
    ['busy', 'Busy', /load is over the budget/, 'Stop new jobs'],
    ['base_image_missing', 'Needs a base image', /no runner base image/, 'Stop new jobs'],
    ['disk_low', 'Disk low', /disk is almost full/, 'Stop new jobs'],
  ] as const;
  for (const [reason, word, note, button] of cases) {
    it(`says ${reason} the same everywhere`, async () => {
      renderApp(
        detail(
          machine({
            state: reason === 'stopped' ? 'drained' : 'active',
            accepting: false,
            taking: { taking_jobs: false, reason },
          }),
        ),
        '/setup/machines/m1',
      );
      await screen.findByRole('heading', { name: 'build-1' });
      // State and Now: the same word, twice. A taking word never shows beside it.
      expect(screen.getAllByText(word).length).toBeGreaterThanOrEqual(2);
      expect(screen.queryByText('Taking jobs')).toBeNull();
      expect(screen.getByText(note)).toBeTruthy();
      expect(screen.getByRole('button', { name: button })).toBeTruthy();
    });
  }

  it('says Taking jobs in State and Now, with the button Stop new jobs', async () => {
    renderApp(detail(), '/setup/machines/m1');
    await screen.findByRole('heading', { name: 'build-1' });
    expect(screen.getAllByText('Taking jobs').length).toBeGreaterThanOrEqual(2);
    expect(screen.queryByText('Not taking jobs')).toBeNull();
    expect(screen.getByRole('button', { name: 'Stop new jobs' })).toBeTruthy();
  });

  it('shows a stopped machine as one picture, and Resume', async () => {
    renderApp(detail(machine({ state: 'drained' })), '/setup/machines/m1');
    await screen.findByRole('heading', { name: 'build-1' });
    expect(screen.getByRole('button', { name: 'Resume' })).toBeTruthy();
    expect(screen.queryByText('Taking jobs')).toBeNull();
    expect(screen.getAllByText('Not taking jobs').length).toBeGreaterThanOrEqual(2);
  });

  it('shows the battery switch as not applying under Always, with the reason', async () => {
    renderApp(
      detail(withSettings({ mode: 'always', pause_on_battery: true })),
      '/setup/machines/m1',
    );
    await screen.findByRole('heading', { name: 'build-1' });
    const sw = screen.getByRole<HTMLButtonElement>('switch', {
      name: 'Pause on battery',
    });
    expect(sw.disabled).toBe(true);
    expect(sw.getAttribute('aria-checked')).toBe('false');
    expect(sw.textContent).toBe('Off');
    expect(screen.getAllByText('Always ignores the battery.')).toHaveLength(1);
  });

  it('shows the battery switch as On under Automatic', async () => {
    renderApp(detail(), '/setup/machines/m1');
    await screen.findByRole('heading', { name: 'build-1' });
    const sw = screen.getByRole<HTMLButtonElement>('switch', {
      name: 'Pause on battery',
    });
    expect(sw.disabled).toBe(false);
    expect(sw.getAttribute('aria-checked')).toBe('true');
  });
});
