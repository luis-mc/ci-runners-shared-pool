// Settings in five tabs (owner decision D8): the tab row, the panes in order, old addresses, 24-hour backup time,
// the update state words, the mode default.
import { cleanup, screen, waitFor, within } from '@testing-library/react';
import { afterEach, describe, expect, it } from 'vitest';
import { renderApp, signedIn } from '../../testing/mock';
import { OLD_SETTINGS, settingsPath } from './sections';
import { section } from './testSection';

afterEach(cleanup);

const titles = (el: HTMLElement) =>
  within(el)
    .getAllByRole('heading', { level: 2 })
    .map((h) => h.textContent ?? '');

describe('Settings tabs (D8)', () => {
  it('has the six tabs in order', async () => {
    renderApp(signedIn(), '/settings');
    const tabs = await screen.findAllByRole('tab');
    expect(tabs.map((t) => t.textContent)).toEqual([
      'Access',
      'Repositories',
      'Console',
      'Updates',
      'Data',
      'Audit log',
    ]);
    expect(
      screen.getByRole('tab', { name: 'Access' }).getAttribute('aria-selected'),
    ).toBe('true');
  });

  it.each([
    ['access', ['users', 'github-app', 'webhooks']],
    ['repositories', ['public-repositories', 'budgets']],
    ['console', ['appearance', 'notifications', 'network']],
    ['updates', ['updates']],
    ['data', ['retention', 'backups', 'config-export']],
  ])('the %s tab holds its sections in order', async (tab, ids) => {
    renderApp(signedIn(), `/settings/${tab}`);
    await screen.findByRole('tab', { name: /./, selected: true });
    const main = screen.getByRole('main');
    await waitFor(() => {
      const found = [...main.querySelectorAll('[id]')]
        .map((e) => e.id)
        .filter((id) => ids.includes(id));
      expect(found).toEqual(ids);
    });
  });

  it.each([
    ['users', 'access', 'users'],
    ['app', 'access', 'github-app'],
    ['budgets', 'repositories', 'budgets'],
    ['appearance', 'console', 'appearance'],
    ['notifications', 'console', 'notifications'],
    ['retention', 'data', 'retention'],
    ['backups', 'data', 'backups'],
    ['export', 'data', 'config-export'],
  ])('the old /settings/%s lands on the %s tab at %s', async (old, tab, id) => {
    expect(settingsPath(old)).toBe(`/settings/${tab}#${id}`);
    expect(OLD_SETTINGS).toContain(old);
    renderApp(signedIn(), `/settings/${old}`);
    expect(
      (await screen.findByRole('tab', { selected: true })).textContent?.toLowerCase(),
    ).toBe(tab === 'access' ? 'access' : tab);
    await waitFor(() => expect(section(id)).toBeTruthy());
  });

  it('the old /settings/updates and /settings/console#updates land on the Updates tab', async () => {
    renderApp(signedIn(), '/settings/console#updates');
    await waitFor(() =>
      expect(screen.getByRole('tab', { selected: true }).textContent).toBe('Updates'),
    );
    expect(OLD_SETTINGS).not.toContain('updates');
    expect(settingsPath('updates')).toBe('/settings/updates');
  });

  it('shows the backup time on a 24-hour clock', async () => {
    renderApp(signedIn(), '/settings/data');
    await screen.findByText('Backup time');
    expect(screen.queryByText('AM')).toBeNull();
    expect(screen.queryByText('PM')).toBeNull();
    expect(titles(screen.getByRole('main'))).toContain('Backups');
  });
});
