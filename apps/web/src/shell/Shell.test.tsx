// R5.3 (shell), R5.4 (palette), R5.8..R5.12 (sessions), R5.7 (paused banner).
import { act, cleanup, fireEvent, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import {
  FakeEventSource,
  apiError,
  machine,
  overview,
  renderApp,
  repository,
  signedIn,
  viewer,
} from '../testing/mock';

const assign = vi.fn();
beforeEach(() => {
  assign.mockReset();
  Object.defineProperty(window, 'location', {
    configurable: true,
    value: {
      ...window.location,
      assign,
      origin: 'http://localhost',
      pathname: '/',
      search: '',
    },
  });
});
afterEach(cleanup);

describe('the shell (R5.3)', () => {
  it('has the header, the seven areas, one headline and the main landmark', async () => {
    renderApp(signedIn(), '/');
    expect(
      await screen.findByRole('heading', { level: 1, name: 'Overview' }),
    ).toBeTruthy();
    expect(screen.getAllByRole('heading', { level: 1 })).toHaveLength(1);
    expect(screen.getAllByRole('banner').length).toBeGreaterThan(0);
    expect(screen.getByRole('main')).toBeTruthy();
    const nav = screen.getByRole('navigation', { name: 'Areas' });
    expect(
      within(nav)
        .getAllByRole('link')
        .map((a) => a.textContent),
    ).toEqual(['Overview', 'Setup', 'Pool', 'Doctor', 'Analytics', 'Settings', 'Help']);
    expect(
      within(nav).getByRole('link', { name: 'Overview' }).getAttribute('aria-current'),
    ).toBe('page');
  });

  it('shows the pool status as a word and a glyph (UX-58)', async () => {
    renderApp(signedIn(), '/');
    await screen.findByRole('heading', { name: 'Overview' });
    // The live stream opens in an effect after the first paint; wait for it.
    await waitFor(() => FakeEventSource.latest());
    act(() =>
      FakeEventSource.latest().emit({
        id: '1',
        type: 'snapshot',
        at: 'x',
        snapshot: overview(),
      }),
    );
    expect(await screen.findByText('1 of 1 machine online')).toBeTruthy();
    expect(screen.getByText('live updates')).toBeTruthy();
  });

  it('reads the header count from the machines list, not the overview (UX-19)', async () => {
    const api = signedIn().on('GET', '/api/v1/machines', {
      items: [machine(), machine({ id: 'm2', name: 'GameRoomPC-2' })],
    });
    renderApp(api, '/');
    expect(await screen.findByText('2 of 2 machines online')).toBeTruthy();
    expect(screen.queryByText('no machines')).toBeNull();
  });

  it('says no machines only when the machines list is empty', async () => {
    renderApp(signedIn().on('GET', '/api/v1/machines', { items: [] }), '/');
    expect(await screen.findByText('no machines')).toBeTruthy();
  });

  it('says reconnecting, never no machines or the first-run banner, while the service restarts (CRSP205)', async () => {
    let phase: 'up' | 'down' | 'back-empty' | 'back' = 'up';
    const api = signedIn()
      .on('GET', '/api/v1/machines', () => {
        if (phase === 'down')
          return apiError(503, 'unavailable', 'The service is restarting.');
        if (phase === 'back-empty') return { items: [] };
        return { items: [machine()] };
      })
      .on('GET', '/api/v1/setup', () => ({
        has_app: true,
        has_owner: true,
        setup_link_redeemed: true,
        steps: {
          connect_github: 'done',
          first_machine: phase === 'back-empty' ? 'todo' : 'done',
          first_repository: 'done',
        },
      }));
    renderApp(api, '/');
    expect(await screen.findByText('1 of 1 machine online')).toBeTruthy();

    // The service goes away: the header waits, it does not claim an empty pool.
    phase = 'down';
    act(() => void window.dispatchEvent(new Event('focus')));
    expect(await screen.findByText('reconnecting')).toBeTruthy();
    expect(screen.queryByText('no machines')).toBeNull();

    // The controller listens again, its machine list not loaded yet: still reconnecting.
    phase = 'back-empty';
    act(() => void window.dispatchEvent(new Event('focus')));
    await waitFor(() => expect(api.count('GET', '/api/v1/machines')).toBeGreaterThan(2));
    await waitFor(() => expect(api.count('GET', '/api/v1/setup')).toBeGreaterThan(2));
    expect(screen.getByText('reconnecting')).toBeTruthy();
    expect(screen.queryByText('no machines')).toBeNull();
    expect(screen.queryByText(/First run is not finished\./)).toBeNull();

    // The machine joins again.
    phase = 'back';
    act(() => void window.dispatchEvent(new Event('focus')));
    expect(await screen.findByText('1 of 1 machine online')).toBeTruthy();
    expect(screen.queryByText('reconnecting')).toBeNull();
  });

  it('hides the first-run banner when all three steps are done', async () => {
    renderApp(signedIn(), '/');
    await screen.findByRole('heading', { name: 'Overview' });
    await screen.findByText('1 of 1 machine online');
    expect(screen.queryByText(/First run is not finished\./)).toBeNull();
  });

  it('shows the first-run banner while a step is todo', async () => {
    const api = signedIn().on('GET', '/api/v1/setup', {
      has_app: true,
      has_owner: true,
      setup_link_redeemed: true,
      steps: { connect_github: 'done', first_machine: 'todo', first_repository: 'done' },
    });
    renderApp(api, '/');
    expect(
      await screen.findByText('First run is not finished. Next: add the first machine.'),
    ).toBeTruthy();
  });

  it('counts the first repository when any repository has the pool on', async () => {
    const api = signedIn()
      .on('GET', '/api/v1/setup', {
        has_app: true,
        has_owner: true,
        setup_link_redeemed: true,
        steps: {
          connect_github: 'done',
          first_machine: 'done',
          first_repository: 'todo',
        },
      })
      .on('GET', '/api/v1/github/repositories', {
        items: [{ id: 1, full_name: 'octo/home-automation', pool_on: true }],
        stale: { stale: false },
      });
    renderApp(api, '/');
    await screen.findByRole('heading', { name: 'Overview' });
    await waitFor(() =>
      expect(api.count('GET', '/api/v1/github/repositories')).toBeGreaterThan(0),
    );
    expect(screen.queryByText(/Next: turn on the first repository/)).toBeNull();
  });

  it('keeps the banner when no repository has the pool on', async () => {
    const api = signedIn()
      .on('GET', '/api/v1/setup', {
        has_app: true,
        has_owner: true,
        setup_link_redeemed: true,
        steps: {
          connect_github: 'done',
          first_machine: 'done',
          first_repository: 'todo',
        },
      })
      .on('GET', '/api/v1/github/repositories', { items: [], stale: { stale: false } });
    renderApp(api, '/');
    expect(
      await screen.findByText(
        'First run is not finished. Next: turn on the first repository.',
      ),
    ).toBeTruthy();
  });

  it('says Live updates paused and keeps the last data (R5.7)', async () => {
    renderApp(signedIn(), '/');
    await screen.findByRole('heading', { name: 'Overview' });
    // The live stream opens in an effect after the first paint; wait for it.
    await waitFor(() => FakeEventSource.latest());
    act(() =>
      FakeEventSource.latest().emit({
        id: '1',
        type: 'snapshot',
        at: 'x',
        snapshot: overview(),
      }),
    );
    await screen.findByText('build-1');
    act(() => FakeEventSource.latest().fail());
    expect(await screen.findByText(/Live updates paused/)).toBeTruthy();
    expect(screen.getByText('build-1')).toBeTruthy();
  });

  it('says in one line on the Overview while a repository runs on hosted runners (R5.44)', async () => {
    const live = repository({
      pool_on: true,
      routing: { where: 'hosted', why: 'fallback_on', oses: ['linux'], used_minutes: 3 },
    });
    renderApp(
      signedIn().on('GET', '/api/v1/github/repositories', {
        items: [live],
        stale: { stale: false },
      }),
      '/',
    );
    expect(
      await screen.findByText(
        '1 repository runs on GitHub-hosted runners because no crsp machine is up.',
      ),
    ).toBeTruthy();
  });

  it('shows not found with a link to the overview', async () => {
    renderApp(signedIn(), '/nowhere');
    expect(await screen.findByRole('heading', { name: 'Page not found' })).toBeTruthy();
    expect(screen.getByRole('link', { name: 'Open the overview' })).toBeTruthy();
  });
});

describe('the command palette (R5.4)', () => {
  it('opens with Ctrl+K, finds areas and goes to the choice', async () => {
    renderApp(signedIn(), '/');
    await screen.findByRole('heading', { name: 'Overview' });
    fireEvent.keyDown(document, { key: 'k', ctrlKey: true });
    const dialog = await screen.findByRole('dialog', { name: 'Commands' });
    fireEvent.change(within(dialog).getByRole('textbox'), {
      target: { value: 'analytics' },
    });
    fireEvent.keyDown(within(dialog).getByRole('textbox'), { key: 'Enter' });
    expect(await screen.findByRole('heading', { name: 'Analytics' })).toBeTruthy();
  });

  it('opens with Cmd+K and closes with Escape', async () => {
    renderApp(signedIn(), '/');
    await screen.findByRole('heading', { name: 'Overview' });
    fireEvent.keyDown(document, { key: 'k', metaKey: true });
    const dialog = await screen.findByRole('dialog', { name: 'Commands' });
    fireEvent.keyDown(dialog, { key: 'Escape' });
    await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull());
  });

  it('lists commands only for roles that may use them', async () => {
    const owner = renderApp(signedIn(), '/');
    await screen.findByRole('heading', { name: 'Overview' });
    fireEvent.keyDown(document, { key: 'k', ctrlKey: true });
    const d1 = await screen.findByRole('dialog', { name: 'Commands' });
    expect(within(d1).getByText('Add a machine')).toBeTruthy();
    owner.unmount();
    cleanup();

    renderApp(signedIn(viewer), '/');
    await screen.findByRole('heading', { name: 'Overview' });
    fireEvent.keyDown(document, { key: 'k', ctrlKey: true });
    const d2 = await screen.findByRole('dialog', { name: 'Commands' });
    expect(within(d2).queryByText('Add a machine')).toBeNull();
    expect(within(d2).queryByText('Add a user')).toBeNull();
  });

  it('opens with focus in the search box, filters as you type, moves with Down, Up, j and k, opens with Enter (UX-57)', async () => {
    const user = userEvent.setup();
    renderApp(signedIn(), '/');
    await screen.findByRole('heading', { name: 'Overview' });
    await user.keyboard('{Control>}k{/Control}');
    const dialog = await screen.findByRole('dialog', { name: 'Commands' });
    const box = within(dialog).getByRole('textbox', {
      name: 'Search areas, items and commands',
    });
    await waitFor(() => expect(document.activeElement).toBe(box));
    // j types in the box; it does not move.
    await user.keyboard('j');
    expect((box as HTMLInputElement).value).toBe('j');
    await user.clear(box);
    await user.keyboard('ana');
    const options = () => within(dialog).getAllByRole('option');
    expect(options().map((o) => o.textContent)).toEqual(
      expect.arrayContaining([expect.stringContaining('Analytics')]),
    );
    expect(within(dialog).queryByRole('option', { name: /Doctor/ })).toBeNull();
    await user.clear(box);
    await user.keyboard('{ArrowDown}');
    expect(document.activeElement).toBe(within(options()[0]!).getByRole('button'));
    await user.keyboard('j');
    expect(document.activeElement).toBe(within(options()[1]!).getByRole('button'));
    await user.keyboard('k');
    expect(document.activeElement).toBe(within(options()[0]!).getByRole('button'));
    await user.keyboard('{ArrowUp}');
    expect(document.activeElement).toBe(box);
    await user.type(box, 'Analytics');
    await user.keyboard('{Enter}');
    expect(await screen.findByRole('heading', { name: 'Analytics' })).toBeTruthy();
    expect(screen.queryByRole('dialog', { name: 'Commands' })).toBeNull();
  });

  it('Escape closes the palette', async () => {
    const user = userEvent.setup();
    renderApp(signedIn(), '/');
    await screen.findByRole('heading', { name: 'Overview' });
    await user.keyboard('{Control>}k{/Control}');
    await screen.findByRole('dialog', { name: 'Commands' });
    await user.keyboard('{Escape}');
    await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull());
  });

  it('opens the shortcut list with ?', async () => {
    renderApp(signedIn(), '/');
    await screen.findByRole('heading', { name: 'Overview' });
    fireEvent.keyDown(document, { key: '?' });
    const d = await screen.findByRole('dialog', { name: 'Shortcuts' });
    expect(within(d).getByText('Move to the next control or list item')).toBeTruthy();
  });
});

