// R5.27, R5.28: settings sub-pages. The owner adds a user, changes a role, removes a user; there is one owner.
import { cleanup, fireEvent, screen, waitFor, within } from '@testing-library/react';
import { afterEach, describe, expect, it } from 'vitest';
import { owner, renderApp, signedIn, viewer } from '../../testing/mock';
import { section } from './testSection';

afterEach(cleanup);

const alice = {
  id: 3,
  login: 'alice',
  role: 'admin' as const,
  added_by: 'octo',
  added_at: '2026-11-03T09:00:00Z',
};

describe('Users and roles (R5.28)', () => {
  const api = () => signedIn().on('GET', '/api/v1/users', { items: [owner, alice] });

  it('lists users with role, added by and added at; the owner has no Remove', async () => {
    renderApp(api(), '/settings/users');
    const table = await screen.findByRole('table', { name: 'Users' });
    expect(within(table).getByText('alice')).toBeTruthy();
    expect(within(table).getByText('2026-11-03 09:00')).toBeTruthy();
    expect(within(table).getAllByRole('button', { name: 'Remove' })).toHaveLength(1);
    expect(within(table).getByText('The owner')).toBeTruthy();
  });

  it('adds a GitHub login with a role', async () => {
    const a = api().on('POST', '/api/v1/users', {
      id: 4,
      login: 'bob',
      role: 'viewer',
      added_at: 'x',
    });
    renderApp(a, '/settings/users');
    await screen.findByRole('table', { name: 'Users' });
    fireEvent.change(screen.getByRole('textbox', { name: 'GitHub login' }), {
      target: { value: 'bob' },
    });
    fireEvent.click(screen.getByRole('button', { name: 'Save' }));
    await waitFor(() => expect(a.count('POST', '/api/v1/users')).toBe(1));
    expect(a.calls.find((c) => c.method === 'POST')?.body).toEqual({
      login: 'bob',
      role: 'viewer',
    });
  });

  it('shows the mistake when no login is written', async () => {
    renderApp(api(), '/settings/users');
    await screen.findByRole('table', { name: 'Users' });
    fireEvent.click(screen.getByRole('radio', { name: /Admin/ }));
    fireEvent.click(screen.getByRole('button', { name: 'Save' }));
    expect(await screen.findByText('Write the GitHub login to add.')).toBeTruthy();
  });

  it('changes a role', async () => {
    const a = api().on('PATCH', '/api/v1/users/:id', { ...alice, role: 'viewer' });
    renderApp(a, '/settings/users');
    await screen.findByRole('table', { name: 'Users' });
    fireEvent.click(screen.getByRole('button', { name: 'Edit' }));
    const dialog = await screen.findByRole('dialog', { name: 'Edit alice' });
    fireEvent.click(within(dialog).getByRole('radio', { name: 'Viewer' }));
    fireEvent.click(within(dialog).getByRole('button', { name: 'Save' }));
    await waitFor(() => expect(a.count('PATCH', '/api/v1/users/3')).toBe(1));
    expect(a.calls.find((c) => c.method === 'PATCH')?.body).toEqual({ role: 'viewer' });
  });

  it('removes a user only after a question that names the login (UX-62)', async () => {
    const a = api().on('DELETE', '/api/v1/users/:id', undefined);
    renderApp(a, '/settings/users');
    await screen.findByRole('table', { name: 'Users' });
    fireEvent.click(screen.getByRole('button', { name: 'Remove' }));
    const dialog = await screen.findByRole('dialog', { name: 'Remove alice?' });
    expect(within(dialog).getByText('User alice can no longer sign in.')).toBeTruthy();
    expect(a.count('DELETE', '/api/v1/users/3')).toBe(0);
    fireEvent.click(within(dialog).getByRole('button', { name: 'Remove' }));
    await waitFor(() => expect(a.count('DELETE', '/api/v1/users/3')).toBe(1));
  });

  it('tells a non-owner that only the owner can change users', async () => {
    renderApp(signedIn(viewer), '/settings/users');
    expect(
      await screen.findByText(/Only the owner can see and change users/),
    ).toBeTruthy();
  });
});

describe('Appearance (UX-60)', () => {
  it('saves the theme at once and applies it', async () => {
    const a = signedIn().on('PUT', '/api/v1/me/preferences', (c) => c.body);
    renderApp(a, '/settings/appearance');
    const light = await screen.findByRole('radio', { name: 'Light' });
    fireEvent.click(light);
    await waitFor(() => expect(a.count('PUT', '/api/v1/me/preferences')).toBe(1));
    expect(a.calls.find((c) => c.method === 'PUT')?.body).toEqual({ theme: 'light' });
    await waitFor(() =>
      expect(document.documentElement.getAttribute('data-theme')).toBe('light'),
    );
  });
});

