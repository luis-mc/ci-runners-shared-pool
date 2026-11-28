// debug-access R5.2, R5.3, R5.14, R5.34 (CRSP331): the Debug access card of a job. The button is disabled with the
// server's reason beside it, opening asks for a reason of 3 to 200 characters, the card says what the shell sees, a
// macOS target says it was not run on a real Mac, an open session offers Extend and End (End asks first).
import { cleanup, fireEvent, screen, waitFor, within } from '@testing-library/react';
import { afterEach, describe, expect, it } from 'vitest';
import type { Schemas } from '../../api/api';
import { renderApp } from '../../testing/mock';
import { jobDetail, jobsApi } from '../../testing/jobsData';
import { reasonProblem } from './DebugCard';

afterEach(cleanup);

type JobDebug = Schemas['JobDebug'];

const SEES =
  "Can see this job's files and its running processes' output, not its secrets.";

function card(over: Partial<JobDebug> = {}): JobDebug {
  return {
    job_id: 12,
    allowed: false,
    warnings: [],
    sees: SEES,
    repository_enabled: false,
    sessions: [],
    ...over,
  };
}

const session: Schemas['DebugSession'] = {
  id: 'dbg_1',
  job_id: 12,
  repository: 'octo/app',
  user: 'admin1',
  reason: 'look at the disk',
  source: 'web',
  state: 'open',
  opened_at: '2026-11-27T10:00:00Z',
  ends_at: '2099-11-27T10:30:00Z',
  extended: false,
  recorded: true,
};

function render(debug: JobDebug) {
  const api = jobsApi({ detail: jobDetail({ state: 'running' }) }).on(
    'GET',
    '/api/v1/jobs/:jobId/debug',
    debug,
  );
  renderApp(api, '/pool/jobs/12');
  return api;
}

describe('Debug access card', () => {
  it('shows the button disabled with the reason and what the shell sees (R5.2, R5.14)', async () => {
    render(
      card({
        why_not_code: 'repo_off',
        why_not: 'Debug access is off for this repository.',
      }),
    );
    const region = await screen.findByRole('region', { name: 'Debug access' });
    expect(await within(region).findByText(SEES)).toBeTruthy();
    const button = within(region).getByRole('button', { name: /Open debug session/ });
    expect(
      button.getAttribute('aria-disabled') ?? button.hasAttribute('disabled'),
    ).toBeTruthy();
    expect(
      within(region).getByText('Debug access is off for this repository.'),
    ).toBeTruthy();
    expect(
      within(region).getByText('No debug sessions yet. Open one from a running job.'),
    ).toBeTruthy();
  });

  it('asks for a reason first and sends it (R5.3)', async () => {
    const api = render(
      card({
        allowed: true,
        repository_enabled: true,
        limit_min: 30,
        idle_min: 10,
      }),
    ).on('POST', '/api/v1/jobs/:jobId/debug-sessions', session);
    expect(
      await screen.findByText(
        /ends after 30 min, or after 10 min/,
        {},
        { timeout: 5000 },
      ),
    ).toBeTruthy();
    const region = screen.getByRole('region', { name: 'Debug access' });
    fireEvent.click(within(region).getByRole('button', { name: /Open debug session/ }));
    const dialog = await screen.findByRole('dialog');
    fireEvent.change(within(dialog).getByLabelText(/Reason/), {
      target: { value: 'look at the disk' },
    });
    const buttons = within(dialog).getAllByRole('button', { name: /Open debug session/ });
    fireEvent.click(buttons[buttons.length - 1]!);
    await waitFor(() =>
      expect(api.calls.filter((c) => c.method === 'POST').map((c) => c.body)).toEqual([
        { reason: 'look at the disk' },
      ]),
    );
  });

  it('warns before a session on a persistent runner and on macOS (R5.10, R5.34)', async () => {
    render(
      card({
        warnings: ['This runner keeps files between jobs.', 'Not run on a real Mac yet.'],
      }),
    );
    expect(await screen.findByText('This runner keeps files between jobs.')).toBeTruthy();
    expect(screen.getByText('Not run on a real Mac yet.')).toBeTruthy();
  });

  it('offers Extend and End on an open session; End asks first (R5.19, R5.26)', async () => {
    render(card({ open_session: session, sessions: [session] }));
    const region = await screen.findByRole('region', { name: 'Debug access' });
    expect(await within(region).findAllByText('admin1')).toBeTruthy();
    expect(within(region).getByRole('button', { name: /Extend/ })).toBeTruthy();
    fireEvent.click(within(region).getByRole('button', { name: /^End$/ }));
    expect(await screen.findByText('End this debug session?')).toBeTruthy();
  });

  it('checks the reason length like the server', () => {
    expect(reasonProblem('ab')).toBeTruthy();
    expect(reasonProblem('abc')).toBeUndefined();
    expect(reasonProblem('x'.repeat(201))).toBeTruthy();
  });
});
