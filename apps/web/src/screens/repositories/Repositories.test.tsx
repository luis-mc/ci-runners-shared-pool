// R5.21: the repositories screen lists repositories with pool state, labels, limits and variable status; it
// turns the pool on and off and edits the policy.
import { cleanup, fireEvent, screen, waitFor, within } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { TIMING } from '../../timing';
import {
  MockApi,
  apiError,
  renderApp,
  repository,
  signedIn,
  viewer,
} from '../../testing/mock';

afterEach(cleanup);
TIMING.planPollMs = 5;

const on = repository({
  id: 8,
  full_name: 'octo/site',
  name: 'site',
  pool_on: true,
  variables: [{ name: 'RUNS_ON_LINUX', status: 'wrong', value: 'old' }],
});
const page = (items = [repository(), on]) => ({ items, stale: { stale: false } });

function withRepos(api: MockApi, items = [repository(), on]): MockApi {
  return api.on('GET', '/api/v1/github/repositories', page(items));
}

/** The repository page draws its title while it loads (RES21 B18): wait for the data, not for the title. */
async function repoHeading(name: string) {
  const heading = await screen.findByRole('heading', { level: 1, name });
  await waitFor(() => expect(screen.queryByText(/Loading the repository/)).toBeNull());
  return heading;
}