describe('Colour themes (UX-60)', () => {
  it('has one Theme group of nine cards, with no Look and no Palette, and Phosphor by default', async () => {
    window.localStorage.clear();
    document.documentElement.removeAttribute('data-theme-name');
    renderApp(signedIn(), '/settings/appearance');
    const group = await screen.findByRole('group', { name: 'Theme' });
    expect(screen.queryByRole('group', { name: 'Look' })).toBeNull();
    expect(screen.queryByRole('group', { name: 'Palette' })).toBeNull();
    const names = within(group)
      .getAllByRole('radio')
      .map((r) => (r as HTMLInputElement).value);
    expect(names).toEqual([
      'phosphor',
      'tokyo-night',
      'catppuccin',
      'gruvbox',
      'solarized',
      'nord',
      'terminal-green',
      'terminal-amber',
      'terminal-ice',
    ]);
    // Every card, the three Terminal ones too, shows a dark and a light sample (CRSP371).
    expect(group.querySelectorAll('.ds-theme-preview')).toHaveLength(18);
    expect(within(group).getByRole('img', { name: 'Nord, light' })).toBeTruthy();
    const amberLight = within(group).getByRole('img', { name: 'Terminal Amber, light' });
    expect(amberLight.getAttribute('data-pal')).toBe('amber');
    expect(amberLight.getAttribute('data-theme')).toBe('light');
    const amber = within(group).getByRole('img', { name: 'Terminal Amber, dark' });
    expect(amber.getAttribute('data-theme-name')).toBe('terminal');
    expect(amber.getAttribute('data-pal')).toBe('amber');
    expect(
      within(group).getByRole<HTMLInputElement>('radio', { name: /^Phosphor/ }).checked,
    ).toBe(true);
  });

  it('applies a Terminal card at once: theme terminal and its palette', async () => {
    window.localStorage.clear();
    document.documentElement.removeAttribute('data-pal');
    renderApp(signedIn(), '/settings/appearance');
    fireEvent.click(await screen.findByRole('radio', { name: /^Terminal Amber/ }));
    expect(document.documentElement.getAttribute('data-theme-name')).toBe('terminal');
    expect(document.documentElement.getAttribute('data-pal')).toBe('amber');
    expect(window.localStorage.getItem('crsp.theme-palette')).toBe('amber');
    fireEvent.click(screen.getByRole('radio', { name: /^Terminal Green/ }));
    expect(document.documentElement.hasAttribute('data-pal')).toBe(false);
    expect(document.documentElement.getAttribute('data-theme-name')).toBe('terminal');
    fireEvent.click(screen.getByRole('radio', { name: /^Gruvbox/ }));
    expect(document.documentElement.getAttribute('data-theme-name')).toBe('gruvbox');
  });

  it('migrates the old Look and Palette: terminal with a palette selects that Terminal card', async () => {
    window.localStorage.clear();
    window.localStorage.setItem('crsp.theme-name', 'terminal');
    window.localStorage.setItem('crsp.theme-palette', 'ice');
    renderApp(signedIn(), '/settings/appearance');
    expect(
      (await screen.findByRole<HTMLInputElement>('radio', { name: /^Terminal Ice/ }))
        .checked,
    ).toBe(true);
    expect(document.documentElement.getAttribute('data-pal')).toBe('ice');
  });

  it('keeps a saved colour theme as it was', async () => {
    window.localStorage.clear();
    window.localStorage.setItem('crsp.theme-name', 'nord');
    window.localStorage.setItem('crsp.theme-palette', 'amber');
    renderApp(signedIn(), '/settings/appearance');
    expect(
      (await screen.findByRole<HTMLInputElement>('radio', { name: /^Nord/ })).checked,
    ).toBe(true);
  });

  it('offers the graph style as its own setting for Terminal only and keeps it in this browser', async () => {
    window.localStorage.clear();
    document.documentElement.removeAttribute('data-graphs');
    renderApp(signedIn(), '/settings/appearance');
    await screen.findByRole('group', { name: 'Theme' });
    expect(screen.queryByRole('group', { name: 'Graphs' })).toBeNull();
    fireEvent.click(screen.getByRole('radio', { name: /^Terminal Green/ }));
    const graphs = screen.getByRole('group', { name: 'Graphs' });
    fireEvent.click(within(graphs).getByRole('radio', { name: /^Dots/ }));
    expect(document.documentElement.getAttribute('data-graphs')).toBe('dotted');
    expect(window.localStorage.getItem('crsp.theme-graphs')).toBe('dotted');
    fireEvent.click(within(graphs).getByRole('radio', { name: /^Blocks/ }));
    expect(document.documentElement.hasAttribute('data-graphs')).toBe(false);
    fireEvent.click(screen.getByRole('radio', { name: /^Nord/ }));
    expect(screen.queryByRole('group', { name: 'Graphs' })).toBeNull();
  });

  it('picks a theme, applies it at once and keeps it in this browser', async () => {
    window.localStorage.clear();
    renderApp(signedIn(), '/settings/appearance');
    fireEvent.click(await screen.findByRole('radio', { name: /^Nord/ }));
    expect(document.documentElement.getAttribute('data-theme-name')).toBe('nord');
    expect(window.localStorage.getItem('crsp.theme-name')).toBe('nord');
    expect(screen.getByRole<HTMLInputElement>('radio', { name: /^Nord/ }).checked).toBe(
      true,
    );
  });

  it('applies the saved theme on load, and the mode choice stays separate', async () => {
    window.localStorage.setItem('crsp.theme-name', 'gruvbox');
    document.documentElement.removeAttribute('data-theme-name');
    renderApp(signedIn(), '/settings/appearance');
    await screen.findByRole('radio', { name: /^Gruvbox/ });
    expect(document.documentElement.getAttribute('data-theme-name')).toBe('gruvbox');
    expect(screen.getByRole('group', { name: 'Mode' })).toBeTruthy();
  });
});

