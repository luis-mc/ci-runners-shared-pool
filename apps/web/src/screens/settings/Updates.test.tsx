// operations R5.15, tray R5.15 to R5.18: the Automatic updates switch is on by default and saved at once; the
// Review step names the running jobs, says nothing has changed, and sends the yes (confirmed=true) only when the
// owner presses the primary.
import { cleanup, fireEvent, screen, waitFor, within } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it } from 'vitest';
import { TIMING } from '../../timing';
import { renderApp, signedIn } from '../../testing/mock';

afterEach(cleanup);
beforeEach(() => {
  TIMING.planPollMs = 10;
  TIMING.restartLimitMs = 5 * 60_000;
});

const status = (extra: object = {}) => ({
  current_version: '1.4.0',
  latest_version: '1.5.0',
  update_available: true,
  state: 'idle',
  auto_update: true,
  running_jobs: [{ repository: 'octo/app', workflow: 'ci', run_id: 7, job: 'test' }],
  drain_timeout_minutes: 60,
  ...extra,
});

describe('Review step', () => {
  it('names the running jobs and changes nothing until the yes (UX-62)', async () => {
    const a = signedIn()
      .on('GET', '/api/v1/update', status({ auto_update: false }))
      .on('POST', '/api/v1/update/apply', status({ state: 'waiting_for_jobs' }));
    renderApp(a, '/settings/updates/review?kind=update');
    expect(await screen.findByText(/Nothing has changed yet/)).toBeTruthy();
    expect(screen.getByText('octo/app ci, run 7, job test')).toBeTruthy();
    expect(screen.getByText(/No running job is stopped/)).toBeTruthy();
    expect(a.count('POST', '/api/v1/update/apply')).toBe(0);
    fireEvent.click(screen.getByRole('button', { name: 'Update to 1.5.0' }));
    await waitFor(() => expect(a.count('POST', '/api/v1/update/apply')).toBe(1));
    expect(
      a.calls.find((c) => c.path === '/api/v1/update/apply')?.query.get('confirmed'),
    ).toBe('true');
    expect(
      await screen.findByText(
        /Waiting for running jobs to end/,
        { selector: 'p' },
        { timeout: 5000 },
      ),
    ).toBeTruthy();
  });

  it('Restart the service also asks on the Review step and sends confirmed=true', async () => {
    const a = signedIn()
      .on('GET', '/api/v1/update', status({ update_available: false }))
      .on('POST', '/api/v1/service/restart', status({ state: 'waiting_for_jobs' }));
    renderApp(a, '/settings/updates/review?kind=restart');
    const group = await screen.findByRole('group', { name: 'Restart the service' });
    expect(await within(group).findByText(/Nothing has changed yet/)).toBeTruthy();
    const bar = screen.getByRole('region', { name: 'Restart the service actions' });
    fireEvent.click(within(bar).getByRole('button', { name: 'Restart the service' }));
    await waitFor(() => expect(a.count('POST', '/api/v1/service/restart')).toBe(1));
    expect(
      a.calls.find((c) => c.path === '/api/v1/service/restart')?.query.get('confirmed'),
    ).toBe('true');
  });

  it('ends at Done with the new version once the service is back', async () => {
    let applied = false;
    const a = signedIn()
      .on('GET', '/api/v1/update', () =>
        applied
          ? status({
              current_version: '1.5.0',
              update_available: false,
              running_jobs: [],
            })
          : status(),
      )
      .on('POST', '/api/v1/update/apply', () => {
        applied = true;
        return status({ state: 'waiting_for_jobs' });
      });
    renderApp(a, '/settings/updates/review?kind=update');
    fireEvent.click(await screen.findByRole('button', { name: 'Update to 1.5.0' }));
    expect(await screen.findByText('The service runs 1.5.0.')).toBeTruthy();
    expect(screen.getByRole('button', { name: 'Open Updates' })).toBeTruthy();
  });
});

