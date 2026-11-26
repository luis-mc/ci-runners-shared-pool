// R5.14, R5.15, R5.16: the setup page. Without a setup link it says how to get one. With one it shows the three
// first-run wizards in order; a later one is greyed until the one before it is done. The checklist stays reachable
// from the banner slot until all three are done.
import { cleanup, fireEvent, screen, waitFor } from '@testing-library/react';
import { afterEach, describe, expect, it } from 'vitest';
import { existsSync, readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { apiError, machine, renderApp, signedIn } from '../../testing/mock';
import { messageFor } from './Setup';

afterEach(cleanup);

const state = (over: object = {}) => ({
  has_app: false,
  has_owner: false,
  setup_link_redeemed: false,
  steps: { connect_github: 'todo', first_machine: 'todo', first_repository: 'todo' },
  ...over,
});

describe('Setup page', () => {
  it('without a setup link says how to get one and offers nothing else (R5.14)', async () => {
    const api = signedIn().on('GET', '/api/v1/setup', state());
    renderApp(api, '/setup?wizard=1');
    expect(
      await screen.findByText(
        /choose Open in the tray menu of the controller.s machine\./,
      ),
    ).toBeTruthy();
    expect(screen.queryByRole('button', { name: 'Next' })).toBeNull();
  });

  it('redeems the setup link once and shows the first wizard (R5.14, R5.15)', async () => {
    const api = signedIn()
      .on('POST', '/api/v1/setup/redeem', state({ setup_link_redeemed: true }))
      .on('GET', '/api/v1/setup', state());
    renderApp(api, '/setup?code=one-time');
    expect(await screen.findByRole('group', { name: 'Connect GitHub' })).toBeTruthy();
    expect(api.count('POST', '/api/v1/setup/redeem')).toBe(1);
    expect(
      (api.calls.find((c) => c.method === 'POST')?.body as { code: string }).code,
    ).toBe('one-time');
    // The later wizards are greyed until this one is done (UX-5).
    expect(screen.getByText('This waits for Connect GitHub.')).toBeTruthy();
    expect(screen.getByText('This waits for Add the first machine.')).toBeTruthy();
    expect(screen.queryByRole('group', { name: 'Add a machine' })).toBeNull();
  });

  it('says so when the setup link does not work', async () => {
    const api = signedIn()
      .on('POST', '/api/v1/setup/redeem', () =>
        apiError(400, 'bad_request', 'The link ran out.'),
      )
      .on('GET', '/api/v1/setup', state());
    renderApp(api, '/setup?code=old');
    expect(
      await screen.findByText(/The setup link did not work\. The link ran out\./),
    ).toBeTruthy();
  });

  it('says GitHub made the App but crsp could not save it, links to it, and clears the error (R5.3)', async () => {
    const api = signedIn()
      .on('POST', '/api/v1/setup/redeem', state({ setup_link_redeemed: true }))
      .on('GET', '/api/v1/setup', state({ setup_link_redeemed: true }));
    renderApp(
      api,
      '/setup?code=x&error=unavailable&app_url=' +
        encodeURIComponent('https://github.com/apps/crsp-home'),
    );
    expect(
      await screen.findByText(/GitHub made the App, but crsp could not save it/),
    ).toBeTruthy();
    expect(screen.getByText(/Delete GitHub App/)).toBeTruthy();
    expect(
      screen.getByRole('button', { name: 'Open the App on GitHub' }).getAttribute('href'),
    ).toBe('https://github.com/apps/crsp-home');
    await waitFor(() => expect(window.location.search).not.toContain('error='));
  });

  it('offers no link to an address that is not on github.com', async () => {
    const api = signedIn().on(
      'GET',
      '/api/v1/setup',
      state({ setup_link_redeemed: true }),
    );
    renderApp(
      api,
      '/setup?error=unavailable&app_url=' + encodeURIComponent('https://evil.example/x'),
    );
    expect(await screen.findByText(/OS secret store is not available/)).toBeTruthy();
    expect(screen.queryByRole('button', { name: 'Open the App on GitHub' })).toBeNull();
  });

  it('explains that App names are unique across GitHub (R5.3)', async () => {
    const api = signedIn().on(
      'GET',
      '/api/v1/setup',
      state({ setup_link_redeemed: true }),
    );
    renderApp(api, '/setup?wizard=1');
    expect(await screen.findByText(/App names are unique across GitHub/)).toBeTruthy();
  });

  it('goes on to the machine wizard once the App is connected and the user is signed in', async () => {
    const api = signedIn()
      .on(
        'GET',
        '/api/v1/setup',
        state({
          has_app: true,
          setup_link_redeemed: true,
          steps: {
            connect_github: 'done',
            first_machine: 'todo',
            first_repository: 'todo',
          },
        }),
      )
      .on('GET', '/api/v1/machines', { items: [] });
    renderApp(api, '/setup?wizard=1');
    expect(await screen.findByRole('group', { name: 'Add a machine' })).toBeTruthy();
    expect(screen.getByText('Connect GitHub is done.')).toBeTruthy();
    expect(screen.getByText('This waits for Add the first machine.')).toBeTruthy();
  });

  it('reviews the account step before it goes to GitHub, and says nothing has changed', async () => {
    const api = signedIn()
      .on('POST', '/api/v1/setup/redeem', state({ setup_link_redeemed: true }))
      .on('GET', '/api/v1/setup', state({ setup_link_redeemed: true }));
    renderApp(api, '/setup?code=x');
    fireEvent.click(await screen.findByRole('radio', { name: 'An organization' }));
    fireEvent.click(screen.getByRole('button', { name: 'Next' }));
    expect(await screen.findByText('Write the organization.')).toBeTruthy();
    fireEvent.change(screen.getByRole('textbox', { name: 'Organization' }), {
      target: { value: 'acme' },
    });
    fireEvent.click(screen.getByRole('button', { name: 'Next' }));
    expect(await screen.findByText('Nothing has changed yet.')).toBeTruthy();
    expect(screen.getByText('acme')).toBeTruthy();
    expect(api.count('POST', '/api/v1/setup/github-app')).toBe(0);
  });
});

// UX-6 guard: every error code the server can put on /setup?error= shows words on the page.
// The codes are read from internal/api/errors.go, so a new code is covered without editing this test.
// Tests run from apps/web (pnpm --filter web) or from the repository root.
const errorsGo = ['../../internal/api/errors.go', 'internal/api/errors.go']
  .map((p) => resolve(process.cwd(), p))
  .find((p) => existsSync(p));
const serverCodes = [
  ...readFileSync(errorsGo ?? 'internal/api/errors.go', 'utf8').matchAll(
    /^\s*Code\w+\s*=\s*"([a-z_]+)"/gm,
  ),
].flatMap((m) => (m[1] ? [m[1]] : []));

describe('Setup errors (UX-6)', () => {
  it('reads the server error codes', () => {
    expect(serverCodes).toContain('unavailable');
    expect(serverCodes.length).toBeGreaterThan(5);
  });

  it.each(serverCodes)('shows a message for error=%s', async (code) => {
    const api = signedIn().on(
      'GET',
      '/api/v1/setup',
      state({ setup_link_redeemed: true }),
    );
    renderApp(api, '/setup?error=' + code);
    const words = messageFor(code, false);
    expect(words.length).toBeGreaterThan(10);
    expect(await screen.findByText(words)).toBeTruthy();
  });
});

describe('Setup page when every step is done (UX-6)', () => {
  const allDoneState = (over: object = {}) =>
    state({
      has_app: true,
      has_owner: true,
      setup_link_redeemed: true,
      steps: { connect_github: 'done', first_machine: 'done', first_repository: 'done' },
      ...over,
    });

  it('offers one primary, Open the overview, that goes on', async () => {
    const api = signedIn()
      .on('GET', '/api/v1/setup', allDoneState())
      .on('GET', '/api/v1/machines', { items: [machine()] });
    renderApp(api, '/setup?wizard=1');
    const go = await screen.findByRole('button', { name: 'Open the overview' });
    expect(go.getAttribute('data-kind')).toBe('primary');
    expect(go.hasAttribute('data-primary')).toBe(true);
    expect(screen.getAllByRole('heading', { level: 1 })).toHaveLength(1);
    const primaries = screen
      .getAllByRole('button')
      .filter((b) => b.getAttribute('data-kind') === 'primary');
    expect(primaries).toHaveLength(1);
    fireEvent.click(go);
    await waitFor(() => expect(window.location.pathname).toBe('/'));
  });

  it('counts the first repository when any repository has the pool on', async () => {
    const api = signedIn()
      .on(
        'GET',
        '/api/v1/setup',
        allDoneState({
          steps: {
            connect_github: 'done',
            first_machine: 'done',
            first_repository: 'todo',
          },
        }),
      )
      .on('GET', '/api/v1/machines', { items: [machine()] })
      .on('GET', '/api/v1/github/repositories', {
        items: [{ id: 1, full_name: 'octo/home-automation', pool_on: true }],
        stale: { stale: false },
      });
    renderApp(api, '/setup?wizard=1');
    expect(await screen.findByRole('button', { name: 'Open the overview' })).toBeTruthy();
    expect(screen.queryByRole('group', { name: 'Turn on the pool' })).toBeNull();
  });

  it('does not offer the overview while a step is open', async () => {
    const api = signedIn()
      .on(
        'GET',
        '/api/v1/setup',
        allDoneState({
          steps: {
            connect_github: 'done',
            first_machine: 'done',
            first_repository: 'todo',
          },
        }),
      )
      .on('GET', '/api/v1/machines', { items: [machine()] })
      .on('GET', '/api/v1/github/repositories', { items: [], stale: { stale: false } });
    renderApp(api, '/setup?wizard=1');
    await screen.findByText('Turn on the first repository');
    expect(screen.queryByRole('button', { name: 'Open the overview' })).toBeNull();
  });

  it('never sends the owner to a terminal', async () => {
    renderApp(signedIn().on('GET', '/api/v1/setup', state()), '/setup?wizard=1');
    await screen.findByText(/no GitHub App yet/);
    expect(document.body.textContent).not.toMatch(/crsp open|terminal|command line/i);
  });
});

describe('First run banner (R5.15)', () => {
  it('stays in the banner slot until all three wizards are done', async () => {
    const api = signedIn().on(
      'GET',
      '/api/v1/setup',
      state({
        has_app: true,
        steps: {
          connect_github: 'done',
          first_machine: 'todo',
          first_repository: 'todo',
        },
      }),
    );
    renderApp(api, '/');
    expect(await screen.findByText(/First run is not finished\./)).toBeTruthy();
    expect(
      screen.getByRole('button', { name: 'Open the setup page' }).getAttribute('href'),
    ).toBe('/setup?wizard=1');
  });

  it('is gone when every wizard is done', async () => {
    renderApp(signedIn(), '/');
    await screen.findByRole('heading', { name: 'Overview' });
    await waitFor(() =>
      expect(screen.queryByText(/First run is not finished\./)).toBeNull(),
    );
  });

  describe('the first machine step when this PC is in the pool (machines R5.1)', () => {
    const done = () =>
      state({
        has_app: true,
        has_owner: true,
        setup_link_redeemed: true,
        steps: {
          connect_github: 'done',
          first_machine: 'done',
          first_repository: 'todo',
        },
      });

    it('shows the controller PC as done and keeps the command path behind Add another machine', async () => {
      const api = signedIn()
        .on('GET', '/api/v1/setup', done())
        .on('GET', '/api/v1/machines', {
          items: [
            machine({
              id: 'own',
              name: 'office-pc',
              is_controller: true,
              facts: { host_name: 'OFFICE-PC' },
            }),
          ],
        });
      renderApp(api, '/setup?wizard=1');
      expect(await screen.findByText('This PC, OFFICE-PC, is in the pool.')).toBeTruthy();
      expect(screen.queryByRole('group', { name: 'Add a machine' })).toBeNull();
      fireEvent.click(screen.getByRole('button', { name: 'Add another machine' }));
      expect(await screen.findByRole('group', { name: 'Add a machine' })).toBeTruthy();
    });

    it('shows the Get this machine ready door for another online machine with no engine (DEP13)', async () => {
      const api = signedIn()
        .on('GET', '/api/v1/setup', done())
        .on('GET', '/api/v1/machines', {
          items: [
            machine({ id: 'm2', name: 'lab-pc', backends: [] }),
            machine({ id: 'm3', name: 'old-pc', backends: [], online: false }),
          ],
        });
      renderApp(api, '/setup?wizard=1');
      expect(
        await screen.findByText(
          'No container engine answers on lab-pc. Linux runners need one.',
        ),
      ).toBeTruthy();
      const link = screen.getByRole('link', { name: 'Get this machine ready' });
      expect(link.getAttribute('href')).toContain('/setup/machines/m2/ready');
      // An offline machine is not offered an install (DEP12).
      expect(screen.queryByText(/old-pc/)).toBeNull();
    });

    it('says the step is done when the machine in the pool is not this PC', async () => {
      const api = signedIn()
        .on('GET', '/api/v1/setup', done())
        .on('GET', '/api/v1/machines', { items: [machine()] });
      renderApp(api, '/setup?wizard=1');
      expect(await screen.findByText('Add the first machine is done.')).toBeTruthy();
      expect(screen.getByRole('button', { name: 'Add another machine' })).toBeTruthy();
    });
  });
});
