// runners R5.83, OPS86: the machine page sets the free disk to keep, per machine, with the Save bar.
import { cleanup, fireEvent, screen, waitFor, within } from '@testing-library/react';
import { afterEach, describe, expect, it } from 'vitest';
import { machine, renderApp, signedIn } from '../../testing/mock';

afterEach(cleanup);

const GB = 1024 ** 3;

const withDisk = (settings: Record<string, unknown>, policy: Record<string, unknown>) => {
  const m = machine();
  return {
    ...m,
    settings: { ...m.settings, ...settings },
    policy: { on_battery: false, battery_known: false, ...policy },
    facts: { ...m.facts, disk_free_bytes: 82 * GB },
  };
};

const open = (m: ReturnType<typeof withDisk>) =>
  signedIn().on('GET', '/api/v1/machines/:id', m).on('PATCH', '/api/v1/machines/:id', m);

const lastPatch = (api: ReturnType<typeof open>) =>
  api.calls.filter((c) => c.method === 'PATCH').pop()?.body as Record<string, unknown>;

describe('Free disk to keep', () => {
  it('shows the value in force, the free space and what happens under it', async () => {
    renderApp(open(withDisk({}, { disk_floor_gib: 10 })), '/pool/machines/m1');
    await screen.findByRole('heading', { name: 'build-1' });
    expect(
      screen.getByRole('button', { name: /Free disk to keep/ }).textContent,
    ).toContain('10 GB');
    expect(
      screen.getByText(/new jobs wait \(disk_low\)\. Running jobs finish\./),
    ).toBeTruthy();
    expect(screen.getByText(/82 GB free now\./)).toBeTruthy();
  });

  it('prefers the console value and saves a new one with Save', async () => {
    const api = open(withDisk({ disk_floor_gib: 100 }, { disk_floor_gib: 100 }));
    renderApp(api, '/pool/machines/m1');
    await screen.findByRole('heading', { name: 'build-1' });
    fireEvent.click(screen.getByRole('button', { name: /Free disk to keep/ }));
    const dialog = await screen.findByRole('dialog');
    for (const k of ['1', '5', '0'])
      fireEvent.click(within(dialog).getByRole('button', { name: k }));
    fireEvent.click(within(dialog).getByRole('button', { name: 'Done' }));
    fireEvent.click(screen.getByRole('button', { name: 'Save' }));
    await waitFor(() => expect(api.count('PATCH', '/api/v1/machines/m1')).toBe(1));
    expect(lastPatch(api)?.disk_floor_gib).toBe(150);
  });

  it('does not send the floor when only another field changed', async () => {
    const api = open(withDisk({}, { disk_floor_gib: 10 }));
    renderApp(api, '/pool/machines/m1');
    await screen.findByRole('heading', { name: 'build-1' });
    fireEvent.click(screen.getAllByRole('checkbox')[0]!);
    fireEvent.click(screen.getByRole('button', { name: 'Save' }));
    await waitFor(() => expect(api.count('PATCH', '/api/v1/machines/m1')).toBe(1));
    expect(lastPatch(api)).not.toHaveProperty('disk_floor_gib');
  });

  it('shows a floor that is off in config read-only', async () => {
    renderApp(open(withDisk({}, { disk_floor_off: true })), '/pool/machines/m1');
    await screen.findByRole('heading', { name: 'build-1' });
    expect(screen.queryByRole('button', { name: /Free disk to keep/ })).toBeNull();
    expect(screen.getByText(/The floor is off in config\.toml/)).toBeTruthy();
  });
});