describe('Retention and backups', () => {
  it('saves retention', async () => {
    const a = signedIn()
      .on('GET', '/api/v1/settings', { retention_job_history_days: 90 })
      .on('PATCH', '/api/v1/settings', (c) => c.body);
    renderApp(a, '/settings/retention');
    await screen.findByRole('heading', { name: 'Retention' });
    await screen.findByText('Job history');
    expect(screen.getByRole<HTMLButtonElement>('button', { name: 'Save' }).disabled).toBe(
      true,
    );
    fireEvent.click(section('retention').getByRole('button', { name: /^Job history/ }));
    const keypad = await screen.findByRole('dialog');
    fireEvent.click(within(keypad).getByRole('button', { name: '5' }));
    fireEvent.click(within(keypad).getByRole('button', { name: 'Done' }));
    expect(await screen.findByText('Unsaved changes (1)')).toBeTruthy();
    fireEvent.click(screen.getByRole('button', { name: 'Save' }));
    await waitFor(() => expect(a.count('PATCH', '/api/v1/settings')).toBe(1));
    expect(
      (
        a.calls.find((c) => c.method === 'PATCH')?.body as {
          retention_job_history_days: number;
        }
      ).retention_job_history_days,
    ).not.toBe(90);
    expect(await screen.findByText('Retention saved.')).toBeTruthy();
  });

  it('lists backups and makes one on demand', async () => {
    const a = signedIn()
      .on('GET', '/api/v1/settings', {})
      .on('GET', '/api/v1/backups', {
        items: [
          {
            id: 'b1',
            file: 'crsp-2026-11-14.db',
            created_at: '2026-11-14T03:00:00Z',
            size_bytes: 4200000,
            state: 'good',
          },
        ],
      })
      .on('POST', '/api/v1/backups', {
        id: 'b2',
        file: 'x',
        created_at: 'x',
        state: 'writing',
      });
    renderApp(a, '/settings/backups');
    const table = await screen.findByRole('table', { name: 'Backups' });
    expect(within(table).getByText('4.2 MB')).toBeTruthy();
    expect(within(table).getByText('ok')).toBeTruthy();
    fireEvent.click(screen.getByRole('button', { name: 'Back up now' }));
    await waitFor(() => expect(a.count('POST', '/api/v1/backups')).toBe(1));
  });

  it('says what to do when there is no backup', async () => {
    const a = signedIn()
      .on('GET', '/api/v1/settings', {})
      .on('GET', '/api/v1/backups', { items: [] });
    renderApp(a, '/settings/backups');
    expect(
      await screen.findByText('No backups yet. Back up now to make the first one.'),
    ).toBeTruthy();
  });
});
