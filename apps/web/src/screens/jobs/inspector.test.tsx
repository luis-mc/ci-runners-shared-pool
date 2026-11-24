// LOOK62: Pool > Jobs as approved. The Jobs list is the first view, its filters are open in a pane, a row opens an
// inspector beside the list, Runs is the second view, and a job the controller reports as reserved is shown as such.
import { cleanup, fireEvent, screen, waitFor, within } from '@testing-library/react';
import { afterEach, describe, expect, it } from 'vitest';
import { renderApp } from '../../testing/mock';
import { job, jobsApi, runSummary } from '../../testing/jobsData';
import { isReserved, jobStatus, waitShort, waitWords } from './words';
import { startWords } from './JobsHome';

afterEach(cleanup);

const running = job({
  id: 1,
  job_name: 'lint',
  state: 'running',
  result: undefined,
  started_at: new Date(Date.now() - 60_000).toISOString(),
  completed_at: undefined,
});
const waiting = job({
  id: 2,
  job_name: 'e2e-1',
  state: 'queued',
  result: undefined,
  started_at: undefined,
  completed_at: undefined,
  runner: undefined,
  wait_reason: 'free_slot',
  wait_text: 'waits for a free slot on build-1',
  queue_position: 1,
  queue_eta: { p50: 300, lo: 240, hi: 420 },
});
// The controller reports the wait reason `reserved` (LOOK65 sends it); the generated list does not have it yet.
const reserved = job({
  id: 3,
  job_name: 'build',
  state: 'queued',
  result: undefined,
  started_at: undefined,
  completed_at: undefined,
  runner: undefined,
  wait_reason: 'reserved' as never,
  wait_text: undefined,
  queue_position: 2,
  queue_eta: { p50: 600, lo: 540, hi: 700 },
});
const finished = job({ id: 4, job_name: 'unit' });
const data = { jobs: [running, waiting, reserved, finished], runs: [runSummary()] };

