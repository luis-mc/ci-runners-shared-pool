// D26: each Settings tab is one form. The bar at the foot says "Unsaved changes (N)" and carries one Save for the
// whole tab; Save is disabled with its reason while the tab is clean. Actions that do not save are normal buttons.
import { cleanup, fireEvent, screen, waitFor, within } from '@testing-library/react';
import { afterEach, describe, expect, it } from 'vitest';
import { owner, renderApp, signedIn, viewer } from '../../testing/mock';

afterEach(cleanup);

const pool = {
  budget_memory_pct: 85,
  budget_cpu_pct: 60,
  size_slots_from_machine: true,
  default_job_slots: 2,
  drain_timeout_minutes: 60,
};
const queue = {
  order: ['priority', 'event_class', 'fair_share', 'queued_at'],
  fair_share: true,
  no_runner_wait_minutes: 2,
};
const bar = () => screen.getByRole('region', { name: 'Actions' });
const save = () => within(bar()).getByRole<HTMLButtonElement>('button', { name: 'Save' });

describe('Settings tab: one Save (D26)', () => {
  it('a clean tab says nothing is unsaved and Save is disabled with its reason', async () => {
    renderApp(
      signedIn(owner)
        .on('GET', '/api/v1/settings/pool', pool)
        .on('GET', '/api/v1/settings/queue', queue),
      '/settings/repositories',
    );
    await screen.findByRole('button', { name: /Memory budget/ });
    expect(within(bar()).queryByText(/Unsaved changes/)).toBeNull();
    expect(save().disabled).toBe(true);
    expect(within(bar()).getByText('Nothing has changed yet.')).toBeTruthy();
    expect(document.querySelectorAll('[data-primary]')).toHaveLength(1);
  });

  it('counts the changes, saves the whole tab with one Save, and is clean again', async () => {
    const a = signedIn(owner)
      .on('GET', '/api/v1/settings/pool', pool)
      .on('GET', '/api/v1/settings/queue', queue)
      .on('PUT', '/api/v1/settings/pool', pool)
      .on('PUT', '/api/v1/settings/queue', queue);
    renderApp(a, '/settings/repositories');
    await screen.findByRole('button', { name: /Memory budget/ });
    fireEvent.click(screen.getByRole('checkbox', { name: /Size the slots/ }));
    fireEvent.click(screen.getByRole('button', { name: /Job slots/ }));
    const keypad = await screen.findByRole('dialog');
    fireEvent.click(within(keypad).getByRole('button', { name: '5' }));
    fireEvent.click(within(keypad).getByRole('button', { name: 'Done' }));
    expect(within(bar()).getByText('Unsaved changes (2)')).toBeTruthy();
    expect(save().disabled).toBe(false);
    fireEvent.click(save());
    await waitFor(() => expect(a.count('PUT', '/api/v1/settings/pool')).toBe(1));
    expect(a.count('PUT', '/api/v1/settings/queue')).toBe(1);
  });

  it('Cancel in the bar puts every section of the tab back', async () => {
    renderApp(
      signedIn(owner)
        .on('GET', '/api/v1/settings/pool', pool)
        .on('GET', '/api/v1/settings/queue', queue),
      '/settings/repositories',
    );
    await screen.findByRole('button', { name: /Memory budget/ });
    fireEvent.click(screen.getByRole('checkbox', { name: /Size the slots/ }));
    fireEvent.click(within(bar()).getByRole('button', { name: 'Cancel' }));
    expect(
      screen.getByRole<HTMLInputElement>('checkbox', { name: /Size the slots/ }).checked,
    ).toBe(true);
    expect(within(bar()).queryByText(/Unsaved changes/)).toBeNull();
  });

  it('two sections of the Data tab count together and one Save sends both', async () => {
    let stored: Record<string, unknown> = { retention_job_history_days: 90 };
    const a = signedIn(owner)
      .on('GET', '/api/v1/settings', () => stored)
      .on('GET', '/api/v1/backups', { items: [] })
      .on('PATCH', '/api/v1/settings', (c) => {
        stored = { ...stored, ...(c.body as object) };
        return stored;
      });
    renderApp(a, '/settings/data');
    await screen.findByText('Job history');
    const change = async (name: RegExp) => {
      fireEvent.click(screen.getByRole('button', { name }));
      const keypad = await screen.findByRole('dialog');
      fireEvent.click(within(keypad).getByRole('button', { name: '5' }));
      fireEvent.click(within(keypad).getByRole('button', { name: 'Done' }));
    };
    await change(/^Job history/);
    await change(/^Keep backups/);
    expect(await screen.findByText('Unsaved changes (2)')).toBeTruthy();
    fireEvent.click(save());
    await waitFor(() => expect(a.count('PATCH', '/api/v1/settings')).toBe(2));
    expect(await screen.findByText('Retention saved.')).toBeTruthy();
    await waitFor(() => expect(within(bar()).queryByText(/Unsaved changes/)).toBeNull());
  });

  it('a viewer sees Save disabled with the reason', async () => {
    renderApp(signedIn(viewer).on('GET', '/api/v1/settings', {}), '/settings/data');
    await screen.findByText('Job history');
    expect(save().disabled).toBe(true);
  });

  it('actions that do not save are normal buttons inside their section', async () => {
    renderApp(
      signedIn(owner).on('GET', '/api/v1/notification-channels', { items: [] }),
      '/settings/console',
    );
    const add = await screen.findByRole('button', { name: 'Add channel' });
    expect(add.hasAttribute('data-primary')).toBe(false);
    expect(within(bar()).getByRole('button', { name: 'Save' })).toBeTruthy();
    expect(document.querySelectorAll('[data-primary]')).toHaveLength(1);
  });
});
