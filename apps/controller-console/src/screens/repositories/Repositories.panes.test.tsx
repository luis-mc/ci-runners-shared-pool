// CRSP162 (CORE-1..7, UX-4, UX-8, UX-12, UX-17): every part of the repository screens sits in a pane, each screen
// has one primary, the failed state keeps its h1, and the detail page has one Save.
import { cleanup, fireEvent, screen, waitFor, within } from '@testing-library/react';
import { afterEach, describe, expect, it } from 'vitest';
import {
  apiError,
  renderApp,
  repository,
  signedIn,
  type MockApi,
} from '../../testing/mock';

afterEach(cleanup);

const list = (api: MockApi, items = [repository()]) =>
  api.on('GET', '/api/v1/github/repositories', { items, stale: { stale: false } });

const primaries = () =>
  [...document.querySelectorAll<HTMLElement>('[data-kind="primary"]')].map(
    (b) => b.textContent,
  );

/** The repository page draws its title while it loads (RES21 B18): wait for the data, not for the title. */
async function repoHeading(name: string) {
  const heading = await screen.findByRole('heading', { level: 1, name });
  await waitFor(() => expect(screen.queryByText(/Loading the repository/)).toBeNull());
  return heading;
}

describe('Repositories list in a pane', () => {
  it('draws the toolbar and the three groups as panes, one card per repository (CORE-1)', async () => {
    renderApp(list(signedIn()), '/setup/pool');
    const group = await screen.findByRole('region', { name: /^Not using the pool/ });
    expect(group.closest('.ds-pane')).toBeTruthy();
    expect(
      within(group).getByRole('heading', { level: 2, name: /^Not using the pool/ }),
    ).toBeTruthy();
    expect(within(group).getByRole('region', { name: /^octo[/]app/ })).toBeTruthy();
    expect(screen.getByRole('group', { name: 'Repositories toolbar' })).toBeTruthy();
    expect(screen.getByRole('textbox', { name: 'Search repositories' })).toBeTruthy();
  });

  it('has no primary button outside the action bar, also while cards are ticked (UX-4, UX-71)', async () => {
    renderApp(list(signedIn()), '/setup/pool');
    await screen.findByRole('region', { name: /^Not using the pool/ });
    expect(primaries()).toEqual([]);
    fireEvent.click(screen.getByRole('checkbox', { name: 'Select octo/app' }));
    expect(screen.getByRole('button', { name: 'Turn on 1' })).toBeTruthy();
    expect(primaries()).toEqual([]);
  });

  it('keeps a locked action with its reason until a card is ticked (UX-12)', async () => {
    renderApp(list(signedIn()), '/setup/pool');
    await screen.findByRole('region', { name: /^Not using the pool/ });
    expect(
      screen.getByRole<HTMLButtonElement>('button', { name: 'Turn on' }).disabled,
    ).toBe(true);
    expect(screen.getAllByText('Tick at least one repository.').length).toBeGreaterThan(
      0,
    );
  });

  it('shows the health tiles above the groups', async () => {
    renderApp(list(signedIn()), '/setup/pool');
    await screen.findByRole('region', { name: /^Not using the pool/ });
    for (const t of ['Machines', 'Runners', 'GitHub App', 'Repositories'])
      expect(screen.getAllByText(t).length).toBeGreaterThan(0);
    expect(screen.getByText('0 of 1')).toBeTruthy();
  });

  it('keeps the pane when the controller fails (UX-28)', async () => {
    renderApp(
      signedIn().on(
        'GET',
        '/api/v1/github/repositories',
        apiError(500, 'internal', 'down'),
      ),
      '/setup/pool',
    );
    expect(await screen.findByText(/Could not load repositories/)).toBeTruthy();
    expect(screen.getByRole('heading', { level: 1, name: 'Setup' })).toBeTruthy();
  });
});

