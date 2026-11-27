// LOOK21 (UX-71, component 48): the Settings tabs count their changes in the one bar, mark the changed field only,
// keep Save and Cancel off until a change, ask before leaving with changes, and the Access tab has one Save.
import {
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
  within,
} from '@testing-library/react';
import { createApiClient } from '@crsp/contract';
import { RouterProvider, createMemoryRouter } from 'react-router-dom';
import { afterEach, describe, expect, it } from 'vitest';
import { App } from '../../App';
import { FakeEventSource, owner, renderApp, signedIn } from '../../testing/mock';
import type { MockApi } from '../../testing/mock';

afterEach(cleanup);

const bar = () => screen.getByRole('region', { name: 'Actions' });
const save = () => within(bar()).getByRole<HTMLButtonElement>('button', { name: 'Save' });
const cancel = () =>
  within(bar()).getByRole<HTMLButtonElement>('button', { name: 'Cancel' });

const settings = {
  retention_job_history_days: 90,
  retention_logs_days: 14,
  retention_logs_mib_per_machine: 500,
  retention_audit_days: 365,
  retention_load_raw_hours: 48,
  backup_time_local: '03:00',
  backup_keep_days: 14,
};

function dataApi(): MockApi {
  return signedIn(owner)
    .on('GET', '/api/v1/settings', settings)
    .on('GET', '/api/v1/backups', { items: [] });
}

function renderRouted(a: MockApi, path: string) {
  FakeEventSource.reset();
  const client = createApiClient('http://controller.test', a.fetch as typeof fetch);
  const router = createMemoryRouter(
    [
      {
        path: '*',
        element: <App client={client} eventSource={(url) => new FakeEventSource(url)} />,
      },
    ],
    { initialEntries: [path] },
  );
  render(<RouterProvider router={router} />);
  return router;
}

async function changeJobHistory() {
  fireEvent.click(await screen.findByRole('button', { name: /Job history/ }));
  const keypad = await screen.findByRole('dialog');
  fireEvent.click(within(keypad).getByRole('button', { name: '5' }));
  fireEvent.click(within(keypad).getByRole('button', { name: 'Done' }));
}

describe('Data tab: the bar and the marks (LOOK21)', () => {
  it('keeps Save and Cancel off until a change, then counts it and marks that field only', async () => {
    renderApp(dataApi(), '/settings/data');
    await screen.findByRole('button', { name: /Job history/ });
    expect(save().disabled).toBe(true);
    expect(cancel().disabled).toBe(true);
    expect(document.querySelectorAll('.ds-field__changed')).toHaveLength(0);
    await changeJobHistory();
    expect(within(bar()).getByText('Unsaved changes (1)')).toBeTruthy();
    expect(save().disabled).toBe(false);
    expect(cancel().disabled).toBe(false);
    const marks = document.querySelectorAll('.ds-field__changed');
    expect(marks).toHaveLength(1);
    expect(marks[0]?.closest('.ds-field')?.textContent).toContain('Job history');
  });

  it('Cancel takes the marks and the count away', async () => {
    renderApp(dataApi(), '/settings/data');
    await changeJobHistory();
    fireEvent.click(cancel());
    await waitFor(() =>
      expect(document.querySelectorAll('.ds-field__changed')).toHaveLength(0),
    );
    expect(save().disabled).toBe(true);
  });
});

describe('Settings tab: the leave guard (LOOK21)', () => {
  it('asks "Discard changes?" on another tab; Keep editing stays, Discard leaves', async () => {
    const router = renderRouted(dataApi(), '/settings/data');
    await changeJobHistory();
    fireEvent.click(screen.getByRole('tab', { name: 'Console' }));
    const dialog = await screen.findByRole('dialog', { name: 'Discard changes?' });
    fireEvent.click(within(dialog).getByRole('button', { name: 'Keep editing' }));
    await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull());
    expect(router.state.location.pathname).toBe('/settings/data');
    fireEvent.click(screen.getByRole('tab', { name: 'Console' }));
    const again = await screen.findByRole('dialog', { name: 'Discard changes?' });
    fireEvent.click(within(again).getByRole('button', { name: 'Discard' }));
    await waitFor(() => expect(router.state.location.pathname).toBe('/settings/console'));
  });

  it('does not ask when nothing changed', async () => {
    const router = renderRouted(dataApi(), '/settings/data');
    await screen.findByRole('button', { name: /Job history/ });
    fireEvent.click(screen.getByRole('tab', { name: 'Console' }));
    await waitFor(() => expect(router.state.location.pathname).toBe('/settings/console'));
    expect(screen.queryByRole('dialog', { name: 'Discard changes?' })).toBeNull();
  });
});

describe('Access tab: one Save (LOOK21)', () => {
  const access = () =>
    signedIn(owner)
      .on('GET', '/api/v1/users', { items: [owner] })
      .on('GET', '/api/v1/github/repositories', { items: [] })
      .on('POST', '/api/v1/users', { ...owner, id: 9, login: 'bob', role: 'viewer' });

  it('has one Save, off until a field is filled, and marks the filled field only', async () => {
    renderApp(access(), '/settings/access');
    await screen.findByRole('table', { name: 'Users' });
    expect(within(bar()).getAllByRole('button', { name: 'Save' })).toHaveLength(1);
    expect(screen.queryByRole('button', { name: 'Add' })).toBeNull();
    expect(save().disabled).toBe(true);
    fireEvent.change(screen.getByRole('textbox', { name: 'GitHub login' }), {
      target: { value: 'bob' },
    });
    expect(within(bar()).getByText('Unsaved changes (1)')).toBeTruthy();
    const marks = document.querySelectorAll('.ds-field__changed');
    expect(marks).toHaveLength(1);
    expect(marks[0]?.closest('.ds-field')?.textContent).toContain('GitHub login');
  });
});