describe('sessions (R5.8 to R5.12)', () => {
  it('sends a signed-out visitor to sign-in and keeps the path (R5.8, R5.9)', async () => {
    const api = signedIn().on('GET', '/api/v1/me', () =>
      apiError(401, 'unauthenticated', 'Sign in.'),
    );
    renderApp(api, '/setup/machines');
    const button = await screen.findByRole('button', { name: 'Sign in with GitHub' });
    fireEvent.click(button);
    expect(assign).toHaveBeenCalledWith(
      '/auth/github/login?return_to=%2Fsetup%2Fmachines',
    );
  });

  it('says the tray link expired and offers GitHub sign-in (tray R5.7)', async () => {
    const api = signedIn().on('GET', '/api/v1/me', () =>
      apiError(401, 'unauthenticated', 'Sign in.'),
    );
    renderApp(api, '/sign-in?error=link_expired');
    expect(await screen.findByText(/sign-in link from the tray expired/)).toBeTruthy();
    expect(screen.queryByText(/GitHub did not let you in/)).toBeNull();
    expect(screen.getByRole('button', { name: 'Sign in with GitHub' })).toBeTruthy();
  });

  it('refuses a return path that is not on this origin (R5.9)', async () => {
    const { safeReturnPath } = await import('../auth/session');
    expect(safeReturnPath('/setup/machines/m1')).toBe('/setup/machines/m1');
    expect(safeReturnPath('//evil.example')).toBe('/');
    expect(safeReturnPath('https://evil.example/x')).toBe('/');
    expect(safeReturnPath('/\\evil')).toBe('/');
    expect(safeReturnPath(null)).toBe('/');
  });

  it('says no access with the login and Sign out, and sends no data (R5.10)', async () => {
    const api = signedIn().on('GET', '/api/v1/me', () =>
      apiError(403, 'forbidden', 'No access.', [{ kind: 'user', id: 'stranger' }]),
    );
    renderApp(api, '/');
    expect(await screen.findByText(/Signed in as stranger/)).toBeTruthy();
    expect(screen.getByRole('button', { name: 'Sign out' })).toBeTruthy();
    expect(api.count('GET', '/api/v1/machines')).toBe(0);
    expect(api.count('GET', '/api/v1/overview')).toBe(0);
  });

  it('shows the session-ended dialog over the page and keeps the page (R5.11)', async () => {
    const api = signedIn().on('GET', '/api/v1/machines', () =>
      apiError(401, 'session_expired', 'The session ended.'),
    );
    renderApp(api, '/setup/machines');
    const dialog = await screen.findByRole('dialog', { name: 'Session ended' });
    expect(
      within(dialog).getByRole('button', { name: 'Sign in with GitHub' }),
    ).toBeTruthy();
    expect(screen.getByRole('heading', { name: 'Machines' })).toBeTruthy();
    fireEvent.click(within(dialog).getByRole('button', { name: 'Sign in with GitHub' }));
    expect(assign).toHaveBeenCalledWith(
      '/auth/github/login?return_to=%2Fsetup%2Fmachines',
    );
  });

  it('signs out on the server and goes to sign-in (R5.12)', async () => {
    const api = signedIn().on('POST', '/api/v1/auth/logout', undefined);
    renderApp(api, '/');
    await screen.findByRole('heading', { name: 'Overview' });
    fireEvent.click(screen.getByRole('button', { name: 'octo' }));
    fireEvent.click(screen.getByRole('button', { name: 'Sign out' }));
    await waitFor(() => expect(api.count('POST', '/api/v1/auth/logout')).toBe(1));
    expect(await screen.findByRole('heading', { name: 'Sign in' })).toBeTruthy();
  });

  it('shows the GitHub sign-in as active with its time in the account menu (LOOK15)', async () => {
    const api = signedIn();
    renderApp(api, '/');
    await screen.findByRole('heading', { name: 'Overview' });
    expect(screen.queryByRole('img', { name: 'GitHub sign-in ended' })).toBeNull();
    fireEvent.click(screen.getByRole('button', { name: 'octo' }));
    expect(screen.getByText(/GitHub sign-in: active until \d\d:\d\d/)).toBeTruthy();
    expect(
      screen.queryByRole('button', { name: 'Sign in with GitHub again' }),
    ).toBeNull();
  });

  it('shows an ended GitHub sign-in with a dot and signs out then in again (LOOK15)', async () => {
    const api = signedIn()
      .on('GET', '/api/v1/me', {
        user: { id: 1, login: 'octo', role: 'owner', added_at: '2026-11-14T10:00:00Z' },
        csrf: 'tok',
        github_signin: { state: 'ended' },
      })
      .on('POST', '/api/v1/auth/logout', undefined);
    renderApp(api, '/setup/machines');
    await screen.findByRole('heading', { name: 'Machines' });
    expect(screen.getByRole('img', { name: 'GitHub sign-in ended' })).toBeTruthy();
    fireEvent.click(screen.getByRole('button', { name: /octo/ }));
    expect(screen.getByText('GitHub sign-in: ended')).toBeTruthy();
    fireEvent.click(screen.getByRole('button', { name: 'Sign in with GitHub again' }));
    await waitFor(() =>
      expect(assign).toHaveBeenCalledWith(
        '/auth/github/login?return_to=%2Fsetup%2Fmachines',
      ),
    );
    expect(api.count('POST', '/api/v1/auth/logout')).toBe(1);
  });

  it('sends the CSRF token on changes (R6.2)', async () => {
    const api = signedIn().on('POST', '/api/v1/auth/logout', undefined);
    let header: string | null = null;
    const original = api.fetch;
    api.fetch = (input) => {
      if (typeof input !== 'string' && input.method === 'POST')
        header = input.headers.get('X-CSRF-Token');
      return original(input);
    };
    renderApp(api, '/');
    await screen.findByRole('heading', { name: 'Overview' });
    fireEvent.click(screen.getByRole('button', { name: 'octo' }));
    fireEvent.click(screen.getByRole('button', { name: 'Sign out' }));
    await waitFor(() => expect(header).toBe('tok'));
  });

  it('shows a machine list through the same shell', async () => {
    const api = signedIn().on('GET', '/api/v1/machines', { items: [machine()] });
    renderApp(api, '/setup/machines');
    expect(await screen.findByRole('link', { name: 'build-1' })).toBeTruthy();
  });
});

