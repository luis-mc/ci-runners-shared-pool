// Lifecycle UI: Settings > Updates with the shared parts, against the contract's /api/update endpoints (mocked
// until the controller serves them).
import { cleanup, fireEvent, screen, waitFor, within } from '@testing-library/react';
import { afterEach, describe, expect, it } from 'vitest';
import { renderApp, signedIn } from '../../testing/mock';

afterEach(cleanup);

const status = (extra: Record<string, unknown> = {}) => ({
  state: 'idle',
  current: '1.4.0',
  available: null,
  required: false,
  policy: 'automatic_when_idle',
  window: null,
  next_check_at: null,
  failures: null,
  waiting_for_jobs: false,
  ...extra,
});

const api = (s: Record<string, unknown> = status()) =>
  signedIn()
    .on('GET', '/api/update/status', s)
    .on('GET', '/api/update/history', () => [
      {
        at: '2026-11-21T10:00:00Z',
        actor: 'luis',
        action: 'install',
        version: '1.4.0',
        result: 'ok',
      },
    ])
    .on('GET', '/api/update/fleet', () => [
      { machine_id: 'm2', name: 'build-2', version: '1.3.0', state: 'needs_you' },
    ])
    .on('POST', '/api/update/policy', {})
    .on('POST', '/api/update/check', {})
    .on('POST', '/api/update/install', {})
    .on('POST', '/api/remove', {});

describe('Settings > Updates (lifecycle)', () => {
  it('shows the Updates card, the history and the machines', async () => {
    renderApp(api(), '/settings/updates');
    expect(await screen.findByRole('region', { name: 'Updates' })).toBeTruthy();
    expect(
      await screen.findByRole('radio', { name: /Automatic when idle/ }),
    ).toBeTruthy();
    expect(screen.getByText('Up to date. Version 1.4.0 is the newest.')).toBeTruthy();
    expect(await screen.findByRole('table', { name: 'Update history' })).toBeTruthy();
    expect(await screen.findByText('build-2')).toBeTruthy();
    expect(screen.getByText('Needs you')).toBeTruthy();
  });

  it('saves the policy at once', async () => {
    const a = api();
    renderApp(a, '/settings/updates');
    fireEvent.click(await screen.findByRole('radio', { name: /Ask first/ }));
    await waitFor(() => expect(a.count('POST', '/api/update/policy')).toBe(1));
    expect(a.calls.find((c) => c.path === '/api/update/policy')?.body).toEqual({
      policy: 'ask_first',
      window: null,
    });
  });

  it('saves a time window once both times are right', async () => {
    const a = api();
    renderApp(a, '/settings/updates');
    fireEvent.change(await screen.findByLabelText(/From/), {
      target: { value: '01:00' },
    });
    fireEvent.change(screen.getByLabelText(/Until/), { target: { value: '05:00' } });
    await waitFor(() => expect(a.count('POST', '/api/update/policy')).toBe(1));
    expect(a.calls.find((c) => c.path === '/api/update/policy')?.body).toEqual({
      policy: 'automatic_when_idle',
      window: { start: '01:00', end: '05:00' },
    });
  });

  it('Check asks the controller to look', async () => {
    const a = api();
    renderApp(a, '/settings/updates');
    fireEvent.click(await screen.findByRole('button', { name: 'Check' }));
    await waitFor(() => expect(a.count('POST', '/api/update/check')).toBe(1));
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
    await waitFor(() => expect(a.count('POST', '/api/update/install')).toBe(1));
    expect(
      a.calls.find(
        (c) =>
          c.path === '/api/update/install' &&
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
    await waitFor(() => expect(a.count('POST', '/api/remove')).toBe(1));
    expect(a.calls.find((c) => c.path === '/api/remove')?.body).toEqual({
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
    await waitFor(() => expect(a.count('POST', '/api/update/install')).toBe(1));
  });

  it('shows progress while a step runs and hides when nothing is going on', async () => {
    renderApp(
      api(status({ state: 'installing', available: '1.5.0', percent: 60 })),
      '/settings/updates',
    );
    await waitFor(() =>
      expect(
        document.querySelector('[data-lifecycle="update-banner"][data-state="running"]'),
      ).toBeTruthy(),
    );
    cleanup();
    renderApp(api(), '/settings/updates');
    await screen.findByRole('region', { name: 'Updates' });
    expect(document.querySelector('[data-lifecycle="update-banner"]')).toBeNull();
  });
});