// CRSP205: a restart is judged only from its own attempt. An old update's failure (to 0.1.13, days ago) stays in
// the service's record; it must not end a restart that worked, and it must not greet a new update.
describe('Restart the service judges its own attempt (CRSP205)', () => {
  const T0 = '2026-11-20T11:19:00Z';
  const old = {
    state: 'failed',
    error:
      'The last update to 0.1.13 failed: the new build did not report healthy in time',
    error_attempt: 'a-old',
  };
  const attempt = { id: 'a-restart', kind: 'restart', started_at: T0 };
  const restartApi = (get: () => object) =>
    signedIn()
      .on('GET', '/api/v1/update', () => get())
      .on('POST', '/api/v1/service/restart', () =>
        status({
          update_available: false,
          running_jobs: [],
          attempt,
          service_started_at: '2026-11-18T08:00:00Z',
          server_time: T0,
          state: 'waiting_for_jobs',
        }),
      );
  const press = async () => {
    const bar = await screen.findByRole('region', {
      name: 'Restart the service actions',
    });
    fireEvent.click(within(bar).getByRole('button', { name: 'Restart the service' }));
  };

  it('ends at Done with the version and how long it took, though an old update failed', async () => {
    let phase: 'before' | 'down' | 'back' = 'before';
    const a = restartApi(() => {
      if (phase === 'down') throw new Error('offline');
      return status({
        update_available: false,
        running_jobs: [],
        current_version: '0.1.17',
        ...old,
        attempt: phase === 'back' ? attempt : undefined,
        service_started_at:
          phase === 'back' ? '2026-11-20T11:19:30Z' : '2026-11-18T08:00:00Z',
        server_time: phase === 'back' ? '2026-11-20T11:19:45Z' : T0,
      });
    });
    renderApp(a, '/settings/updates/review?kind=restart');
    // The old failed update is not the Review step's problem.
    await screen.findByRole('group', { name: 'Restart the service' });
    expect(screen.queryByText(/did not finish/)).toBeNull();
    await press();
    phase = 'down';
    await waitFor(() => expect(a.count('GET', '/api/v1/update')).toBeGreaterThan(2));
    phase = 'back';
    expect(await screen.findByText('The service restarted.')).toBeTruthy();
    expect(screen.queryByText(/0\.1\.13/)).toBeNull();
    expect(screen.queryByText(/did not finish/)).toBeNull();
    expect(screen.getByText('0.1.17')).toBeTruthy();
    expect(screen.getByText('Took')).toBeTruthy();
    expect(screen.getByText('45 seconds')).toBeTruthy();
  });

  it('names this restart in the Problem when the service does not come back in time', async () => {
    TIMING.restartLimitMs = 60;
    let pressed = false;
    const a = restartApi(() =>
      status({
        update_available: false,
        running_jobs: [],
        state: pressed ? 'restarting' : 'idle',
        attempt,
        service_started_at: '2026-11-18T08:00:00Z',
        server_time: T0,
      }),
    );
    renderApp(a, '/settings/updates/review?kind=restart');
    await screen.findByRole('group', { name: 'Restart the service' });
    await press();
    pressed = true;
    const problem = await screen.findByText(
      /The restart you started at .* did not finish/,
    );
    expect(problem.textContent).toMatch(/did not come back within/);
    expect(problem.textContent).not.toMatch(/0\.1\.13/);
  });

  it('shows the error of this restart when it failed', async () => {
    let failed = false;
    const a = restartApi(() =>
      status({
        update_available: false,
        running_jobs: [],
        attempt,
        service_started_at: '2026-11-18T08:00:00Z',
        ...(failed
          ? {
              state: 'failed',
              error: 'restarting the service failed with exit code 1',
              error_attempt: 'a-restart',
            }
          : {}),
      }),
    );
    renderApp(a, '/settings/updates/review?kind=restart');
    await screen.findByRole('group', { name: 'Restart the service' });
    await press();
    failed = true;
    expect(
      await screen.findByText(/restarting the service failed with exit code 1/),
    ).toBeTruthy();
  });

  it('does not greet a new update with an old failure', async () => {
    const a = signedIn().on('GET', '/api/v1/update', status(old));
    renderApp(a, '/settings/updates/review?kind=update');
    expect(await screen.findByRole('button', { name: 'Update to 1.5.0' })).toBeTruthy();
    expect(screen.queryByText(/did not finish/)).toBeNull();
    expect(screen.queryByText(/0\.1\.13/)).toBeNull();
  });
});