describe('the menu on a phone (UX-15, UX-16)', () => {
  it('is one Menu button that opens the areas', async () => {
    const query = (q: string) =>
      ({
        matches: q.includes('max-width'),
        media: q,
        addEventListener: () => undefined,
        removeEventListener: () => undefined,
      }) as unknown as MediaQueryList;
    vi.stubGlobal('matchMedia', query);
    try {
      renderApp(signedIn(), '/');
      const button = await screen.findByRole('button', { name: 'Menu: Overview' });
      expect(screen.queryByRole('navigation', { name: 'Areas' })).toBeNull();
      fireEvent.click(button);
      const nav = screen.getByRole('navigation', { name: 'Areas' });
      expect(within(nav).getAllByRole('link')).toHaveLength(7);
      expect(screen.getByRole('button', { name: 'Close the menu' })).toBeTruthy();
    } finally {
      vi.unstubAllGlobals();
    }
  });
});

// runners R5.89: a runner service that did not start is a Problem banner on every screen, with the machine,
// the runner and the reason; Start asks the agent again, and the banner clears when the service runs.
describe('a runner service that did not start', () => {
  const why =
    'The service did not start: Logon failure: the user has not been granted the requested logon type at this computer (Windows error 1069)';
  const broken = () =>
    machine({
      id: 'm9',
      name: 'GameRoomPC',
      os: 'windows',
      runner_problems: [
        {
          runner: 'crsp-gameroompc-1',
          service: 'actions.runner.luis-mc-demo.crsp-gameroompc-1',
          account: '.\\crsp-gameroompc-1',
          reason: why,
          logon_right: false,
        },
      ],
    });

  it('shows a Problem banner naming the machine, the runner and the reason, with Start', async () => {
    const api = signedIn().on('GET', '/api/v1/machines', { items: [broken()] });
    renderApp(api, '/setup/runners');
    const banner = await screen.findByText(
      /crsp-gameroompc-1 on GameRoomPC did not start/,
    );
    expect(banner.textContent).toContain('Logon failure');
    expect(screen.getByRole('button', { name: 'Start' })).toBeTruthy();
  });

  it('Start asks that machine to check again, and the banner clears once the service runs', async () => {
    let fixed = false;
    const api = signedIn()
      .on('GET', '/api/v1/machines', () => ({
        items: [fixed ? machine({ id: 'm9', name: 'GameRoomPC' }) : broken()],
      }))
      .on('POST', '/api/v1/machines/:id/redetect', () => {
        fixed = true;
        return { status: 202 };
      });
    renderApp(api, '/setup/runners');
    fireEvent.click(await screen.findByRole('button', { name: 'Start' }));
    await waitFor(() =>
      expect(api.count('POST', '/api/v1/machines/m9/redetect')).toBe(1),
    );
    await waitFor(() =>
      expect(
        screen.queryByText(/crsp-gameroompc-1 on GameRoomPC did not start/),
      ).toBeNull(),
    );
  });

  it('shows no banner for a machine without a problem or one that is offline', async () => {
    const off = { ...broken(), online: false };
    const api = signedIn().on('GET', '/api/v1/machines', { items: [machine(), off] });
    renderApp(api, '/setup/runners');
    await screen.findByRole('heading', { level: 1, name: 'Runners' });
    expect(screen.queryByText(/did not start/)).toBeNull();
  });
});