describe('Repositories list', () => {
  it('shows four columns, and limits and variable status as a muted line under the name (LOOK29)', async () => {
    renderApp(withRepos(signedIn()), '/repositories');
    const table = await screen.findByRole('table', { name: 'octo repositories' });
    expect(
      within(table)
        .getAllByRole('columnheader')
        .map((h) => (h.textContent ?? '').replace(/[ ▲▼]+$/, '').trim())
        .filter(Boolean),
    ).toEqual(['Repository', 'Pool', 'Jobs go to', 'Runner type', 'Last delivery']);
    expect(within(table).getByText('on')).toBeTruthy();
    expect(within(table).getByText('off')).toBeTruthy();
    expect(
      within(table).getByText(/parallel · priority \d+ · wrong variable/),
    ).toBeTruthy();
    expect(within(table).getByText(/parallel · priority \d+ · not used/)).toBeTruthy();
  });

  it('filters by pool state and by search', async () => {
    renderApp(withRepos(signedIn()), '/repositories');
    await screen.findByRole('table', { name: 'octo repositories' });
    fireEvent.click(screen.getByRole('button', { name: 'Pool on' }));
    expect(screen.queryByRole('link', { name: 'app' })).toBeNull();
    expect(screen.getByRole('link', { name: 'site' })).toBeTruthy();
    fireEvent.click(screen.getByRole('button', { name: 'Pool on' }));
    fireEvent.change(screen.getByRole('textbox', { name: 'Search repositories' }), {
      target: { value: 'app' },
    });
    expect(screen.queryByRole('link', { name: 'site' })).toBeNull();
  });

  it('shows the action bar with the count, and Use pool waits for a selection (UX-4, UX-7, UX-12, UX-71)', async () => {
    renderApp(withRepos(signedIn()), '/repositories');
    await screen.findByRole('table', { name: 'octo repositories' });
    const bar = screen.getByRole('region', { name: 'Actions' });
    expect(within(bar).getByText('Nothing selected')).toBeTruthy();
    const use = within(bar).getByRole<HTMLButtonElement>('button', { name: 'Use pool' });
    expect(use.disabled).toBe(true);
    expect(
      within(bar).getByText('Tick the repositories that should use the pool.'),
    ).toBeTruthy();
    fireEvent.click(screen.getByRole('button', { name: 'Select all 2' }));
    expect(within(bar).getByText('2 selected')).toBeTruthy();
    expect(
      within(bar).getByRole<HTMLButtonElement>('button', { name: 'Use pool' }).disabled,
    ).toBe(false);
    fireEvent.click(screen.getByRole('button', { name: 'Clear' }));
    expect(within(bar).getByText('Nothing selected')).toBeTruthy();
  });

  it('turns Use pool into Stop using pool when every selected repository uses the pool', async () => {
    renderApp(withRepos(signedIn()), '/repositories');
    await screen.findByRole('table', { name: 'octo repositories' });
    fireEvent.click(screen.getByRole('checkbox', { name: 'Select octo/site' }));
    const bar = screen.getByRole('region', { name: 'Actions' });
    expect(within(bar).getByRole('button', { name: 'Stop using pool' })).toBeTruthy();
    fireEvent.click(screen.getByRole('checkbox', { name: 'Select octo/app' }));
    expect(within(bar).getByRole('button', { name: 'Use pool' })).toBeTruthy();
  });

  it('disables turn-on for a repository without GitHub admin rights and says why (UX-38)', async () => {
    renderApp(
      withRepos(signedIn(), [repository({ user_is_github_admin: false })]),
      '/repositories',
    );
    await screen.findByRole('table', { name: 'octo repositories' });
    fireEvent.click(screen.getByRole('checkbox', { name: 'Select octo/app' }));
    const button = screen.getByRole<HTMLButtonElement>('button', { name: 'Use pool' });
    expect(button.disabled).toBe(true);
    expect(screen.getByText(/You need admin rights on octo\/app in GitHub/)).toBeTruthy();
  });

  it('does not say "you need admin rights" when the flag was not read (OPS15)', async () => {
    renderApp(
      withRepos(signedIn(), [
        repository({ user_is_github_admin: false, user_admin_known: false }),
      ]),
      '/repositories',
    );
    await screen.findByRole('table', { name: 'octo repositories' });
    fireEvent.click(screen.getByRole('checkbox', { name: 'Select octo/app' }));
    const button = screen.getByRole<HTMLButtonElement>('button', { name: 'Use pool' });
    expect(button.disabled).toBe(false);
    expect(screen.queryByText(/You need admin rights/)).toBeNull();
  });

  it('turns the pool off only after a question that names each repository and variable (UX-62)', async () => {
    const api = withRepos(signedIn())
      .on('POST', '/api/v1/repository-plans', {
        id: 'p1',
        area: 'repositories',
        state: 'planned',
        created_at: 'x',
        created_by: 'octo',
        steps: [
          {
            id: 's1',
            action: 'pool_off',
            text: 'Turn off',
            needs_yes: false,
            state: 'planned',
          },
        ],
      })
      .on('POST', '/api/v1/repository-plans/:id/apply', {
        id: 'p1',
        area: 'repositories',
        state: 'done',
        created_at: 'x',
        created_by: 'octo',
        steps: [],
      });
    renderApp(api, '/repositories');
    await screen.findByRole('table', { name: 'octo repositories' });
    fireEvent.click(screen.getByRole('checkbox', { name: 'Select octo/site' }));
    fireEvent.click(screen.getByRole('button', { name: 'Stop using pool' }));
    const dialog = await screen.findByRole('dialog', {
      name: 'Turn off these repositories?',
    });
    expect(within(dialog).getByText('octo/site: pool off')).toBeTruthy();
    expect(within(dialog).getByText('octo/site: variable RUNS_ON_LINUX')).toBeTruthy();
    expect(api.count('POST', '/api/v1/repository-plans')).toBe(0);
    fireEvent.click(within(dialog).getByRole('button', { name: 'Turn off' }));
    await waitFor(() =>
      expect(api.count('POST', '/api/v1/repository-plans/p1/apply')).toBe(1),
    );
    expect(
      (
        api.calls.find((c) => c.path === '/api/v1/repository-plans')?.body as {
          changes: { pool: string }[];
        }
      ).changes[0]?.pool,
    ).toBe('off');
  });

  it('says what to do when the App is on no repository', async () => {
    renderApp(withRepos(signedIn(), []), '/repositories');
    expect(
      await screen.findByText(
        'No repositories yet. Install the App on a repository to start.',
      ),
    ).toBeTruthy();
  });

  it('shows the last copy with its age when GitHub cannot be reached', async () => {
    const api = signedIn().on('GET', '/api/v1/github/repositories', {
      items: [repository()],
      stale: { stale: true, as_of: '2026-11-14T08:00:00Z', reason: 'unreachable' },
    });
    renderApp(api, '/repositories');
    expect(
      await screen.findByText(
        /GitHub could not be reached\. The list is the last copy, from 2026-11-14/,
      ),
    ).toBeTruthy();
    expect(screen.getByRole('table', { name: 'octo repositories' })).toBeTruthy();
  });

  it('says the sign-in ended, never that GitHub is down (CRSP148)', async () => {
    const api = signedIn().on('GET', '/api/v1/github/repositories', {
      items: [repository()],
      stale: { stale: true, reason: 'session_expired' },
    });
    renderApp(api, '/repositories');
    expect(await screen.findByText(/Your GitHub sign-in ended/)).toBeTruthy();
    expect(screen.queryByText(/not answering/)).toBeNull();
  });

  it('offers Sign in with GitHub again and returns to the same screen (OPS13, UX-6)', async () => {
    const assign = vi.fn();
    vi.stubGlobal('location', { ...window.location, assign });
    try {
      const api = signedIn().on('GET', '/api/v1/github/repositories', {
        items: [repository()],
        stale: { stale: true, reason: 'session_expired' },
      });
      renderApp(api, '/repositories?q=web');
      await screen.findByText(/Your GitHub sign-in ended/);
      fireEvent.click(screen.getByRole('button', { name: 'Sign in with GitHub again' }));
      expect(assign).toHaveBeenCalledWith(
        '/auth/github/login?return_to=' + encodeURIComponent('/repositories?q=web'),
      );
    } finally {
      vi.unstubAllGlobals();
    }
  });

  it('has no sign-in button when the reason is not the sign-in (OPS13)', async () => {
    const api = signedIn().on('GET', '/api/v1/github/repositories', {
      items: [repository()],
      stale: { stale: true, reason: 'unreachable' },
    });
    renderApp(api, '/repositories');
    await screen.findByText(/GitHub could not be reached/);
    expect(
      screen.queryByRole('button', { name: 'Sign in with GitHub again' }),
    ).toBeNull();
  });

  it('shows the real GitHub status and message (UX-28, CRSP148)', async () => {
    const api = signedIn().on('GET', '/api/v1/github/repositories', {
      items: [repository()],
      stale: {
        stale: true,
        reason: 'github_error',
        status: 422,
        message: 'Validation Failed',
      },
    });
    renderApp(api, '/repositories');
    expect(
      await screen.findByText(/GitHub answered 422: Validation Failed\./),
    ).toBeTruthy();
  });

  it('does not say "you need admin rights" while the list is the last copy (CRSP148)', async () => {
    const api = signedIn().on('GET', '/api/v1/github/repositories', {
      items: [repository({ user_is_github_admin: false })],
      stale: { stale: true, reason: 'session_expired' },
    });
    renderApp(api, '/repositories');
    await screen.findByText(/Your GitHub sign-in ended/);
    expect(screen.queryByText(/You need admin rights/)).toBeNull();
  });

  it('says could not load when the controller fails', async () => {
    const api = signedIn().on('GET', '/api/v1/github/repositories', () =>
      apiError(502, 'github_unavailable', 'GitHub says no.'),
    );
    renderApp(api, '/repositories');
    expect(await screen.findByText(/Could not load repositories\./)).toBeTruthy();
    expect(screen.getByText(/GitHub is not answering\. Try again\./)).toBeTruthy();
  });

  it('opens GitHub through the install link for a repository that is not listed', async () => {
    const api = withRepos(signedIn()).on('GET', '/api/v1/github/install-url', {
      url: 'https://github.example/apps/x/installations/new',
    });
    renderApp(api, '/repositories');
    await screen.findByRole('table', { name: 'octo repositories' });
    fireEvent.click(screen.getByRole('button', { name: 'Add a repository' }));
    await waitFor(() => expect(api.count('GET', '/api/v1/github/install-url')).toBe(1));
  });
});

