// LOOK70: Pool > Overview is the four counts, Queue plan and Running, as the approved prototype; no slot timeline.
import { cleanup, screen } from '@testing-library/react';
import { afterEach, describe, expect, it } from 'vitest';
import { overview, renderApp, signedIn } from '../../testing/mock';
import { job } from '../../testing/jobsData';

afterEach(cleanup);

describe('Pool Overview', () => {
  it('shows the counts, the queue plan and the running jobs, and no Jobs over time', async () => {
    renderApp(
      signedIn().on(
        'GET',
        '/api/v1/overview',
        overview({
          queued: [job({ id: 1, state: 'queued', job_name: 'plain', queue_position: 1 })],
          queued_count: 1,
        }),
      ),
      '/pool',
    );
    await screen.findByRole('region', { name: 'Queue plan' });
    expect(screen.getByText('Starting')).toBeTruthy();
    expect(screen.getByRole('region', { name: 'Running' })).toBeTruthy();
    expect(screen.queryByRole('region', { name: 'Jobs over time' })).toBeNull();
  });
});
