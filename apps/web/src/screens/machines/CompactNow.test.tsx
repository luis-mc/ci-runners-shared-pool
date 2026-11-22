// caches R5.58: Compact now asks first and names the size that can be reclaimed, then starts the compact; it is busy while
// the compact runs and disabled with a reason when the agent cannot compact.
import { cleanup, fireEvent, screen, waitFor, within } from '@testing-library/react';
import { afterEach, describe, expect, it } from 'vitest';
import type { Schemas } from '../../api/api';
import { machine, renderApp, signedIn } from '../../testing/mock';

afterEach(cleanup);

const GIB = 1024 ** 3;
const COMPACT = '/api/v1/machines/m1/docker-compact';

const windows = (status: Record<string, unknown> = {}): Schemas['Machine'] => ({
  ...machine({
    os: 'windows',
    docker_compact_status: {
      enabled: true,
      running: false,
      disk_present: true,
      can_compact: true,
      file_bytes: 236 * GIB,
      used_bytes: 71 * GIB,
      reclaimable_bytes: 165 * GIB,
      reason: 'busy',
      ...status,
    },
  }),
});

const page = (m = windows()) =>
  signedIn().on('GET', '/api/v1/machines/:id', m).on('POST', COMPACT, { started: true });

describe('Compact now', () => {
  it('asks first, names what can be reclaimed, and starts only after the yes', async () => {
    const api = page();
    renderApp(api, '/setup/machines/m1');
    fireEvent.click(
      await screen.findByRole('button', { name: 'Make the disk smaller now' }),
    );
    const card = await screen.findByRole('dialog');
    const lines = within(within(card).getByRole('list', { name: 'What goes' }))
      .getAllByRole('listitem')
      .map((l) => l.textContent);
    expect(lines[0]).toBe('Can be reclaimed: 177 GB');
    expect(api.count('POST', COMPACT)).toBe(0);
    fireEvent.click(within(card).getByRole('button', { name: 'Make smaller' }));
    await waitFor(() => expect(api.count('POST', COMPACT)).toBe(1));
  });

  it('is busy while the compact runs and says so', async () => {
    renderApp(page(windows({ running: true })), '/setup/machines/m1');
    const b = await screen.findByRole('button', { name: /Making the disk smaller/ });
    expect(b.hasAttribute('disabled') || b.getAttribute('aria-disabled') === 'true').toBe(
      true,
    );
  });

  it('is disabled with the reason when the agent has no administrator rights', async () => {
    renderApp(page(windows({ can_compact: false })), '/setup/machines/m1');
    const b = await screen.findByRole('button', { name: 'Make the disk smaller now' });
    expect(b.hasAttribute('disabled') || b.getAttribute('aria-disabled') === 'true').toBe(
      true,
    );
  });

  it('is not there when the machine has no Docker disk file', async () => {
    const { docker_compact_status: _gone, ...bare } = windows();
    renderApp(page(bare), '/setup/machines/m1');
    await screen.findByRole('heading', { name: 'build-1' });
    expect(
      screen.queryByRole('button', { name: 'Make the disk smaller now' }),
    ).toBeNull();
  });
});
