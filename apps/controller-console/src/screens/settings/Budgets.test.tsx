// R5.27: the Budgets sub-page (machines R5.22, scheduling R5.8 to R5.10, observability R5.5).
import { cleanup, fireEvent, screen, waitFor } from '@testing-library/react';
import { afterEach, describe, expect, it } from 'vitest';
import type { Schemas } from '../../api/api';
import { owner, renderApp, signedIn, viewer } from '../../testing/mock';
import { budgetProblems } from './Budgets';

afterEach(cleanup);

const pool = {
  budget_memory_pct: 85,
  budget_cpu_pct: 60,
  size_slots_from_machine: true,
  default_job_slots: 2,
  drain_timeout_minutes: 60,
};
const queue: Schemas['QueueSettings'] = {
  order: ['priority', 'event_class', 'fair_share', 'queued_at'],
  fair_share: true,
  no_runner_wait_minutes: 2,
};

const KEYS = ['Repository priority', 'Event class', 'Fair share', 'Time queued'];
const rowNames = () =>
  screen
    .getAllByRole('listitem')
    .map((li) => li.querySelector('.ds-order__name')?.textContent)
    .filter(Boolean);

const api = (user = owner) =>
  signedIn(user)
    .on('GET', '/api/v1/settings/pool', pool)
    .on('GET', '/api/v1/settings/queue', queue);

describe('Budgets (R5.27)', () => {
  it('has a Budgets tab and shows the pool defaults and the queue order', async () => {
    renderApp(api(), '/settings/budgets');
    expect(await screen.findByRole('tab', { name: 'Repositories' })).toBeTruthy();
    expect(await screen.findByRole('button', { name: /Memory budget/ })).toBeTruthy();
    expect(
      screen.getByRole('button', { name: /Time to finish running jobs/ }).textContent,
    ).toContain('60');
    expect(rowNames()).toEqual(KEYS);
    expect(screen.getByText(/no running job is waited/)).toBeTruthy();
  });

  it('saves both, in the order the page shows them, with Cancel and Save', async () => {
    const a = api()
      .on('PUT', '/api/v1/settings/pool', pool)
      .on('PUT', '/api/v1/settings/queue', queue);
    renderApp(a, '/settings/budgets');
    await screen.findByRole('button', { name: /Memory budget/ });
    expect(screen.getByRole('button', { name: 'Cancel' })).toBeTruthy();
    fireEvent.click(screen.getByRole('checkbox', { name: /Size the slots/ }));
    fireEvent.click(screen.getByRole('button', { name: 'Save' }));
    await waitFor(() => expect(a.count('PUT', '/api/v1/settings/queue')).toBe(1));
    expect(
      a.calls.find((c) => c.path === '/api/v1/settings/pool' && c.method === 'PUT')?.body,
    ).toEqual({ ...pool, size_slots_from_machine: false });
    expect(await screen.findByText('Settings saved.')).toBeTruthy();
  });

  it('lists each order key once and saves a moved order', async () => {
    const a = api()
      .on('PUT', '/api/v1/settings/pool', pool)
      .on('PUT', '/api/v1/settings/queue', queue);
    renderApp(a, '/settings/budgets');
    await screen.findByRole('button', { name: /Memory budget/ });
    expect(
      screen.getByRole<HTMLButtonElement>('button', {
        name: 'Move Repository priority up',
      }).disabled,
    ).toBe(true);
    fireEvent.click(screen.getByRole('button', { name: 'Move Event class up' }));
    fireEvent.click(screen.getByRole('button', { name: 'Save' }));
    await waitFor(() => expect(a.count('PUT', '/api/v1/settings/queue')).toBe(1));
    expect(
      a.calls.find((c) => c.path === '/api/v1/settings/queue' && c.method === 'PUT')
        ?.body,
    ).toEqual({
      ...queue,
      order: ['event_class', 'priority', 'fair_share', 'queued_at'],
    });
  });

  it('normalises stored duplicates on load', async () => {
    const a = signedIn(owner)
      .on('GET', '/api/v1/settings/pool', pool)
      .on('GET', '/api/v1/settings/queue', {
        ...queue,
        order: ['queued_at', 'queued_at', 'priority', 'priority'],
      });
    renderApp(a, '/settings/budgets');
    await screen.findByRole('button', { name: /Memory budget/ });
    expect(rowNames()).toEqual([
      'Time queued',
      'Repository priority',
      'Event class',
      'Fair share',
    ]);
  });

  it('Cancel puts the saved values back', async () => {
    renderApp(api(), '/settings/budgets');
    await screen.findByRole('button', { name: /Memory budget/ });
    const box = screen.getByRole<HTMLInputElement>('checkbox', {
      name: /Size the slots/,
    });
    fireEvent.click(box);
    expect(box.checked).toBe(false);
    fireEvent.click(screen.getByRole('button', { name: 'Cancel' }));
    expect(
      screen.getByRole<HTMLInputElement>('checkbox', { name: /Size the slots/ }).checked,
    ).toBe(true);
  });

  it('shows the refusal of the server in one line', async () => {
    const a = api().on(
      'PUT',
      '/api/v1/settings/pool',
      new Response(
        JSON.stringify({
          error: {
            code: 'bad_request',
            message: 'drain_timeout_minutes must be between 5 and 1440',
          },
        }),
        { status: 400, headers: { 'content-type': 'application/json' } },
      ),
    );
    renderApp(a, '/settings/budgets');
    await screen.findByRole('button', { name: /Memory budget/ });
    fireEvent.click(screen.getByRole('checkbox', { name: /Size the slots/ }));
    fireEvent.click(screen.getByRole('button', { name: 'Save' }));
    expect(await screen.findByText(/Could not save\. .*between 5 and 1440/)).toBeTruthy();
  });

  it('a viewer cannot save and is told why', async () => {
    renderApp(api(viewer), '/settings/budgets');
    await screen.findByRole('button', { name: /Memory budget/ });
    const save = screen.getByRole<HTMLButtonElement>('button', { name: /Save/ });
    expect(save.disabled || save.getAttribute('aria-disabled') === 'true').toBe(true);
  });
});

describe('budgetProblems', () => {
  it('accepts the defaults and the edges of the contract', () => {
    expect(budgetProblems(pool, queue)).toEqual([]);
    expect(
      budgetProblems(
        {
          ...pool,
          budget_memory_pct: 10,
          budget_cpu_pct: 95,
          default_job_slots: 0,
          drain_timeout_minutes: 1440,
        },
        { ...queue, no_runner_wait_minutes: 60 },
      ),
    ).toEqual([]);
  });
  it('names each value outside its bounds', () => {
    const out = budgetProblems(
      {
        ...pool,
        budget_memory_pct: 9,
        budget_cpu_pct: 96,
        default_job_slots: 65,
        drain_timeout_minutes: 4,
      },
      { ...queue, no_runner_wait_minutes: 0 },
    );
    expect(out).toHaveLength(5);
  });
});
