// LOOK65: Pool > Queue Analysis has the Reserved chip: only the job its machine reserves room for.
import { cleanup, fireEvent, screen } from '@testing-library/react';
import { afterEach, describe, expect, it } from 'vitest';
import { overview, renderApp, signedIn } from '../../testing/mock';
import { job } from '../../testing/jobsData';

afterEach(cleanup);

const queued = [
  job({
    id: 1,
    state: 'queued',
    job_name: 'plain',
    queue_position: 1,
    wait_reason: 'free_slot',
  }),
  job({
    id: 2,
    state: 'queued',
    job_name: 'held',
    queue_position: 2,
    wait_reason: 'reserved',
    reserved_since: '2026-11-24T10:00:00Z',
  }),
  job({
    id: 3,
    state: 'queued',
    job_name: 'nofit',
    queue_position: 3,
    wait_reason: 'no_runner_with_labels',
  }),
];

describe('Queue Analysis chips', () => {
  it('offers All, Waiting for room, Reserved and No runner fits, and each chip keeps its own jobs', async () => {
    renderApp(
      signedIn().on('GET', '/api/v1/overview', overview({ queued, queued_count: 3 })),
      '/pool/queue',
    );
    await screen.findByText(/1\. plain/);
    for (const name of ['Waiting for room', 'Reserved', 'No runner fits']) {
      expect(screen.getByRole('button', { name })).toBeTruthy();
    }
    fireEvent.click(screen.getByRole('button', { name: 'Reserved' }));
    expect(screen.getByText(/2\. held/)).toBeTruthy();
    expect(screen.queryByText(/1\. plain/)).toBeNull();
    expect(screen.queryByText(/3\. nofit/)).toBeNull();
    fireEvent.click(screen.getByRole('button', { name: 'Waiting for room' }));
    expect(screen.getByText(/1\. plain/)).toBeTruthy();
    expect(screen.queryByText(/2\. held/)).toBeNull();
  });
});
