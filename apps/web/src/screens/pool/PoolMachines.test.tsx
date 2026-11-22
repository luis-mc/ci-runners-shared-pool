// LOOK58c: Pool > Machines cards with health and job stats, the 24 h / 7 d switch, the empty states and the machine page.
import { cleanup, fireEvent, screen, waitFor, within } from '@testing-library/react';
import { afterEach, describe, expect, it } from 'vitest';
import { machine, renderApp, runner, signedIn, type MockApi } from '../../testing/mock';
import {
  fullHealth,
  machineStats,
  statsSet,
  unknownHealth,
} from '../../testing/machineHealthData';
import type { Schemas } from '../../api/api';

afterEach(cleanup);

function api(
  opts: {
    machines?: Schemas['Machine'][];
    health?: Schemas['MachineHealth'];
    runners?: Schemas['Runner'][];
  } = {},
): MockApi {
  return signedIn()
    .on('GET', '/api/v1/machines', { items: opts.machines ?? [machine()] })
    .on('GET', '/api/v1/machines/:id', (opts.machines ?? [machine()])[0])
    .on('GET', '/api/v1/machines/:id/health', opts.health ?? fullHealth())
    .on('GET', '/api/v1/machines/:id/job-stats', (c) =>
      c.query.get('window') === '7d'
        ? machineStats('7d', {
            machine: statsSet({
              counts: { ...statsSet().counts, succeeded: 140, total: 150 },
            }),
          })
        : machineStats('24h'),
    )
    .on('GET', '/api/v1/runners', { items: opts.runners ?? [runner()] });
}

describe('Pool > Machines cards', () => {
  it('shows the health badges, the key numbers, the job stats and the runners of a machine', async () => {
    renderApp(api(), '/pool/machines');
    const card = await screen.findByRole('region', { name: 'build-1' });
    await within(card).findByText('3 days 4 h');
    expect(within(card).getByText('Uptime')).toBeTruthy();
    expect(within(card).getByText('Days until full')).toBeTruthy();
    expect(within(card).getByText('about 6.5 days (Docker disk)')).toBeTruthy();
    expect(within(card).getByText('1.0 GB of 8.0 GB used')).toBeTruthy();
    expect(within(card).getByText('61.5 °C')).toBeTruthy();
    expect(within(card).getByText('1.2, 0.9, 0.7')).toBeTruthy();
    expect(within(card).getByText('Average run time')).toBeTruthy();
    expect(within(card).getByText('4 min 12 s')).toBeTruthy();
    expect(within(card).getByText('Stopped by crsp')).toBeTruthy();
    // The runner of the machine, with its own stats.
    expect(within(card).getByText('crsp-linux-1')).toBeTruthy();
    expect(within(card).getByText(/24 jobs, 4 failed or cut short/)).toBeTruthy();
    // The last failure links to the job.
    const link = within(card).getByRole('link', { name: 'build' });
    expect(link.getAttribute('href')).toBe('/pool/jobs/77');
    // The title opens the machine page.
    expect(within(card).getByRole('link', { name: 'build-1' }).getAttribute('href')).toBe(
      '/pool/machines/m1',
    );
  });

  it('reads unknown on the OS of the machine for a value the API left out', async () => {
    renderApp(
      api({ machines: [machine({ os: 'windows' })], health: unknownHealth() }),
      '/pool/machines',
    );
    const card = await screen.findByRole('region', { name: 'build-1' });
    await waitFor(() =>
      expect(within(card).getAllByText(/^unknown on Windows/).length).toBeGreaterThan(5),
    );
    expect(
      within(card).getByText('unknown on Windows: Windows has no load average'),
    ).toBeTruthy();
    expect(within(card).queryByText(/^unknown on Linux/)).toBeNull();
  });

  it('says the health is not reported yet and still shows every number as unknown', async () => {
    renderApp(
      api({ health: { machine_id: 'm1', reported: false, stale: false, volumes: [] } }),
      '/pool/machines',
    );
    const card = await screen.findByRole('region', { name: 'build-1' });
    await within(card).findByText('health not reported yet');
    expect(within(card).getAllByText(/^unknown on Linux/).length).toBe(13);
  });

  it('switches the job stats between 24 h and 7 days', async () => {
    const mock = api();
    renderApp(mock, '/pool/machines');
    const card = await screen.findByRole('region', { name: 'build-1' });
    const group = await within(card).findByRole('group', { name: 'Job stats window' });
    expect(
      within(group).getByRole('button', { name: '24 h' }).getAttribute('aria-pressed'),
    ).toBe('true');
    await within(card).findByText('18');
    fireEvent.click(within(group).getByRole('button', { name: '7 days' }));
    await within(card).findByText('140');
    expect(
      mock.calls.some(
        (c) => c.path.endsWith('/job-stats') && c.query.get('window') === '7d',
      ),
    ).toBe(true);
    expect(
      within(group).getByRole('button', { name: '7 days' }).getAttribute('aria-pressed'),
    ).toBe('true');
  });

  it('says so when no job failed and when the machine has no runner', async () => {
    const mock = api({ runners: [] }).on(
      'GET',
      '/api/v1/machines/:id/job-stats',
      machineStats('24h', {
        machine: statsSet({ top_failing: [] }, ['last_failure']),
        runners: [],
      }),
    );
    renderApp(mock, '/pool/machines');
    const card = await screen.findByRole('region', { name: 'build-1' });
    await within(card).findByText('none in the last 24 hours');
    expect(within(card).getByText('No job failed in the last 24 hours.')).toBeTruthy();
    expect(within(card).getByText('No runner is on this machine.')).toBeTruthy();
  });

  it('says no machine is in the pool yet', async () => {
    renderApp(api({ machines: [] }), '/pool/machines');
    expect(await screen.findByText('No machine is in the pool yet.')).toBeTruthy();
  });
});