describe('Repository detail', () => {
  const api = () =>
    withRepos(signedIn()).on('GET', '/api/v1/repositories/:id', (_c, p) =>
      p.id === '8' ? on : repository(),
    );

  it('shows state, variables and the policy form', async () => {
    renderApp(api(), '/repositories/octo/site');
    expect(await repoHeading('octo/site')).toBeTruthy();
    expect(screen.getByText('RUNS_ON_LINUX')).toBeTruthy();
    expect(screen.getByRole('textbox', { name: 'Labels' })).toBeTruthy();
  });

  it('asks first before jobs from public forks are turned on (UX-9)', async () => {
    const a = api().on('PATCH', '/api/v1/repositories/:id/policy', on.policy);
    renderApp(a, '/repositories/octo/app');
    await repoHeading('octo/app');
    fireEvent.click(screen.getByRole('checkbox', { name: 'Run jobs from public forks' }));
    expect(
      screen.getByText(/Jobs from public forks run code you did not write/),
    ).toBeTruthy();
    fireEvent.click(screen.getByRole('button', { name: 'Save' }));
    const dialog = await screen.findByRole('dialog', {
      name: 'Allow jobs from public forks?',
    });
    expect(a.count('PATCH', '/api/v1/repositories/7/policy')).toBe(0);
    fireEvent.click(within(dialog).getByRole('button', { name: 'Allow' }));
    await waitFor(() =>
      expect(a.count('PATCH', '/api/v1/repositories/7/policy')).toBe(1),
    );
    expect(
      (a.calls.find((c) => c.method === 'PATCH')?.body as { confirm_fork_risk: boolean })
        .confirm_fork_risk,
    ).toBe(true);
  });

  it('shows where the jobs go now and why (github-integration R5.44)', async () => {
    const hosted = repository({
      id: 9,
      full_name: 'octo/live',
      name: 'live',
      pool_on: true,
      routing: { where: 'hosted', why: 'fallback_on', oses: ['linux'], used_minutes: 12 },
    });
    const waiting = repository({
      id: 10,
      full_name: 'octo/wait',
      name: 'wait',
      pool_on: true,
      routing: {
        where: 'waiting',
        why: 'fallback_off',
        oses: ['windows'],
        used_minutes: 0,
      },
    });
    renderApp(withRepos(signedIn(), [hosted, waiting]), '/repositories');
    const table = await screen.findByRole('table', { name: 'octo repositories' });
    expect(within(table).getByText('GitHub-hosted runners')).toBeTruthy();
    expect(within(table).getByText('waiting for a machine')).toBeTruthy();
  });

  it('says why on the repository page and keeps fallback off by default', async () => {
    const wait = repository({
      pool_on: true,
      routing: {
        where: 'waiting',
        why: 'fallback_off',
        oses: ['linux'],
        used_minutes: 0,
      },
    });
    renderApp(
      withRepos(signedIn(), [wait]).on('GET', '/api/v1/repositories/:id', wait),
      '/repositories/octo/app',
    );
    await repoHeading('octo/app');
    expect(
      screen.getByText('No crsp machine for Linux is up. Jobs wait for one.'),
    ).toBeTruthy();
    const wait1 = screen.getByRole('radio', { name: 'Wait for a crsp machine' });
    expect((wait1 as HTMLInputElement).checked).toBe(true);
  });

  it('asks first before the hosted fallback is turned on (UX-9, R5.42)', async () => {
    const a = api().on('PATCH', '/api/v1/repositories/:id/policy', on.policy);
    renderApp(a, '/repositories/octo/app');
    await repoHeading('octo/app');
    fireEvent.click(screen.getByRole('radio', { name: 'Run on GitHub-hosted runners' }));
    fireEvent.click(screen.getByRole('button', { name: 'Save' }));
    const dialog = await screen.findByRole('dialog', {
      name: 'Run on GitHub-hosted runners?',
    });
    expect(within(dialog).getByText(/GitHub may bill the minutes/)).toBeTruthy();
    expect(a.count('PATCH', '/api/v1/repositories/7/policy')).toBe(0);
    fireEvent.click(within(dialog).getByRole('button', { name: 'Turn on' }));
    await waitFor(() =>
      expect(a.count('PATCH', '/api/v1/repositories/7/policy')).toBe(1),
    );
    const sent = a.calls.find((c) => c.method === 'PATCH')?.body as {
      hosted_fallback: boolean;
      confirm_fallback_cost: boolean;
    };
    expect(sent.hosted_fallback).toBe(true);
    expect(sent.confirm_fallback_cost).toBe(true);
  });

  it('refuses the fallback choice for a public repository', async () => {
    const pub = repository({ visibility: 'public' });
    renderApp(
      withRepos(signedIn(), [pub]).on('GET', '/api/v1/repositories/:id', pub),
      '/repositories/octo/app',
    );
    await repoHeading('octo/app');
    const r = screen.getByRole('radio', { name: /Run on GitHub-hosted runners/ });
    expect((r as HTMLInputElement).disabled).toBe(true);
  });

  it('has no light runner setting in the policy form (OPS49)', async () => {
    renderApp(api(), '/repositories/octo/app');
    await repoHeading('octo/app');
    expect(screen.queryByRole('checkbox', { name: /light runner/i })).toBeNull();
    expect(screen.queryByText(/RUNS_ON_LIGHT/)).toBeNull();
  });

  it('shows every mistake above its box and in a list after Save (UX-21)', async () => {
    renderApp(api(), '/repositories/octo/app');
    await repoHeading('octo/app');
    fireEvent.change(screen.getByRole('textbox', { name: 'Labels' }), {
      target: { value: '' },
    });
    fireEvent.click(screen.getByRole('button', { name: 'Save' }));
    expect(await screen.findByText('Add at least one label.')).toBeTruthy();
  });

  it('shows the extra packages and saves the list through its own endpoint (runners R5.51)', async () => {
    const a = api()
      .on(
        'GET',
        '/api/v1/repositories/:id',
        repository({
          policy: {
            ...repository().policy,
            extra_packages: ['libc++-dev', 'zlib1g-dev'],
          },
        }),
      )
      .on('PUT', '/api/v1/repositories/:id/extra-packages', {
        packages: ['zlib1g-dev'],
        text: 'zlib1g-dev',
      });
    renderApp(a, '/repositories/octo/app');
    await repoHeading('octo/app');
    const box = screen.getByRole<HTMLTextAreaElement>('textbox', {
      name: /Extra packages/,
    });
    expect(box.value).toBe('libc++-dev\nzlib1g-dev');
    fireEvent.change(box, { target: { value: 'zlib1g-dev' } });
    fireEvent.click(screen.getByRole('button', { name: 'Save' }));
    expect(await screen.findByText('Saved.')).toBeTruthy();
    expect((a.calls.find((c) => c.method === 'PUT')?.body as { text: string }).text).toBe(
      'zlib1g-dev',
    );
    expect(a.count('PATCH', '/api/v1/repositories/7/policy')).toBe(0);
  });

  it('refuses a bad package name with its line before anything is sent (runners R5.51, UX-21)', async () => {
    const a = api().on('PUT', '/api/v1/repositories/:id/extra-packages', {
      packages: [],
      text: '',
    });
    renderApp(a, '/repositories/octo/app');
    await repoHeading('octo/app');
    fireEvent.change(screen.getByRole('textbox', { name: /Extra packages/ }), {
      target: { value: 'zlib1g-dev\n# a note\n-evil' },
    });
    fireEvent.click(screen.getByRole('button', { name: 'Save' }));
    expect(
      (await screen.findAllByText(/Line 3: -evil is not a package name/)).length,
    ).toBe(2);
    expect(a.count('PUT', '/api/v1/repositories/7/extra-packages')).toBe(0);
  });

  it('shows the controller refusal when it says no (runners R5.51)', async () => {
    const a = api().on(
      'PUT',
      '/api/v1/repositories/:id/extra-packages',
      apiError(400, 'invalid', 'line 1: "a b" is not a package name'),
    );
    renderApp(a, '/repositories/octo/app');
    await repoHeading('octo/app');
    fireEvent.change(screen.getByRole('textbox', { name: /Extra packages/ }), {
      target: { value: 'ok-name' },
    });
    fireEvent.click(screen.getByRole('button', { name: 'Save' }));
    expect(await screen.findByText(/line 1: "a b" is not a package name/)).toBeTruthy();
  });

  it('shows Use pool disabled with the reason when the user lacks GitHub admin rights', async () => {
    const a = withRepos(signedIn()).on(
      'GET',
      '/api/v1/repositories/:id',
      repository({ user_is_github_admin: false }),
    );
    renderApp(a, '/repositories/octo/app');
    const button = await screen.findByRole<HTMLButtonElement>('button', {
      name: 'Use pool',
    });
    expect(button.disabled).toBe(true);
    expect(
      screen.getByText('You need admin rights on this repository in GitHub.'),
    ).toBeTruthy();
  });

  it('keeps a viewer from changing anything', async () => {
    const a = withRepos(signedIn(viewer)).on(
      'GET',
      '/api/v1/repositories/:id',
      repository(),
    );
    renderApp(a, '/repositories/octo/app');
    await repoHeading('octo/app');
    expect(screen.getByRole<HTMLButtonElement>('button', { name: 'Save' }).disabled).toBe(
      true,
    );
  });
});

