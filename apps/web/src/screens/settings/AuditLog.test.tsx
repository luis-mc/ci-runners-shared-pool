// Audit log (web-console 4.9, observability R5.24 to R5.27, UX-39): the list, its filters and the Export button.
import { cleanup, fireEvent, screen, waitFor, within } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { renderApp, signedIn, viewer, apiError } from '../../testing/mock';

afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
});

const row = (id: number, action: string, over: object = {}) => ({
  id,
  at: '2026-11-14T10:00:00Z',
  actor: 'octo',
  source: 'web',
  action,
  target_type: 'machine',
  target_id: 'm1',
  summary: `did ${action}`,
  result: 'done',
  ...over,
});

const file = (name: string, body: string, type: string) =>
  new Response(body, {
    headers: {
      'Content-Type': type,
      'Content-Disposition': `attachment; filename="${name}"`,
    },
  });

let saved: { name: string; text: Promise<string> }[] = [];

beforeEach(() => {
  saved = [];
  URL.createObjectURL = (b: Blob) => {
    saved.push({ name: '', text: b.text() });
    return 'blob:x';
  };
  URL.revokeObjectURL = () => undefined;
  vi.spyOn(HTMLAnchorElement.prototype, 'click').mockImplementation(function (
    this: HTMLAnchorElement,
  ) {
    const last = saved[saved.length - 1];
    if (last) last.name = this.download;
  });
});

describe('Audit log', () => {
  const api = () =>
    signedIn().on('GET', '/api/v1/audit', {
      items: [row(2, 'machine.drain'), row(1, 'settings.update', { result: 'refused' })],
    });

  it('shows what each change acted on, a plain detail and the time on one line (LOOK49)', async () => {
    renderApp(
      signedIn().on('GET', '/api/v1/audit', {
        items: [
          row(2, 'variable.restored', {
            target_type: 'repository',
            target_id: 'luis-mc/app',
            summary: 'not on the list',
          }),
          row(1, 'variable.restored', {
            target_type: 'repository',
            target_id: 'luis-mc/web',
          }),
        ],
      }),
      '/settings/audit',
    );
    const table = await screen.findByRole('table', { name: 'Audit log' });
    expect(within(table).getByRole('columnheader', { name: 'On' })).toBeTruthy();
    expect(within(table).getByText('repository luis-mc/app')).toBeTruthy();
    expect(within(table).getByText('repository luis-mc/web')).toBeTruthy();
    expect(table.textContent).not.toContain('detail=');
    const times = table.querySelectorAll('time.ds-datetime');
    expect(times).toHaveLength(2);
    expect(times[0]?.getAttribute('datetime')).toBe('2026-11-14T10:00:00.000Z');
  });

  it('puts Export in the list toolbar, not over the rows (LOOK49)', async () => {
    renderApp(api(), '/settings/audit');
    await screen.findByRole('table', { name: 'Audit log' });
    const bar = screen.getByRole('group', { name: 'Audit log toolbar' });
    expect(within(bar).getByRole('button', { name: /Export/ })).toBeTruthy();
  });

  it('lists the changes newest first, with who, what and the result', async () => {
    renderApp(api(), '/settings/audit');
    const table = await screen.findByRole('table', { name: 'Audit log' });
    const rows = within(table).getAllByRole('row');
    expect(rows[1]?.textContent).toContain('machine.drain');
    expect(rows[2]?.textContent).toContain('settings.update');
    expect(within(table).getByText('refused')).toBeTruthy();
  });

  it('says so when nothing matches the search', async () => {
    const a = signedIn().on('GET', '/api/v1/audit', (c) =>
      c.query.get('actor') ? { items: [] } : { items: [row(1, 'machine.drain')] },
    );
    renderApp(a, '/settings/audit');
    await screen.findByRole('table', { name: 'Audit log' });
    fireEvent.change(screen.getByRole('textbox', { name: 'Search who' }), {
      target: { value: 'nobody' },
    });
    expect(await screen.findByText(/No change matches the search/)).toBeTruthy();
  });

  it('Export asks for every row of the filters and saves the file the controller named (UX-39)', async () => {
    const a = api().on('GET', '/api/v1/exports/audit', () =>
      file('crsp-audit-2026-11-14-1005.csv', 'id,at\n2,x\n', 'text/csv'),
    );
    renderApp(a, '/settings/audit');
    await screen.findByRole('table', { name: 'Audit log' });
    fireEvent.change(screen.getByRole('textbox', { name: 'Search what' }), {
      target: { value: 'machine.drain' },
    });
    await waitFor(() =>
      expect(a.calls.some((c) => c.query.get('action') === 'machine.drain')).toBe(true),
    );
    fireEvent.click(screen.getByRole('button', { name: 'Export' }));
    await waitFor(() => expect(saved).toHaveLength(1));
    const call = a.calls.find((c) => c.path === '/api/v1/exports/audit');
    expect(call?.query.get('action')).toBe('machine.drain');
    expect(call?.query.get('limit')).toBeNull();
    expect(call?.query.get('cursor')).toBeNull();
    expect(saved[0]?.name).toBe('crsp-audit-2026-11-14-1005.csv');
    expect(await saved[0]?.text).toBe('id,at\n2,x\n');
  });

  it('a failed export says so in one line and the button works again', async () => {
    const a = api().on('GET', '/api/v1/exports/audit', () =>
      apiError(500, 'internal', 'internal error'),
    );
    renderApp(a, '/settings/audit');
    await screen.findByRole('table', { name: 'Audit log' });
    fireEvent.click(screen.getByRole('button', { name: 'Export' }));
    expect(
      await screen.findByText(/Could not export the audit log\. internal error/),
    ).toBeTruthy();
    expect(screen.getByRole('button', { name: 'Export' }).hasAttribute('disabled')).toBe(
      false,
    );
  });

  it('a viewer may read and export the audit log', async () => {
    renderApp(
      signedIn(viewer).on('GET', '/api/v1/audit', { items: [row(1, 'machine.drain')] }),
      '/settings/audit',
    );
    await screen.findByRole('table', { name: 'Audit log' });
    expect(screen.getByRole('button', { name: 'Export' }).hasAttribute('disabled')).toBe(
      false,
    );
  });
});
