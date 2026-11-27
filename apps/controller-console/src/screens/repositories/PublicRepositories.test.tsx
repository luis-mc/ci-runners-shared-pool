// CRSP131, web-console R5.68 to R5.70: the setting "Enable for public repositories", the active-types line,
// the Visibility column, greyed public rows and the Problem of a public repository on the pool while it is off.
import { cleanup, fireEvent, screen, waitFor, within } from '@testing-library/react';
import { afterEach, describe, expect, it } from 'vitest';
import { MockApi, renderApp, repository, signedIn } from '../../testing/mock';

afterEach(cleanup);

const setting = (api: MockApi, enabled: boolean, publicOnPool = 0): MockApi =>
  api.on('GET', '/api/v1/settings/public-repositories', {
    enabled,
    public_on_pool: publicOnPool,
  });

const priv = repository({ id: 7, full_name: 'octo/app', name: 'app' });
const pub = repository({
  id: 8,
  full_name: 'octo/site',
  name: 'site',
  visibility: 'public',
});
const list = (api: MockApi, items = [priv, pub]): MockApi =>
  api.on('GET', '/api/v1/github/repositories', { items, stale: { stale: false } });

describe('Enable for public repositories (settings)', () => {
  it('is off by default and turning it on asks first with the impact in plain words', async () => {
    const api = setting(signedIn(), false).on(
      'PUT',
      '/api/v1/settings/public-repositories',
      { enabled: true, public_on_pool: 0, stopped: 0 },
    );
    renderApp(api, '/settings/repositories');
    const sw = await screen.findByRole('switch', {
      name: 'Enable for public repositories',
    });
    expect(sw.getAttribute('aria-checked')).toBe('false');
    fireEvent.click(sw);
    const dialog = await screen.findByRole('dialog', { name: 'Are you sure?' });
    expect(
      within(dialog).getByText('Anyone can open a pull request on a public repository.'),
    ).toBeTruthy();
    expect(
      within(dialog).getByText('A self-hosted runner runs that code on your machines.'),
    ).toBeTruthy();
    expect(within(dialog).getByText('Jobs from forks stay refused.')).toBeTruthy();
    expect(
      within(dialog).getByText(
        'Secrets, caches and files on the machine could be reached.',
      ),
    ).toBeTruthy();
    expect(
      within(dialog).getByText('Keeping your repositories private is safer.'),
    ).toBeTruthy();
    expect(api.count('PUT', '/api/v1/settings/public-repositories')).toBe(0);
    fireEvent.click(within(dialog).getByRole('button', { name: 'Turn on' }));
    await waitFor(() =>
      expect(api.count('PUT', '/api/v1/settings/public-repositories')).toBe(1),
    );
    expect(api.calls.find((c) => c.method === 'PUT')?.body).toEqual({ enabled: true });
  });

  it('cancel changes nothing', async () => {
    const api = setting(signedIn(), false);
    renderApp(api, '/settings/repositories');
    fireEvent.click(
      await screen.findByRole('switch', { name: 'Enable for public repositories' }),
    );
    const dialog = await screen.findByRole('dialog', { name: 'Are you sure?' });
    fireEvent.click(within(dialog).getByRole('button', { name: 'Cancel' }));
    expect(screen.queryByRole('dialog')).toBeNull();
    expect(api.count('PUT', '/api/v1/settings/public-repositories')).toBe(0);
  });

  it('turning it off names how many repositories have their pool switched off', async () => {
    const api = setting(signedIn(), true, 3).on(
      'PUT',
      '/api/v1/settings/public-repositories',
      { enabled: false, public_on_pool: 0, stopped: 3 },
    );
    renderApp(api, '/settings/repositories');
    const sw = await screen.findByRole('switch', {
      name: 'Enable for public repositories',
    });
    expect(sw.getAttribute('aria-checked')).toBe('true');
    fireEvent.click(sw);
    const dialog = await screen.findByRole('dialog', { name: 'Are you sure?' });
    expect(
      within(dialog).getByText(/3 public repositories use the pool now/),
    ).toBeTruthy();
    expect(within(dialog).getByText(/drained and removed/)).toBeTruthy();
    expect(within(dialog).getByText(/The settings are kept/)).toBeTruthy();
    fireEvent.click(within(dialog).getByRole('button', { name: 'Turn off' }));
    expect(
      await screen.findByText(/switched off for 3 public repositories/),
    ).toBeTruthy();
    expect(api.calls.find((c) => c.method === 'PUT')?.body).toEqual({ enabled: false });
  });

  it('is locked with the reason for a user who is not the owner', async () => {
    const api = setting(
      signedIn({ id: 2, login: 'vic', role: 'viewer', added_at: 'x' }),
      false,
    );
    renderApp(api, '/settings/repositories');
    const sw = await screen.findByRole<HTMLButtonElement>('switch', {
      name: 'Enable for public repositories',
    });
    expect(sw.disabled).toBe(true);
  });
});

