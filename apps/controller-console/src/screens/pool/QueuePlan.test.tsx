// LOOK65: Pool > Queue Analysis has the Reserved chip: only the job its machine reserves room for.
import { cleanup, fireEvent, screen, within } from '@testing-library/react';
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
    wait_text: 'pool-main',
    labels: ['self-hosted', 'linux', 'pool-main'],
  }),
];

const ov = (q = queued) =>
  signedIn().on(
    'GET',
    '/api/v1/overview',
    overview({ queued: q, queued_count: q.length }),
  );

describe('Queue Analysis table', () => {
  it('offers All, Waiting for room, Reserved and No runner fits with real counts, and each keeps its own jobs', async () => {
    renderApp(ov(), '/pool/queue');
    await screen.findByText('plain');
    expect(screen.getByRole('button', { name: 'All 3' })).toBeTruthy();
    expect(screen.getByRole('button', { name: 'Waiting for room 1' })).toBeTruthy();
    expect(screen.getByRole('button', { name: 'Reserved 1' })).toBeTruthy();
    expect(screen.getByRole('button', { name: 'No runner fits 1' })).toBeTruthy();
    fireEvent.click(screen.getByRole('button', { name: 'Reserved 1' }));
    const t = () => within(screen.getByRole('table', { name: 'Queue, in start order' }));
    expect(t().getByText('held')).toBeTruthy();
    expect(t().queryByText('plain')).toBeNull();
    expect(t().queryByText('nofit')).toBeNull();
    fireEvent.click(screen.getByRole('button', { name: 'Waiting for room 1' }));
    expect(t().getByText('plain')).toBeTruthy();
    expect(t().queryByText('held')).toBeNull();
  });

  it('is one row per job with one status word, and says once why jobs have no runner', async () => {
    renderApp(ov(), '/pool/queue');
    await screen.findByText('plain');
    expect(
      within(screen.getByRole('table', { name: 'Queue, in start order' })).getAllByRole(
        'row',
      ).length,
    ).toBe(1 + 3); // head and three jobs
    expect(
      screen.getByText(
        /1 job asks for the label pool-main, which no runner has\. Check the repository's runs-on variable\./,
      ),
    ).toBeTruthy();
    expect(screen.getByRole('link', { name: 'Open Setup > Pool setup' })).toBeTruthy();
    expect(screen.queryByText('waits to be placed')).toBeNull();
    expect(screen.queryByText('Does not start by itself')).toBeNull();
  });

  it('opens the details of a row: start estimate, machine and the whole reason', async () => {
    renderApp(ov(), '/pool/queue');
    await screen.findByText('nofit');
    fireEvent.click(screen.getAllByRole('button', { name: /Details/ })[2]!);
    expect(
      screen.getAllByText(/No runner has the label pool-main\./).length,
    ).toBeGreaterThan(0);
    const d = screen.getByRole('region', { name: 'Details: nofit' });
    expect(d.textContent).toMatch(/Start not known|Starts about/);
  });

  it('has no count tiles and no slot timeline, and shows the Reservation pane and the running jobs, each in its own full pane (LOOK70)', async () => {
    renderApp(ov(), '/pool/queue');
    await screen.findByText('plain');
    expect(screen.queryByText('Starting')).toBeNull();
    expect(screen.queryByText('Where the queue lands on the slots')).toBeNull();
    expect(screen.queryByRole('region', { name: 'Jobs over time' })).toBeNull();
    expect(screen.getByRole('region', { name: 'Reservation' })).toBeTruthy();
    expect(screen.getByRole('link', { name: 'Open held in Pool > Jobs' })).toBeTruthy();
    expect(
      screen.getByRole('region', { name: 'Where running jobs are going' }),
    ).toBeTruthy();
    // CRSP391: the panes sit in a Stack, never in the form field grid that gave them a 280 px column.
    expect(document.querySelector('.ds-field-grid')).toBeNull();
  });

  it('shows no Reservation pane when no job is reserved', async () => {
    renderApp(ov([queued[0]!]), '/pool/queue');
    await screen.findByText('plain');
    expect(screen.queryByRole('region', { name: 'Reservation' })).toBeNull();
  });
});
