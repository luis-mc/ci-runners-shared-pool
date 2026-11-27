// CRSP235, machines R5.81: Restart the service of an agent machine, with the shared wizard. Review names the
// running jobs, the button is busy while it runs, the page follows Waiting then Restarting, and ends at Done when the
// agent is connected again. No job is cut.
import { cleanup, fireEvent, screen, waitFor } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it } from 'vitest';
import { TIMING } from '../../timing';
import { apiError, machine, renderApp, signedIn, viewer } from '../../testing/mock';

afterEach(cleanup);
beforeEach(() => {
  TIMING.planPollMs = 10;
});

const job = {
  id: 9,
  repository: 'octo/app',
  run_id: 1,
  run_attempt: 1,
  github_job_id: 1,
  job_name: 'e2e',
  labels: [],
  state: 'running' as const,
};
const change = (state: string, jobs: unknown[] = []) =>
  ({
    id: 'ch1',
    machine_id: 'm1',
    kind: 'service_restart',
    state,
    started_at: '2026-11-14T10:00:00Z',
    waiting_for: jobs,
  }) as never;

describe('Restart the service (machine)', () => {
  it('names the running jobs, asks the yes, follows the restart and ends at Done', async () => {
    let step = 0;
    const api = signedIn()
      .on('GET', '/api/v1/machines/:id', () => {
        if (step === 0) return machine({ running_jobs: [job] });
        step += 1;
        // waiting for the job, then restarting, then the agent is back
        if (step === 2) return machine({ pending_change: change('waiting', [job]) });
        if (step === 3)
          return machine({ pending_change: change('running'), online: false });
        return machine();
      })
      .on('POST', '/api/v1/machines/:id/restart', () => {
        step = 1;
        return change('waiting', [job]);
      });
    renderApp(api, '/setup/machines/m1/restart');
    expect(await screen.findByText('octo/app e2e')).toBeTruthy();
    fireEvent.click(screen.getByRole('button', { name: 'Restart the service' }));
    await screen.findByText(/The service of build-1 restarted\./);
    expect(api.count('POST', '/api/v1/machines/m1/restart')).toBe(1);
    const call = api.calls.find((c) => c.method === 'POST');
    expect((call?.query as URLSearchParams).get('confirmed')).toBe('true');
    expect(screen.getByText('Jobs stopped')).toBeTruthy();
    // CRSP336 (UX-88): the terminal keeps what the console saw, in order, after the work ends.
    expect(screen.getByText('$ Restart the service of build-1')).toBeTruthy();
    expect(screen.getByText('The service is connected again.')).toBeTruthy();
  });

  it('says why it cannot start when the machine is offline and tries again', async () => {
    let online = false;
    const api = signedIn().on('GET', '/api/v1/machines/:id', () => machine({ online }));
    renderApp(api, '/setup/machines/m1/restart');
    expect(
      await screen.findByText(/is offline\. Its service can restart only/),
    ).toBeTruthy();
    online = true;
    fireEvent.click(screen.getByRole('button', { name: 'Try again' }));
    expect(await screen.findByText(/Nothing has changed yet/)).toBeTruthy();
  });

  it('says what failed when the controller refuses', async () => {
    const api = signedIn()
      .on('GET', '/api/v1/machines/:id', machine())
      .on('POST', '/api/v1/machines/:id/restart', () =>
        apiError(409, 'conflict', 'the machine is offline'),
      );
    renderApp(api, '/setup/machines/m1/restart');
    await screen.findByText(/Nothing has changed yet/);
    await waitFor(() =>
      expect(
        screen.getByRole<HTMLButtonElement>('button', { name: 'Restart the service' })
          .disabled,
      ).toBe(false),
    );
    fireEvent.click(screen.getByRole('button', { name: 'Restart the service' }));
    expect(await screen.findByText(/The restart did not finish\./)).toBeTruthy();
    expect(screen.getByRole('button', { name: 'Try again' })).toBeTruthy();
  });

  it('locks the button for a viewer and says why', async () => {
    const api = signedIn(viewer).on('GET', '/api/v1/machines/:id', machine());
    renderApp(api, '/setup/machines/m1/restart');
    const b = await screen.findByRole<HTMLButtonElement>('button', {
      name: 'Restart the service',
    });
    expect(b.disabled).toBe(true);
  });

  it('points the controller machine to Settings, Updates', async () => {
    const api = signedIn().on(
      'GET',
      '/api/v1/machines/:id',
      machine({ is_controller: true }),
    );
    renderApp(api, '/setup/machines/m1/restart');
    await waitFor(() =>
      expect(
        screen.getByText(/Its service restarts from Settings, Updates\./),
      ).toBeTruthy(),
    );
    expect(api.count('POST', '/api/v1/machines/m1/restart')).toBe(0);
  });
});
