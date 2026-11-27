// caches R5.51 to R5.54: the Machine page shows the Docker disk compact policy, saves each control at once, and says when the agent cannot compact.
import { cleanup, fireEvent, screen, waitFor, within } from '@testing-library/react';
import { afterEach, describe, expect, it } from 'vitest';
import type { Schemas } from '../../api/api';
import { machine, renderApp, signedIn } from '../../testing/mock';

afterEach(cleanup);

const GIB = 1024 ** 3;

const windows = (
  over: Record<string, unknown> = {},
  status: Record<string, unknown> = {},
): Schemas['Machine'] => {
  const m = machine({
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
  });
  return {
    ...m,
    settings: {
      ...m.settings,
      docker_compact: 'auto' as const,
      docker_compact_threshold_gib: 30,
      docker_compact_idle_minutes: 30,
      ...over,
    },
  };
};

const detail = (m = windows()) =>
  signedIn().on('GET', '/api/v1/machines/:id', m).on('PATCH', '/api/v1/machines/:id', m);

const lastPatch = (api: ReturnType<typeof detail>) =>
  api.calls.filter((c) => c.method === 'PATCH').pop()?.body as Record<string, unknown>;

describe('Docker disk section', () => {
  it('shows the numbers, the state and the policy', async () => {
    renderApp(detail(), '/setup/machines/m1');
    await screen.findByRole('heading', { name: 'build-1' });
    expect(screen.getByRole('heading', { name: 'Docker disk' })).toBeTruthy();
    expect(screen.getByText('Waiting: jobs run or wait here')).toBeTruthy();
    expect(screen.getByText('Can be reclaimed')).toBeTruthy();
    const section = within(
      screen
        .getByRole('heading', { name: 'Docker disk' })
        .closest('section') as HTMLElement,
    );
    expect(
      section.getByRole<HTMLInputElement>('radio', { name: /Automatic/ }).checked,
    ).toBe(true);
    expect(screen.queryByText(/Quiet hours/)).toBeNull();
    expect(screen.getByText(/at most once a day/)).toBeTruthy();
  });

  it('saves the mode at once, without Save', async () => {
    const api = detail();
    renderApp(api, '/setup/machines/m1');
    await screen.findByRole('heading', { name: 'build-1' });
    fireEvent.click(screen.getByRole('radio', { name: /^Off/ }));
    await waitFor(() => expect(api.count('PATCH', '/api/v1/machines/m1')).toBe(1));
    expect(lastPatch(api)).toEqual({ docker_compact: 'off' });
  });

  it('says when the agent cannot make the disk smaller and offers Turn off', async () => {
    const api = detail(windows({}, { can_compact: false, reason: 'no_admin' }));
    renderApp(api, '/setup/machines/m1');
    await screen.findByRole('heading', { name: 'build-1' });
    expect(screen.getByText(/does not run as administrator/)).toBeTruthy();
    expect(
      screen.getByText('Cannot: the agent has no administrator rights'),
    ).toBeTruthy();
    fireEvent.click(screen.getByRole('button', { name: 'Turn off' }));
    await waitFor(() => expect(api.count('PATCH', '/api/v1/machines/m1')).toBe(1));
    expect(lastPatch(api)).toEqual({ docker_compact: 'off' });
  });

  it('shows nothing for a machine without a Docker Desktop disk file', async () => {
    renderApp(detail(machine()), '/setup/machines/m1');
    await screen.findByRole('heading', { name: 'build-1' });
    expect(screen.queryByRole('heading', { name: 'Docker disk' })).toBeNull();
  });
});