describe('the machine page', () => {
  it('shows every metric, the disks, the job stats, the runners and the Jobs over time section', async () => {
    renderApp(api(), '/pool/machines/m1');
    expect(
      await screen.findByRole('heading', { level: 1, name: 'build-1' }),
    ).toBeTruthy();
    const crumbs = screen.getByRole('navigation', { name: 'You are here' });
    expect(within(crumbs).getByText('Machines')).toBeTruthy();
    await screen.findByText('Key numbers');
    for (const name of [
      'Disks',
      'Memory and swap',
      'Network and clock',
      'Restarts, sleep and power',
      'Versions and Docker',
      'Heat and load',
      'Job stats',
      'Jobs over time',
    ])
      expect(screen.getByRole('region', { name })).toBeTruthy();
    const disks = screen.getByRole('table', { name: 'Disks of this machine' });
    expect(within(disks).getByText('Docker disk')).toBeTruthy();
    expect(within(disks).getByText('6.5')).toBeTruthy();
    expect(within(disks).getByText('free space is not falling')).toBeTruthy();
    const runners = await screen.findByRole('table', { name: 'Runners of this machine' });
    expect(within(runners).getByText('crsp-linux-1')).toBeTruthy();
    expect(screen.getByText('Docker 27.1')).toBeTruthy();
  });

  it('reads unknown on Windows with the reason for each value the machine could not give', async () => {
    renderApp(
      api({ machines: [machine({ os: 'windows' })], health: unknownHealth() }),
      '/pool/machines/m1',
    );
    await screen.findByText('Key numbers');
    expect(screen.getAllByText(/^unknown on Windows/).length).toBeGreaterThan(15);
    expect(screen.getAllByText('unknown on Windows: no sensor').length).toBeGreaterThan(
      0,
    );
    expect(screen.queryByText(/^unknown on Linux/)).toBeNull();
  });

  it('switches the job stats window on the page', async () => {
    const mock = api();
    renderApp(mock, '/pool/machines/m1');
    const section = await screen.findByRole('region', {
      name: 'Job stats',
    });
    await within(section).findByText('Jobs that finished in the last 24 hours.');
    fireEvent.click(within(section).getByRole('button', { name: '7 days' }));
    await within(section).findByText('Jobs that finished in the last 7 days.');
    await waitFor(() =>
      expect(
        mock.calls.some(
          (c) => c.path.endsWith('/job-stats') && c.query.get('window') === '7d',
        ),
      ).toBe(true),
    );
  });
});