describe('Webhook health (RES14)', () => {
  const polling = repository({
    id: 9,
    full_name: 'octo/slow',
    owner: 'octo',
    name: 'slow',
    pool_on: true,
    webhook: {
      mode: 'polling',
      failures_in_a_row: 3,
      last_result: 'failed',
      last_delivery_at: new Date(Date.now() - 5 * 60000).toISOString(),
      polling_since: new Date().toISOString(),
    },
  });
  const healthy = repository({
    id: 10,
    full_name: 'octo/fine',
    owner: 'octo',
    name: 'fine',
    pool_on: true,
    webhook: {
      mode: 'webhook',
      failures_in_a_row: 0,
      last_result: 'ok',
      last_delivery_at: new Date().toISOString(),
    },
  });

  it('shows the last delivery result per repository in the list', async () => {
    renderApp(withRepos(signedIn(), [polling, healthy, repository()]), '/repositories');
    const table = await screen.findByRole('table', { name: 'octo repositories' });
    expect(within(table).getByText('delivery failed')).toBeTruthy();
    expect(within(table).getByText('delivered')).toBeTruthy();
  });

  it('says plainly when the App has no webhook (OPS22)', async () => {
    const none = repository({
      id: 11,
      full_name: 'octo/nohook',
      owner: 'octo',
      name: 'nohook',
      pool_on: true,
      webhook: {
        mode: 'webhook',
        failures_in_a_row: 0,
        last_result: 'missed',
        last_delivery_at: new Date().toISOString(),
        webhooks_off: true,
      },
    });
    const a = withRepos(signedIn(), [none]).on('GET', '/api/v1/repositories/:id', none);
    renderApp(a, '/repositories/octo/nohook');
    expect(
      await screen.findByText(/has no webhook, so GitHub tells crsp nothing/),
    ).toBeTruthy();
    expect(screen.queryByText('delivery missed')).toBeNull();
    // OPS25: one link to where the owner turns webhooks on, no GitHub settings hunt.
    expect(
      screen.getByRole('link', { name: 'Turn on webhooks' }).getAttribute('href'),
    ).toBe('/settings/access#webhooks');
  });

  it('says what a missed delivery means and what to check (OPS22)', async () => {
    const missed = repository({
      id: 12,
      full_name: 'octo/late',
      owner: 'octo',
      name: 'late',
      pool_on: true,
      webhook: {
        mode: 'webhook',
        failures_in_a_row: 1,
        last_result: 'missed',
        last_delivery_at: new Date().toISOString(),
      },
    });
    const a = withRepos(signedIn(), [missed]).on(
      'GET',
      '/api/v1/repositories/:id',
      missed,
    );
    renderApp(a, '/repositories/octo/late');
    expect(await screen.findByText(/did not tell crsp about a job/)).toBeTruthy();
  });

  it('says crsp polls and switches back on a press', async () => {
    const a = withRepos(signedIn(), [polling])
      .on('GET', '/api/v1/repositories/:id', polling)
      .on('PUT', '/api/v1/repositories/:id/webhook-mode', {
        mode: 'webhook',
        failures_in_a_row: 0,
      });
    renderApp(a, '/repositories/octo/slow');
    expect(await screen.findByText(/now polls GitHub for its jobs/)).toBeTruthy();
    fireEvent.click(screen.getByRole('button', { name: 'Switch back to webhooks' }));
    await waitFor(() =>
      expect(a.count('PUT', '/api/v1/repositories/9/webhook-mode')).toBe(1),
    );
    expect(a.calls.find((c) => c.method === 'PUT')?.body).toEqual({ mode: 'webhook' });
  });

  it('draws no notice while deliveries work', async () => {
    const a = withRepos(signedIn(), [healthy]).on(
      'GET',
      '/api/v1/repositories/:id',
      healthy,
    );
    renderApp(a, '/repositories/octo/fine');
    await repoHeading('octo/fine');
    expect(screen.queryByText(/now polls GitHub/)).toBeNull();
    expect(screen.getByText('Last webhook delivery')).toBeTruthy();
  });
});