describe('Repositories: active types, Visibility and greyed public rows', () => {
  it('shows the active types as a chip in the toolbar, not a link, and the chip opens the setting (UX-8)', async () => {
    renderApp(list(setting(signedIn(), false)), '/setup/pool');
    const chip = await screen.findByRole('button', {
      name: 'Types: Private (default)',
    });
    expect(screen.queryByRole('link', { name: /Private \(default\)/ })).toBeNull();
    expect(
      within(screen.getByRole('group', { name: 'Repositories toolbar' })).getByRole(
        'button',
        { name: /Types/ },
      ),
    ).toBe(chip);
    fireEvent.click(chip);
    expect(
      await screen.findByRole('heading', { level: 1, name: 'Settings' }),
    ).toBeTruthy();
  });

  it('shows Private & Public when the setting is on', async () => {
    renderApp(list(setting(signedIn(), true)), '/setup/pool');
    expect(
      await screen.findByRole('button', { name: 'Types: Private & Public' }),
    ).toBeTruthy();
  });

  it('tags a public row with its visibility; public rows are greyed without a repeated note while it is off', async () => {
    renderApp(list(setting(signedIn(), false)), '/setup/pool?public=1');
    const table = await screen.findByRole('table', { name: 'octo repositories' });
    expect(within(table).queryByRole('columnheader', { name: 'Visibility' })).toBeNull();
    await screen.findByRole('button', { name: 'Types: Private (default)' });
    const rows = within(table).getAllByRole('row');
    const siteRow = rows.find((r) => within(r).queryByRole('link', { name: 'site' }));
    const appRow = rows.find((r) => within(r).queryByRole('link', { name: 'app' }));
    expect(siteRow?.getAttribute('data-inactive')).toBe('true');
    expect(appRow?.getAttribute('data-inactive')).toBeNull();
    expect(within(siteRow as HTMLElement).getByText('Public')).toBeTruthy();
    expect(within(siteRow as HTMLElement).getByText('inactive type')).toBeTruthy();
    expect(within(appRow as HTMLElement).queryByText('Private')).toBeNull();
    expect(within(appRow as HTMLElement).queryByText('inactive type')).toBeNull();
    expect(within(table).queryByText(/Public repositories are off/)).toBeNull();
    expect(
      within(table).queryByRole('link', { name: 'Turn on public repositories' }),
    ).toBeNull();
  });

  it('does not grey public rows while the setting is on', async () => {
    renderApp(list(setting(signedIn(), true)), '/setup/pool');
    const table = await screen.findByRole('table', { name: 'octo repositories' });
    await screen.findByRole('button', { name: 'Types: Private & Public' });
    for (const row of within(table).getAllByRole('row'))
      expect(row.getAttribute('data-inactive')).toBeNull();
  });

  it('disables Turn on for a selection that holds a public repository while off, with the reason', async () => {
    renderApp(list(setting(signedIn(), false)), '/setup/pool?public=1');
    await screen.findByRole('button', { name: 'Types: Private (default)' });
    fireEvent.click(screen.getByRole('checkbox', { name: 'Select octo/site' }));
    const button = screen.getByRole<HTMLButtonElement>('button', { name: 'Use pool' });
    expect(button.disabled).toBe(true);
    expect(screen.getByText(/Public repositories are off: octo\/site/)).toBeTruthy();
  });

  it('shows the Problem of a public repository on the pool while off, with its two actions', async () => {
    const problem = {
      code: 'public_pool_on_while_off' as const,
      text: 'This public repository still uses the pool, but public repositories are off.',
      actions: [
        {
          id: 'turn_on_public_repositories' as const,
          label: 'Turn on public repositories',
        },
        { id: 'turn_off_pool' as const, label: 'Turn off the pool' },
      ],
    };
    const bad = { ...pub, pool_on: true, problem };
    const api = setting(signedIn(), false).on('GET', '/api/v1/repositories/:id', bad);
    renderApp(list(api, [priv, bad]), '/setup/pool/octo/site');
    const alert = await screen.findByRole('alert');
    expect(within(alert).getByText(problem.text)).toBeTruthy();
    expect(
      within(alert).getByRole('button', { name: 'Turn on public repositories' }),
    ).toBeTruthy();
    expect(within(alert).getByRole('button', { name: 'Turn off the pool' })).toBeTruthy();
    fireEvent.click(
      within(alert).getByRole('button', { name: 'Turn on public repositories' }),
    );
    expect(await screen.findByRole('dialog', { name: 'Are you sure?' })).toBeTruthy();
  });
});

describe('Turn on the pool and Add runners while public repositories are off', () => {
  it('Turn on the pool does not list a public repository and says why, with a link', async () => {
    renderApp(list(setting(signedIn(), false)), '/setup/pool/turn-on');
    expect(await screen.findByText(/1 public repository is not listed/)).toBeTruthy();
    expect(screen.getByRole('button', { name: 'Open the setting' })).toBeTruthy();
    expect(screen.getByRole('checkbox', { name: 'Select octo/app' })).toBeTruthy();
    expect(screen.queryByRole('checkbox', { name: 'Select octo/site' })).toBeNull();
  });

  it('Add runners names no repository, so the server refuses a public one while off (D31)', async () => {
    const onPool = { ...pub, pool_on: true };
    renderApp(list(setting(signedIn(), false), [onPool]), '/setup/runners/add');
    await screen.findByRole('textbox', { name: /Labels/ });
    expect(screen.queryByRole('radio', { name: /octo\/site/ })).toBeNull();
  });
});
