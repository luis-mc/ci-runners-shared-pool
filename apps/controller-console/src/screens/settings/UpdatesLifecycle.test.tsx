// Lifecycle UI: Settings > Updates with the shared parts, against the contract's /api/v1/update endpoints (mocked
// until the controller serves them).
import { cleanup, fireEvent, screen, waitFor, within } from '@testing-library/react';
import { afterEach, describe, expect, it } from 'vitest';
import { renderApp, signedIn } from '../../testing/mock';

afterEach(cleanup);

const status = (extra: Record<string, unknown> = {}) => ({
  state: 'up_to_date',
  current: '1.4.0',
  available: null,
  required: false,
  policy: 'automatic_when_idle',
  window: null,
  next_check_at: null,
  failures: null,
  waiting_for_jobs: false,
  notes: null,
  updated: null,
  ...extra,
});

const api = (s: Record<string, unknown> = status()) =>
  signedIn()
    .on('GET', '/api/v1/update/status', s)
    .on('GET', '/api/v1/update/history', () => [
      {
        at: '2026-11-21T10:00:00Z',
        actor: 'luis',
        action: 'install',
        version: '1.4.0',
        result: 'ok',
      },
    ])
    .on('GET', '/api/v1/update/fleet', () => [
      { machine_id: 'm2', name: 'build-2', version: '1.3.0', state: 'needs_you' },
    ])
    .on('POST', '/api/v1/update/policy', {})
    .on('POST', '/api/v1/update/check', {})
    .on('POST', '/api/v1/update/install', {})
    .on('POST', '/api/v1/remove', {});

describe('Settings > Updates (lifecycle)', () => {
  it('shows the Updates card, the last check and the machines, no history table (CRSP368)', async () => {
    renderApp(
      api(
        status({
          last_check: { at: '2026-11-27T13:49:13.637Z', by: 'luis-mc', kind: 'manual' },
        }),
      ),
      '/settings/updates',
    );
    expect(await screen.findByRole('region', { name: 'Updates' })).toBeTruthy();
    expect(
      await screen.findByRole('radio', { name: /Automatic when idle/ }),
    ).toBeTruthy();
    expect(screen.getByText('Up to date. Version 1.4.0 is the newest.')).toBeTruthy();
    const line = await screen.findByText(/^Last checked .*, by luis-mc \(manual\)\.$/);
    expect(line.textContent).not.toMatch(/T\d\d:\d\d|Z$/);
    expect(screen.queryByRole('table', { name: 'Update history' })).toBeNull();
    expect(
      screen.getByRole('link', { name: 'See all in the Audit log' }).getAttribute('href'),
    ).toBe('/settings/audit');
    expect(await screen.findByText('build-2')).toBeTruthy();
    expect(screen.getByText('Needs you')).toBeTruthy();
  });

  it('saves the policy at once', async () => {
    const a = api();
    renderApp(a, '/settings/updates');
    fireEvent.click(await screen.findByRole('radio', { name: /Ask first/ }));
    await waitFor(() => expect(a.count('POST', '/api/v1/update/policy')).toBe(1));
    expect(a.calls.find((c) => c.path === '/api/v1/update/policy')?.body).toEqual({
      policy: 'ask_first',
      window: null,
    });
  });

  it('saves a time window once both times are right', async () => {
    const a = api();
    renderApp(a, '/settings/updates');
    fireEvent.click(await screen.findByRole('button', { name: 'Choose a From time' }));
    expect(a.count('POST', '/api/v1/update/policy')).toBe(0);
    fireEvent.click(screen.getByRole('button', { name: 'Choose a Until time' }));
    const hours = screen.getAllByRole('spinbutton', { name: 'Hours' });
    fireEvent.keyDown(hours[0] as HTMLElement, { key: 'ArrowUp' });
    fireEvent.keyDown(hours[1] as HTMLElement, { key: 'ArrowUp' });
    for (let i = 0; i < 4; i++)
      fireEvent.keyDown(hours[1] as HTMLElement, { key: 'ArrowUp' });
    await waitFor(() =>
      expect(a.count('POST', '/api/v1/update/policy')).toBeGreaterThan(1),
    );
    expect(
      a.calls.filter((c) => c.path === '/api/v1/update/policy').at(-1)?.body,
    ).toEqual({
      policy: 'automatic_when_idle',
      window: { start: '01:00', end: '05:00' },
    });
  });

  it('Check asks the controller to look', async () => {
    const a = api();
    renderApp(a, '/settings/updates');
    fireEvent.click(await screen.findByRole('button', { name: 'Look for updates' }));
    await waitFor(() => expect(a.count('POST', '/api/v1/update/check')).toBe(1));
  });

  it('says the retry time after a failed test', async () => {
    renderApp(
      api(
        status({
          state: 'rolled_back',
          available: '1.5.0',
          failures: { version: '1.5.0', count: 1, retry_at: '2026-11-21T15:30:00Z' },
        }),
      ),
      '/settings/updates',
    );
    expect(
      await screen.findAllByText(
        /failed its test\. Version 1\.4\.0 is running again\. We try again at/,
      ),
    ).toBeTruthy();
  });

  it('starts an update on another machine', async () => {
    const a = api(
      status({ state: 'available', available: '1.5.0', policy: 'ask_first' }),
    );
    renderApp(a, '/settings/updates');
    fireEvent.click(await screen.findByRole('button', { name: 'Update build-2' }));
    const remote = await screen.findByRole('region', { name: 'Update build-2' });
    fireEvent.click(within(remote).getByRole('button', { name: 'Update build-2' }));
    await waitFor(() => expect(a.count('POST', '/api/v1/update/install')).toBe(1));
    expect(
      a.calls.find(
        (c) =>
          c.path === '/api/v1/update/install' &&
          (c.body as { machine_id?: string }).machine_id,
      )?.body,
    ).toEqual({
      version: '1.5.0',
      machine_id: 'm2',
    });
  });

  it('Remove stays disabled until a data choice is made, then sends delete_data', async () => {
    const a = api();
    renderApp(a, '/settings/updates');
    fireEvent.click(await screen.findByRole('button', { name: 'Remove crsp' }));
    const dialog = await screen.findByRole('dialog', { name: 'Remove crsp' });
    const confirm = within(dialog).getByRole('button', { name: 'Remove crsp' });
    expect(confirm.hasAttribute('disabled')).toBe(true);
    fireEvent.click(within(dialog).getByRole('radio', { name: /Keep my data/ }));
    expect(confirm.hasAttribute('disabled')).toBe(false);
    fireEvent.click(confirm);
    await waitFor(() => expect(a.count('POST', '/api/v1/remove')).toBe(1));
    expect(a.calls.find((c) => c.path === '/api/v1/remove')?.body).toEqual({
      delete_data: false,
    });
    expect(
      await screen.findByText(/Removing crsp\. This page stops working/),
    ).toBeTruthy();
  });
});