describe('Repository detail in panes', () => {
  const detail = (r = repository()) =>
    list(signedIn(), [r]).on('GET', '/api/v1/repositories/:id', r);

  it('keeps the h1 when the repository cannot be loaded (UX-17)', async () => {
    renderApp(list(signedIn(), []), '/setup/pool/octo/app');
    expect(await screen.findByText(/Could not load the repository/)).toBeTruthy();
    expect(screen.getByRole('heading', { level: 1, name: 'octo/app' })).toBeTruthy();
  });

  it('puts State, Policy, Hosted fallback and Extra packages in panes', async () => {
    renderApp(detail(), '/setup/pool/octo/app');
    await repoHeading('octo/app');
    for (const t of ['State', 'Policy', 'Hosted fallback', 'Extra packages'])
      expect(screen.getByRole('heading', { level: 2, name: t })).toBeTruthy();
  });

  it('has one Save and one primary (UX-4, UX-24)', async () => {
    renderApp(detail(), '/setup/pool/octo/app');
    await repoHeading('octo/app');
    expect(screen.getAllByRole('button', { name: /^Save/ })).toHaveLength(1);
    // The action bar holds the one primary: Save (UX-4, UX-71).
    expect(primaries()).toEqual(['Save']);
  });

  it('keeps Save the one primary also once the pool is on, in the action bar', async () => {
    renderApp(detail(repository({ pool_on: true })), '/setup/pool/octo/app');
    await repoHeading('octo/app');
    expect(primaries()).toEqual(['Save']);
    const bar = screen.getByRole('region', { name: 'Actions' });
    expect(within(bar).getByRole('button', { name: 'Save' })).toBeTruthy();
    expect(within(bar).getByRole('button', { name: 'Cancel' })).toBeTruthy();
  });

  it('saves the policy, the fallback and the packages with one Save, asking once', async () => {
    const r = repository();
    const a = detail(r)
      .on('PATCH', '/api/v1/repositories/:id/policy', r.policy)
      .on('PUT', '/api/v1/repositories/:id/extra-packages', {
        packages: ['zlib1g-dev'],
        text: 'zlib1g-dev',
      });
    renderApp(a, '/setup/pool/octo/app');
    await repoHeading('octo/app');
    fireEvent.click(screen.getByRole('checkbox', { name: 'Run jobs from public forks' }));
    fireEvent.click(screen.getByRole('radio', { name: 'Run on GitHub-hosted runners' }));
    fireEvent.change(screen.getByRole('textbox', { name: /Extra packages/ }), {
      target: { value: 'zlib1g-dev' },
    });
    fireEvent.click(screen.getByRole('button', { name: 'Save' }));
    const dialog = await screen.findByRole('dialog', { name: 'Save these changes?' });
    expect(within(dialog).getByText(/public forks run on your machines/)).toBeTruthy();
    expect(within(dialog).getByText(/GitHub may bill/)).toBeTruthy();
    expect(a.count('PATCH', '/api/v1/repositories/7/policy')).toBe(0);
    fireEvent.click(within(dialog).getByRole('button', { name: 'Save' }));
    await waitFor(() =>
      expect(a.count('PUT', '/api/v1/repositories/7/extra-packages')).toBe(1),
    );
    expect(a.count('PATCH', '/api/v1/repositories/7/policy')).toBe(1);
    const sent = a.calls.find((c) => c.method === 'PATCH')?.body as Record<
      string,
      unknown
    >;
    expect(sent['allow_fork_pull_requests']).toBe(true);
    expect(sent['hosted_fallback']).toBe(true);
    expect(sent['confirm_fork_risk']).toBe(true);
    expect(sent['confirm_fallback_cost']).toBe(true);
  });

  it('disables Save with its reason while clean, and counts the unsaved changes', async () => {
    const a = detail();
    renderApp(a, '/setup/pool/octo/app');
    await repoHeading('octo/app');
    const save = screen.getByRole<HTMLButtonElement>('button', { name: 'Save' });
    expect(save.disabled).toBe(true);
    expect(screen.getByText('Nothing has changed yet.')).toBeTruthy();
    fireEvent.click(screen.getByRole('checkbox', { name: 'Run jobs from public forks' }));
    expect(screen.getByText('Unsaved changes (1)')).toBeTruthy();
    expect(screen.getByRole<HTMLButtonElement>('button', { name: 'Save' }).disabled).toBe(
      false,
    );
    expect(
      a.calls.filter((c) => c.method === 'PATCH' || c.method === 'PUT'),
    ).toHaveLength(0);
  });
});

describe('Turn on the pool in panes', () => {
  const wizard = (items: ReturnType<typeof repository>[]) => list(signedIn(), items);

  it('puts the choice in a pane with the choosing buttons and one primary', async () => {
    renderApp(wizard([repository()]), '/setup/pool/turn-on');
    const pane = (
      await screen.findByRole('heading', { level: 2, name: /Repositories to turn on/ })
    ).closest('.ds-pane') as HTMLElement;
    expect(
      within(pane).getByRole('table', { name: 'Repositories to turn on' }),
    ).toBeTruthy();
    expect(within(pane).getByRole('button', { name: 'Select all 1' })).toBeTruthy();
    expect(within(pane).getByRole('button', { name: 'Clear' })).toBeTruthy();
    expect(primaries()).toEqual(['Next']);
  });

  it('names the admin rights as the next action when every repository is blocked', async () => {
    renderApp(
      wizard([repository({ user_is_github_admin: false })]),
      '/setup/pool/turn-on',
    );
    expect(
      await screen.findByText(
        /No repository is free for you to turn on. One needs admin rights/,
      ),
    ).toBeTruthy();
    expect(screen.getByRole('button', { name: 'Add a repository' })).toBeTruthy();
    expect(primaries()).toEqual(['Next']);
  });

  it('never says admin rights are missing when the flag was not read (OPS15)', async () => {
    renderApp(
      wizard([repository({ user_is_github_admin: false, user_admin_known: false })]),
      '/setup/pool/turn-on',
    );
    expect(
      await screen.findByRole('table', { name: 'Repositories to turn on' }),
    ).toBeTruthy();
    expect(screen.queryByText(/admin rights/)).toBeNull();
    expect(screen.queryByText(/No repository is free for you/)).toBeNull();
  });

  it('names the setting as the next action when only public repositories are left and they are off', async () => {
    renderApp(
      wizard([repository({ visibility: 'public' })]).on(
        'GET',
        '/api/v1/settings/public-repositories',
        { enabled: false, public_on_pool: 0 },
      ),
      '/setup/pool/turn-on',
    );
    expect(await screen.findByText(/the ones left are public/)).toBeTruthy();
    expect(screen.getByRole('button', { name: 'Open the setting' })).toBeTruthy();
  });

  it('says the App must be installed when nothing is waiting', async () => {
    renderApp(wizard([]), '/setup/pool/turn-on');
    expect(await screen.findByText(/No repository is waiting/)).toBeTruthy();
    expect(screen.getByRole('button', { name: 'Add a repository' })).toBeTruthy();
  });
});
