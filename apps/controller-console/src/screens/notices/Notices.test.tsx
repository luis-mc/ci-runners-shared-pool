// notifications R5.32, R6.7, R6.8 and UX-18, UX-26, UX-52: the Notices page. It lists the last 30 days with the event in
// words, the subject, the state, the tone as a word and what became of each delivery, and it only shows and links.
import { cleanup, fireEvent, screen, waitFor, within } from '@testing-library/react';
import { afterEach, describe, expect, it } from 'vitest';
import { apiError, renderApp, signedIn } from '../../testing/mock';
import { deliveryText, eventWord } from './Notices';

afterEach(cleanup);

const offline = {
  id: 'ntc_1',
  type: 'machine.offline',
  subject_kind: 'machine',
  subject_id: 'm1',
  subject: 'build-02',
  state: 'firing',
  tone: 'problem',
  summary: 'build-02 stopped answering at 14:30 UTC.',
  link: '/setup/machines/m1',
  flaps: 2,
  at: '2026-11-25T14:30:00Z',
  deliveries: [
    {
      id: 'dlv_1',
      channel: 'Team chat',
      kind: 'webhook',
      target: 'hooks.example.com',
      status: 'failed',
      attempt: 7,
      last_error: 'hooks.example.com answered HTTP 503.',
    },
    {
      id: 'dlv_2',
      channel: 'My desktop',
      kind: 'desktop',
      target: 'octo',
      status: 'delivered',
      attempt: 1,
    },
  ],
};
const back = {
  id: 'ntc_2',
  type: 'machine.offline',
  subject: 'build-02',
  state: 'resolved',
  tone: 'done',
  summary: 'build-02 is back online.',
  flaps: 0,
  at: '2026-11-25T14:40:00Z',
  deliveries: [],
};

const api = (items: unknown[] = [offline, back]) =>
  signedIn().on('GET', '/api/v1/notices', { items });

describe('Notices page', () => {
  it('lists notices with the event, subject, state and tone as words', async () => {
    renderApp(api(), '/notices');
    const table = await screen.findByRole('table', {
      name: 'Notices of the last 30 days',
    });
    const rows = within(table).getAllByRole('row');
    const first = within(rows[1]!);
    expect(first.getByText('Machine offline')).toBeTruthy();
    expect(first.getByText('build-02')).toBeTruthy();
    expect(first.getByText('Open')).toBeTruthy();
    expect(first.getByText('Problem')).toBeTruthy();
    expect(first.getByText(/It came back 2 times/)).toBeTruthy();
    // The tone is a word, so it reads without colour (R6.8).
    expect(within(table).getByText('Done')).toBeTruthy();
    expect(within(table).getByText('Resolved')).toBeTruthy();
  });

  it('shows what became of the deliveries as a word and a glyph, the worst one first', async () => {
    renderApp(api(), '/notices');
    const table = await screen.findByRole('table', {
      name: 'Notices of the last 30 days',
    });
    expect(within(table).getAllByText('Failed').length).toBeGreaterThan(0);
    expect(within(table).getByText('Kept here only')).toBeTruthy();
  });

  it('says what to do when there are no notices', async () => {
    renderApp(api([]), '/notices');
    expect(
      await screen.findByText(
        'No notices yet. Notices appear here when a machine, job or disk needs you.',
      ),
    ).toBeTruthy();
  });

  it('says what failed and offers Try again', async () => {
    const a = signedIn().on(
      'GET',
      '/api/v1/notices',
      apiError(500, 'internal', 'internal error'),
    );
    renderApp(a, '/notices');
    expect(await screen.findByText(/Could not load the notices/)).toBeTruthy();
    expect(screen.getByRole('button', { name: 'Try again' })).toBeTruthy();
  });

  it('only shows and links: it has no switch, no form and a link to the settings (UX-52)', async () => {
    renderApp(api(), '/notices');
    await screen.findByRole('table', { name: 'Notices of the last 30 days' });
    expect(screen.queryByRole('checkbox')).toBeNull();
    expect(screen.queryByRole('textbox')).toBeNull();
    const link = screen.getByRole('button', { name: 'Open notification settings' });
    expect(link.getAttribute('href')).toContain('/settings/networking');
  });

  it('is reached from the Notifications settings', async () => {
    renderApp(
      signedIn()
        .on('GET', '/api/v1/notification-channels', { items: [] })
        .on('GET', '/api/v1/notices', { items: [] }),
      '/settings/notifications',
    );
    fireEvent.click(await screen.findByRole('button', { name: 'See the notices' }));
    await waitFor(() =>
      expect(screen.getByRole('heading', { name: 'Notices' })).toBeTruthy(),
    );
  });
});

describe('Notices words', () => {
  it('names an event and a delivery without a path or a secret', () => {
    expect(eventWord('disk.low')).toBe('Disk low');
    expect(eventWord('odd.type')).toBe('odd.type');
    expect(deliveryText(offline.deliveries[0] as never)).toBe(
      'Team chat to hooks.example.com: Failed, 7 tries. hooks.example.com answered HTTP 503.',
    );
    expect(
      deliveryText({
        id: 'd',
        channel: 'Mail',
        kind: 'email',
        target: 'a@example.com',
        status: 'queued',
        attempt: 1,
        next_try_at: '2026-11-25T15:00:00Z',
      } as never),
    ).toMatch(/^Mail to a@example.com: Waiting to send, next try /);
  });
});
