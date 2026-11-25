// machines R5.83 to R5.86: the machine page shows the uptime over 24 hours, 7 days and 30 days and why the machine took no job.
import { cleanup, screen, within } from '@testing-library/react';
import { afterEach, describe, expect, it } from 'vitest';
import type { Schemas } from '../../api/api';
import { apiError, machine, renderApp, signedIn } from '../../testing/mock';

afterEach(cleanup);

const uptime = (
  over: Partial<Schemas['MachineUptime']> = {},
): Schemas['MachineUptime'] => ({
  machine_id: 'm1',
  watched: true,
  since: '2026-11-20T08:00:00Z',
  windows: [
    {
      window: '24h',
      covered_seconds: 86400,
      online_seconds: 79000,
      taking_seconds: 70000,
      uptime_pct: 91.4,
      taking_pct: 81,
    },
    {
      window: '7d',
      covered_seconds: 3 * 86400,
      online_seconds: 250000,
      taking_seconds: 240000,
      uptime_pct: 96.5,
      taking_pct: 92.6,
    },
    {
      window: '30d',
      covered_seconds: 3 * 86400,
      online_seconds: 250000,
      taking_seconds: 240000,
      uptime_pct: 96.5,
      taking_pct: 92.6,
    },
  ],
  periods: [
    { started_at: '2026-11-22T10:00:00Z', offline: false, reason: 'on_battery' },
    {
      started_at: '2026-11-21T10:00:00Z',
      ended_at: '2026-11-21T12:30:00Z',
      offline: true,
      reason: 'heartbeat_lost',
    },
  ],
  ...over,
});

const base = () => signedIn().on('GET', '/api/v1/machines/:id', machine());

const page = (u: Schemas['MachineUptime']) =>
  base().on('GET', '/api/v1/machines/:id/uptime', u);

describe('Uptime section', () => {
  it('shows the three windows and the reason of each period', async () => {
    renderApp(page(uptime()), '/setup/machines/m1');
    const head = await screen.findByRole('heading', { name: 'Uptime' });
    const section = within(head.closest('section') as HTMLElement);
    expect(await section.findByText('Last 24 hours')).toBeTruthy();
    expect(section.getByText('91.4')).toBeTruthy();
    expect(section.getByText('Last 30 days')).toBeTruthy();
    expect(section.getByText(/took jobs 81.0% of the time/)).toBeTruthy();
    expect(section.getByText('On battery')).toBeTruthy();
    expect(section.getByText('No sign of life for 30 seconds')).toBeTruthy();
    expect(section.getByText('2 h 30 min')).toBeTruthy();
    expect(section.getByText('Now')).toBeTruthy();
    // A window watched for less than its length says so.
    expect(section.getByText('Last 7 days, watched for')).toBeTruthy();
  });

  it('says nothing was watched yet, with no percent', async () => {
    renderApp(
      page(uptime({ watched: false, windows: [], periods: [] })),
      '/setup/machines/m1',
    );
    const head = await screen.findByRole('heading', { name: 'Uptime' });
    const section = within(head.closest('section') as HTMLElement);
    expect(await section.findByText(/No uptime yet/)).toBeTruthy();
    expect(section.queryByText(/% online/)).toBeNull();
  });

  it('says the machine took jobs the whole time when no period is listed', async () => {
    renderApp(page(uptime({ periods: [] })), '/setup/machines/m1');
    expect(await screen.findByText(/took jobs the whole time/)).toBeTruthy();
  });

  it('says it could not load, with Try again', async () => {
    renderApp(
      base().on('GET', '/api/v1/machines/:id/uptime', () =>
        apiError(500, 'internal', 'internal error'),
      ),
      '/setup/machines/m1',
    );
    const head = await screen.findByRole('heading', { name: 'Uptime' });
    const section = within(head.closest('section') as HTMLElement);
    expect(await section.findByText('Could not load the uptime.')).toBeTruthy();
    expect(section.getByRole('button', { name: 'Try again' })).toBeTruthy();
  });
});