describe('the app-wide update banner', () => {
  it('offers the update under Ask first and starts it', async () => {
    const a = api(
      status({ state: 'available', available: '1.5.0', policy: 'ask_first' }),
    );
    renderApp(a, '/settings/updates');
    const banner = await waitFor(() => {
      const el = document.querySelector('[data-lifecycle="update-banner"]');
      if (!el) throw new Error('no banner');
      return el as HTMLElement;
    });
    expect(banner.dataset.state).toBe('ready');
    fireEvent.click(
      within(banner).getByRole('button', { name: 'Update to version 1.5.0' }),
    );
    await waitFor(() => expect(a.count('POST', '/api/v1/update/install')).toBe(1));
  });

  it('shows progress only in the Updates pane, and one top line on other pages (CRSP364)', async () => {
    renderApp(
      api(status({ state: 'installing', available: '1.5.0', percent: 60 })),
      '/settings/updates',
    );
    const card = await screen.findByRole('region', { name: 'Updates' });
    await waitFor(() => expect(within(card).getByRole('progressbar')).toBeTruthy());
    expect(screen.getAllByRole('progressbar')).toHaveLength(1);
    expect(document.querySelector('[data-lifecycle="update-banner"]')).toBeNull();
    cleanup();
    renderApp(
      api(status({ state: 'installing', available: '1.5.0', percent: 60 })),
      '/settings/access',
    );
    const banner = await waitFor(() => {
      const el = document.querySelector(
        '[data-lifecycle="update-banner"][data-state="running"]',
      );
      if (!el) throw new Error('no banner');
      return el as HTMLElement;
    });
    expect(within(banner).getByText('An update is running.')).toBeTruthy();
    expect(within(banner).queryByRole('progressbar')).toBeNull();
    expect(
      within(banner).getByRole('button', { name: 'See the update' }).getAttribute('href'),
    ).toBe('/settings/updates');
    cleanup();
    renderApp(api(), '/settings/updates');
    await screen.findByRole('region', { name: 'Updates' });
    expect(document.querySelector('[data-lifecycle="update-banner"]')).toBeNull();
  });

  // CRSP235: the controller's own restart is reached from the console, not only from the tray.
  it('offers Restart the service, which opens its Review step', async () => {
    renderApp(api(), '/settings/updates');
    fireEvent.click(await screen.findByRole('button', { name: 'Restart the service' }));
    expect(
      await screen.findByRole('heading', { name: 'Restart the service' }),
    ).toBeTruthy();
  });
});

describe('Settings > Updates, release notes', () => {
  it('shows the note of the offered version, and nothing without one', async () => {
    window.localStorage.clear();
    renderApp(
      api(status({ state: 'available', available: '1.5.0', notes: 'Live progress.' })),
      '/settings/updates',
    );
    expect(await screen.findByText('What is new in version 1.5.0')).toBeTruthy();
    expect(screen.getByText('Live progress.')).toBeTruthy();
    cleanup();
    renderApp(
      api(status({ state: 'available', available: '1.5.0' })),
      '/settings/updates',
    );
    await screen.findByRole('region', { name: 'Updates' });
    expect(screen.queryByText(/What is new in version/)).toBeNull();
  });

  it('shows the note of the new version once after an update', async () => {
    window.localStorage.clear();
    const s = status({
      updated: { version: '1.4.0', notes: 'Runners have a filter bar.' },
    });
    renderApp(api(s), '/settings/updates');
    const note = await screen.findAllByText('Runners have a filter bar.');
    expect(note.length).toBeGreaterThan(0);
    fireEvent.click(screen.getAllByRole('button', { name: /^hide$/i })[0]!);
    cleanup();
    renderApp(api(s), '/settings/updates');
    await screen.findByRole('region', { name: 'Updates' });
    expect(screen.queryByText('Runners have a filter bar.')).toBeNull();
  });
});