describe('Pool > Jobs as approved', () => {
  it('starts on the Jobs list: Job, Status, Machine, Start and a Why button on every row', async () => {
    renderApp(jobsApi(data), '/pool/jobs');
    const table = await screen.findByRole('table', { name: 'All jobs' });
    const heads = within(table)
      .getAllByRole('columnheader')
      .map((h) => h.textContent);
    expect(heads.slice(0, 4)).toEqual(['Job', 'Status', 'Machine', 'Start']);
    const rows = within(table).getAllByRole('row').slice(1);
    expect(rows).toHaveLength(4);
    expect(within(table).getAllByRole('button', { name: 'Why' })).toHaveLength(4);
    expect(within(table).getAllByText('octo/app #100', { exact: false }).length).toBe(4);
  });

  it('shows the filters open at first, with Repository, Machine, Runner type and Branch', async () => {
    renderApp(jobsApi(data), '/pool/jobs');
    const pane = await screen.findByRole('region', { name: 'Filters' });
    for (const name of [
      'Repository',
      'Machine',
      'Runner type',
      'Branch',
      'Range',
      'Group by',
    ])
      expect(within(pane).getByLabelText(name)).toBeTruthy();
    expect(within(pane).getByText('open')).toBeTruthy();
    expect(within(pane).getByRole('textbox', { name: /Search/ })).toBeTruthy();
  });

  it('puts a filter choice in the address and keeps the list', async () => {
    const api = jobsApi(data);
    renderApp(api, '/pool/jobs');
    await screen.findByRole('table', { name: 'All jobs' });
    fireEvent.change(screen.getByLabelText('Runner type'), {
      target: { value: 'ephemeral' },
    });
    await waitFor(() => {
      const last = api.calls.filter((c) => c.path === '/api/v1/jobs').at(-1);
      expect(last?.query.get('runner_kind')).toBe('ephemeral');
    });
  });

  it('opens the inspector beside the list from the Why button, and the list stays', async () => {
    renderApp(jobsApi(data), '/pool/jobs');
    const table = await screen.findByRole('table', { name: 'All jobs' });
    expect(screen.queryByRole('complementary')).toBeNull();
    const row = within(table).getByText('e2e-1').closest('tr') as HTMLElement;
    fireEvent.click(within(row).getByRole('button', { name: 'Why' }));
    const side = await screen.findByRole('complementary');
    expect(within(side).getByRole('heading', { name: 'e2e-1: waiting' })).toBeTruthy();
    expect(
      within(side).getAllByText('waits for a free slot on build-1').length,
    ).toBeGreaterThan(0);
    // The separate job page stays reachable from the inspector.
    expect(within(side).getByText('Open job')).toBeTruthy();
    expect(screen.getByRole('table', { name: 'All jobs' })).toBeTruthy();
    fireEvent.click(within(side).getByRole('button', { name: 'Close details' }));
    expect(screen.queryByRole('complementary')).toBeNull();
  });

  it('opens the inspector from a click on the row', async () => {
    renderApp(jobsApi(data), '/pool/jobs');
    const table = await screen.findByRole('table', { name: 'All jobs' });
    fireEvent.click(within(table).getByText('lint').closest('tr') as HTMLElement);
    expect(await screen.findByRole('complementary')).toBeTruthy();
    expect(screen.getByRole('table', { name: 'All jobs' })).toBeTruthy();
  });

  it('keeps the Runs table as the second view', async () => {
    renderApp(jobsApi(data), '/pool/jobs');
    await screen.findByRole('table', { name: 'All jobs' });
    const show = screen.getByRole('group', { name: 'Show runs or jobs' });
    expect(
      within(show)
        .getAllByRole('button')
        .map((b) => b.textContent),
    ).toEqual(['Jobs', 'Runs']);
    fireEvent.click(within(show).getByRole('button', { name: 'Runs' }));
    expect(await screen.findByRole('table', { name: 'All runs' })).toBeTruthy();
  });

  it('shows a reserved job as Reserved, with its Start and its reason', async () => {
    renderApp(jobsApi(data), '/pool/jobs');
    const table = await screen.findByRole('table', { name: 'All jobs' });
    const row = within(table).getByText('build').closest('tr') as HTMLElement;
    expect(within(row).getByText('Reserved')).toBeTruthy();
    expect(within(row).getByText(/^Starts about \d\d:\d\d$/)).toBeTruthy();
    fireEvent.click(within(row).getByRole('button', { name: 'Why' }));
    const side = await screen.findByRole('complementary');
    expect(within(side).getAllByText(/room another job holds/).length).toBeGreaterThan(0);
  });
});

describe('the words of a reserved job', () => {
  it('reads the wait reason reserved of a queued job only', () => {
    const q = { state: 'queued' as const, wait_reason: 'reserved' as never };
    expect(isReserved(q)).toBe(true);
    expect(isReserved({ state: 'running', wait_reason: 'reserved' as never })).toBe(
      false,
    );
    expect(jobStatus(q)).toEqual({ kind: 'waiting', word: 'Reserved' });
    expect(waitWords(q)).toMatch(/room another job holds/);
    expect(waitShort(q)).toBe('room held for it');
    expect(jobStatus({ state: 'queued', wait_reason: 'free_slot' }).word).toBe('waiting');
  });

  it('says when a job starts, started or finished', () => {
    const now = Date.parse('2026-11-23T14:00:00');
    expect(startWords(job({ state: 'queued', queue_eta: undefined }), now)).toBe(
      'Start not known',
    );
    expect(
      startWords(job({ state: 'queued', queue_eta: { p50: 0, lo: 0, hi: 0 } }), now),
    ).toMatch(/^Starts about \d\d:\d\d$/);
    expect(startWords(job({ state: 'running' }), now)).toMatch(/^since \d\d:\d\d$/);
    expect(startWords(job({ state: 'done' }), now)).toMatch(/^finished \d\d:\d\d$/);
  });
});