describe('A repository that left the installation (github-integration R5.22)', () => {
  const left = repository({
    id: 12,
    full_name: 'octo/gone',
    name: 'gone',
    pool_on: false,
    pool_off_reason: 'left_installation',
  });
  const reason =
    "This repository left the GitHub App's installation, so its pool was turned off.";

  it('says in the list that it left the installation', async () => {
    renderApp(withRepos(signedIn(), [left, repository()]), '/repositories');
    const table = await screen.findByRole('table', { name: 'octo repositories' });
    expect(within(table).getByText('left the installation')).toBeTruthy();
  });

  it('gives the reason on the repository page', async () => {
    const a = withRepos(signedIn(), [left]).on('GET', '/api/v1/repositories/:id', left);
    renderApp(a, '/repositories/octo/gone');
    expect(await screen.findByText(reason)).toBeTruthy();
  });

  it('gives no reason for a pool an admin turned off', async () => {
    const a = withRepos(signedIn(), [repository()]).on(
      'GET',
      '/api/v1/repositories/:id',
      repository(),
    );
    renderApp(a, '/repositories/octo/app');
    await repoHeading('octo/app');
    expect(screen.queryByText(reason)).toBeNull();
  });
});

describe('Missing App permission on Repositories (R5.86)', () => {
  const grant = {
    ready: false,
    next: 'accept',
    missing: ['secrets'],
    installation_id: 10,
    steps: [
      { id: 'install', done: true, permissions: [] },
      { id: 'ask', done: true, permissions: [] },
      {
        id: 'accept',
        done: false,
        permissions: ['secrets'],
        url: 'https://github.com/settings/installations/10',
      },
    ],
  };

  it('shows the guided grant for an installation that lacks a permission, with one action', async () => {
    const api = withRepos(signedIn())
      .on('GET', '/api/v1/github/installations', {
        items: [
          {
            id: 10,
            account_login: 'octo',
            account_type: 'User',
            suspended: false,
            repository_selection: 'selected',
            missing_permissions: ['secrets'],
          },
        ],
        stale: { stale: false },
      })
      .on('GET', '/api/v1/github/grant', grant);
    renderApp(api, '/repositories');
    const open = await screen.findByRole('button', { name: 'Accept it on GitHub' });
    expect(open.getAttribute('href')).toBe(
      'https://github.com/settings/installations/10',
    );
    expect(
      api.calls
        .find((c) => c.path === '/api/v1/github/grant')
        ?.query.get('installation_id'),
    ).toBe('10');
  });

  it('shows nothing when every installation has what the App asks for', async () => {
    const api = withRepos(signedIn()).on('GET', '/api/v1/github/installations', {
      items: [
        {
          id: 10,
          account_login: 'octo',
          account_type: 'User',
          suspended: false,
          repository_selection: 'selected',
        },
      ],
      stale: { stale: false },
    });
    renderApp(api, '/repositories');
    await screen.findByRole('table', { name: 'octo repositories' });
    await waitFor(() =>
      expect(api.count('GET', '/api/v1/github/installations')).toBeGreaterThan(0),
    );
    expect(screen.queryByRole('button', { name: /on GitHub$/ })).toBeNull();
    expect(api.count('GET', '/api/v1/github/grant')).toBe(0);
  });
});
