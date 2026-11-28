// Caches R5.77, R5.78: the one hub on the Caches tab — the budget every kind shares, one card per kind with its
// switch and Wipe, and warmth by repository (CRSP342 option 1).
import { cleanup, fireEvent, screen, waitFor, within } from '@testing-library/react';
import { afterEach, describe, expect, it } from 'vitest';
import type { Schemas } from '../../api/api';
import { apiError, machine, renderApp, signedIn, viewer } from '../../testing/mock';
import { hitRate } from './CacheHub';

afterEach(cleanup);

const GB = 1024 ** 3;

const card = (over: Partial<Schemas['CacheHubCard']>): Schemas['CacheHubCard'] => ({
  kind: 'actions_cache',
  on: true,
  size_bytes: 0,
  entry_count: 0,
  hits: 0,
  misses: 0,
  served_bytes: 0,
  status: 'ok',
  wipe_id: 'mirror:actions_all',
  ...over,
});

const hub = (over: Partial<Schemas['CacheHub']> = {}): Schemas['CacheHub'] => ({
  settings: {
    actions_cache: true,
    git_mirror: true,
    registry_mirror: false,
    package_proxy: false,
    budget_gib: 60,
    git_refresh_minutes: 15,
    warmth_hours: 24,
  },
  budget_bytes: 60 * GB,
  used_bytes: 18 * GB,
  hits: 88,
  misses: 12,
  served_bytes: 41 * GB,
  warm_machines: 2,
  cards: [
    card({
      kind: 'actions_cache',
      entry_count: 212,
      size_bytes: 9 * GB,
      status: 'waiting',
      note: 'the receiver does not listen on the LAN yet',
    }),
    card({
      kind: 'git_mirror',
      entry_count: 3,
      size_bytes: 4 * GB,
      wipe_id: 'mirror:git_all',
    }),
    card({ kind: 'registry_mirror', on: false, wipe_id: 'mirror:registry_mirror' }),
    card({ kind: 'package_proxy', on: false, wipe_id: 'mirror:packages' }),
  ],
  repos: [{ repo: 'luis-mc/site', hits: 12, misses: 31, size_bytes: GB, warmth: 40 }],
  ...over,
});

const api = (h: Schemas['CacheHub']) =>
  signedIn()
    .on('GET', '/api/v1/machines', { items: [machine()] })
    .on('GET', '/api/v1/images', { items: [] })
    .on('GET', '/api/v1/caches', { items: [] })
    .on('GET', '/api/v1/caches/hub', h);

describe('Cache hub', () => {
  it('shows the hit rate, the one budget, a card per kind and warmth by repository', async () => {
    renderApp(api(hub()), '/setup/images?tab=caches');
    expect(await screen.findByRole('region', { name: 'Shared cache' })).toBeTruthy();
    expect(screen.getByText('One budget')).toBeTruthy();
    for (const title of [
      'Actions cache',
      'Repository mirrors',
      'Registry mirror',
      'Package proxy',
    ])
      expect(screen.getByRole('region', { name: title })).toBeTruthy();
    const actions = screen.getByRole('region', { name: 'Actions cache' });
    expect(
      within(actions).getByText('the receiver does not listen on the LAN yet'),
    ).toBeTruthy();
    const pkg = screen.getByRole('region', { name: 'Package proxy' });
    expect(within(pkg).getByText('npm, PyPI, crates, apt')).toBeTruthy();
    expect(screen.getByRole('table', { name: 'Warmth by repository' })).toBeTruthy();
    expect(screen.getByText('luis-mc/site')).toBeTruthy();
    expect(screen.getAllByRole('switch')).toHaveLength(4);
  });

  it('turns a kind on with the whole settings object', async () => {
    let body: unknown;
    const h = hub();
    renderApp(
      api(h).on('PUT', '/api/v1/caches/settings', (call) => {
        body = call.body;
        return { ...h.settings, package_proxy: true };
      }),
      '/setup/images?tab=caches',
    );
    const pkg = await screen.findByRole('region', { name: 'Package proxy' });
    fireEvent.click(within(pkg).getByRole('switch'));
    await waitFor(() => expect(body).toEqual({ ...h.settings, package_proxy: true }));
  });

  it('asks before a wipe and names what goes', async () => {
    let confirmed = false;
    renderApp(
      api(hub()).on('POST', '/api/v1/caches/:id/wipe', (call) => {
        if (call.query.get('confirm')) {
          confirmed = true;
          return {
            id: 'mirror:git_all',
            kind: 'git_mirror',
            machine_id: 'controller',
            size_bytes: 0,
            state: 'wiping',
          };
        }
        return apiError(409, 'needs_confirmation', 'Needs a yes.', [
          { kind: 'cache', id: 'mirror:git_all', text: 'git mirror o/r, 4.0 GB' },
        ]);
      }),
      '/setup/images?tab=caches',
    );
    const git = await screen.findByRole('region', { name: 'Repository mirrors' });
    fireEvent.click(within(git).getByRole('button', { name: 'Wipe' }));
    expect(await screen.findByText('git mirror o/r, 4.0 GB')).toBeTruthy();
    expect(confirmed).toBe(false);
    const dialog = screen.getByRole('dialog');
    fireEvent.click(within(dialog).getByRole('button', { name: 'Wipe' }));
    await waitFor(() => expect(confirmed).toBe(true));
  });

  it('a viewer sees the hub but cannot switch, save or wipe', async () => {
    renderApp(
      signedIn(viewer)
        .on('GET', '/api/v1/machines', { items: [machine()] })
        .on('GET', '/api/v1/images', { items: [] })
        .on('GET', '/api/v1/caches', { items: [] })
        .on('GET', '/api/v1/caches/hub', hub()),
      '/setup/images?tab=caches',
    );
    const git = await screen.findByRole('region', { name: 'Repository mirrors' });
    expect(
      within(git).getByRole<HTMLButtonElement>('button', { name: 'Wipe' }).disabled,
    ).toBe(true);
    expect(screen.queryByRole('spinbutton', { name: 'Budget' })).toBeNull();
  });

  it('has no hit rate before the first lookup', () => {
    expect(hitRate(0, 0)).toBeUndefined();
    expect(hitRate(88, 12)).toBe('88');
  });
});
