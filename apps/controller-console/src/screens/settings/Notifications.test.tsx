// notifications R5.7 to R5.9, R5.19, R5.29 to R5.31; web-console 4.9, R5.30 to R5.32: the Notifications settings
// sub-page. Channels in a table; an add and edit form that ends with Cancel and Save; Send test with a busy
// button and one result line; delete asks first and names the channel and its events; a secret is never shown.
import { cleanup, fireEvent, screen, waitFor, within } from '@testing-library/react';
import { afterEach, describe, expect, it } from 'vitest';
import { apiError, renderApp, signedIn, viewer, type MockApi } from '../../testing/mock';
import { section } from './testSection';

afterEach(cleanup);

const hook = {
  id: 'nch_1',
  kind: 'webhook',
  name: 'Team chat',
  scope: 'pool',
  enabled: true,
  events: ['machine.offline', 'job.waiting'],
  min_severity: 'warning',
  url: 'https://hooks.example.com/crsp/T000/xyz',
  secret_set: true,
  last_test_at: '2026-11-14T10:00:00Z',
  last_test_ok: true,
};
const mail = {
  id: 'nch_2',
  kind: 'email',
  name: 'My mail',
  scope: 'personal',
  enabled: false,
  events: ['disk.low'],
  min_severity: 'error',
  address: 'alice@example.com',
  secret_set: false,
  smtp: {
    host: 'smtp.example.com',
    port: 587,
    username: 'mailer',
    from: 'crsp@example.com',
    has_password: true,
  },
};

const api = (items: unknown[] = [hook, mail]) =>
  signedIn().on('GET', '/api/v1/notification-channels', { items });

const bodyOf = (a: MockApi, method: string) =>
  a.calls.find((c) => c.method === method)?.body as Record<string, unknown>;

// A field marked optional carries "(optional)" after its name.
const fill = (label: string, value: string) =>
  fireEvent.change(
    screen.getByLabelText((text) => text === label || text === `${label} (optional)`),
    { target: { value } },
  );

describe('Notifications: the channel table', () => {
  it('lists each channel with its kind, target, events, severity, state and last test', async () => {
    renderApp(api(), '/settings/notifications');
    const table = await screen.findByRole('table', { name: 'Notification channels' });
    expect(within(table).getByText('Team chat')).toBeTruthy();
    // A webhook shows its host only: the path can hold a token.
    expect(within(table).getByText('hooks.example.com')).toBeTruthy();
    expect(within(table).queryByText(/T000/)).toBeNull();
    expect(within(table).getByText('Machine offline, Job waiting')).toBeTruthy();
    expect(within(table).getByText('alice@example.com')).toBeTruthy();
    expect(within(table).getByText('Error')).toBeTruthy();
    expect(within(table).getByText('Off')).toBeTruthy();
    expect(within(table).getByText('Delivered')).toBeTruthy();
    expect(within(table).getByText('Not tested')).toBeTruthy();
    expect(screen.getByRole('tab', { name: 'Console' })).toBeTruthy();
  });

  it('says what to do when there is no channel', async () => {
    renderApp(api([]), '/settings/notifications');
    expect(
      await screen.findByText(
        'No channels yet. Add a channel to be told when a machine, job or disk needs you.',
      ),
    ).toBeTruthy();
  });

  it('says what failed and offers Try again when the list does not load', async () => {
    const a = signedIn().on(
      'GET',
      '/api/v1/notification-channels',
      apiError(500, 'internal', 'internal error'),
    );
    renderApp(a, '/settings/notifications');
    expect(
      await screen.findByText(/Could not load the notification channels/),
    ).toBeTruthy();
    expect(
      section('notifications').getByRole('button', { name: 'Try again' }),
    ).toBeTruthy();
  });
});

describe('Notifications: add and edit', () => {
  it('adds a webhook with a made secret; Cancel and Save are in the bar', async () => {
    const a = api([]).on('POST', '/api/v1/notification-channels', (c) => ({
      ...(c.body as object),
      id: 'nch_9',
      scope: 'pool',
      secret_set: true,
    }));
    renderApp(a, '/settings/notifications');
    fireEvent.click(await screen.findByRole('button', { name: 'Add channel' }));
    const form = screen.getByRole('region', { name: 'Add a channel' });
    const bar = screen.getByRole('region', { name: 'Actions' });
    expect(within(bar).getByText('Unsaved changes (1)')).toBeTruthy();
    expect(
      within(bar)
        .getAllByRole('button')
        .map((b) => b.textContent),
    ).toEqual(['Cancel', 'Save']);
    fireEvent.click(within(form).getByRole('radio', { name: /^Webhook/ }));
    fill('Name', 'Team chat');
    fill('Web address', 'https://hooks.example.com/crsp');
    fireEvent.click(screen.getByRole('button', { name: 'Make a secret' }));
    const secret = screen.getByLabelText<HTMLInputElement>('Signing secret').value;
    expect(secret).toMatch(/^[0-9a-f]{64}$/);
    fireEvent.click(screen.getByRole('checkbox', { name: 'Job waiting' }));
    fireEvent.click(screen.getByRole('radio', { name: 'Error' }));
    fireEvent.click(screen.getByRole('button', { name: 'Save' }));
    await waitFor(() => expect(a.count('POST', '/api/v1/notification-channels')).toBe(1));
    expect(bodyOf(a, 'POST')).toEqual({
      kind: 'webhook',
      name: 'Team chat',
      enabled: true,
      events: ['machine.offline', 'job.waiting'],
      min_severity: 'error',
      url: 'https://hooks.example.com/crsp',
      secret,
    });
    expect(await screen.findByText('Channel Team chat saved.')).toBeTruthy();
    expect(screen.queryByRole('region', { name: 'Add a channel' })).toBeNull();
  });

  it('adds an email channel with its mail server', async () => {
    const a = api([]).on('POST', '/api/v1/notification-channels', (c) => ({
      ...(c.body as object),
      id: 'nch_9',
      scope: 'personal',
      secret_set: false,
    }));
    renderApp(a, '/settings/notifications');
    fireEvent.click(await screen.findByRole('button', { name: 'Add channel' }));
    fill('Name', 'My mail');
    fill('Address', 'alice@example.com');
    fill('Mail server', 'smtp.example.com');
    fill('User name', 'mailer');
    fill('Password', 'pw-secret-1');
    fill('Sender', 'crsp@example.com');
    expect(screen.getByLabelText<HTMLInputElement>(/^Password/).type).toBe('password');
    fireEvent.click(screen.getByRole('button', { name: 'Save' }));
    await waitFor(() => expect(a.count('POST', '/api/v1/notification-channels')).toBe(1));
    expect(bodyOf(a, 'POST')).toEqual({
      kind: 'email',
      name: 'My mail',
      enabled: true,
      events: ['machine.offline'],
      min_severity: 'warning',
      address: 'alice@example.com',
      smtp: {
        host: 'smtp.example.com',
        port: 587,
        from: 'crsp@example.com',
        username: 'mailer',
        password: 'pw-secret-1',
      },
    });
  });

  it('shows every mistake at the top and sends nothing', async () => {
    const a = api([]);
    renderApp(a, '/settings/notifications');
    fireEvent.click(await screen.findByRole('button', { name: 'Add channel' }));
    fireEvent.click(screen.getByRole('checkbox', { name: 'Machine offline' }));
    fireEvent.click(screen.getByRole('button', { name: 'Save' }));
    expect(await screen.findByText('Write a name for the channel.')).toBeTruthy();
    expect(screen.getByText('Pick at least one event.')).toBeTruthy();
    expect(
      screen.getByText(
        'Write the address as an email address, for example name@example.com.',
      ),
    ).toBeTruthy();
    expect(a.count('POST', '/api/v1/notification-channels')).toBe(0);
  });

  it('refuses a webhook address that is not https and a short secret', async () => {
    const a = api([]);
    renderApp(a, '/settings/notifications');
    fireEvent.click(await screen.findByRole('button', { name: 'Add channel' }));
    fireEvent.click(screen.getByRole('radio', { name: /^Webhook/ }));
    fill('Name', 'x');
    fill('Web address', 'http://hooks.example.com');
    fill('Signing secret', 'short');
    fireEvent.click(screen.getByRole('button', { name: 'Save' }));
    expect(
      await screen.findByText('Write the webhook address as an https URL.'),
    ).toBeTruthy();
    expect(screen.getByText(/at least 16 characters/)).toBeTruthy();
    expect(a.count('POST', '/api/v1/notification-channels')).toBe(0);
  });

  it('shows the answer of the server when it refuses the channel', async () => {
    const a = api([]).on(
      'POST',
      '/api/v1/notification-channels',
      apiError(400, 'bad_request', 'The mail server points to this machine.'),
    );
    renderApp(a, '/settings/notifications');
    fireEvent.click(await screen.findByRole('button', { name: 'Add channel' }));
    fireEvent.click(screen.getByRole('radio', { name: /^Desktop/ }));
    fill('Name', 'Desk');
    fireEvent.click(screen.getByRole('button', { name: 'Save' }));
    expect(await screen.findByText(/Could not save the channel/)).toBeTruthy();
    expect(screen.getByText(/points to this machine/)).toBeTruthy();
    expect(screen.getByRole('region', { name: 'Add a channel' })).toBeTruthy();
  });

  it('Cancel leaves the form and sends nothing', async () => {
    const a = api();
    renderApp(a, '/settings/notifications');
    fireEvent.click(await screen.findByRole('button', { name: 'Add channel' }));
    fill('Name', 'Draft');
    fireEvent.click(screen.getByRole('button', { name: 'Cancel' }));
    expect(screen.queryByRole('region', { name: 'Add a channel' })).toBeNull();
    expect(a.calls.filter((c) => c.method !== 'GET')).toHaveLength(0);
  });

  it('edits a channel: the secret is not filled in, and a secret left empty is not sent', async () => {
    const a = api().on('PATCH', '/api/v1/notification-channels/:id', hook);
    renderApp(a, '/settings/notifications');
    const table = await screen.findByRole('table', { name: 'Notification channels' });
    fireEvent.click(within(table).getAllByRole('button', { name: 'Edit' })[0]!);
    const form = await screen.findByRole('region', { name: 'Edit Team chat' });
    expect(within(form).getByLabelText<HTMLInputElement>('Name').value).toBe('Team chat');
    expect(within(form).getByLabelText<HTMLInputElement>(/Signing secret/).value).toBe(
      '',
    );
    expect(
      within(form).getByText(/A secret is set\. Leave this empty to keep it/),
    ).toBeTruthy();
    expect(within(form).queryByRole('radio', { name: /^Webhook/ })).toBeNull(); // the kind never changes
    fill('Name', 'Renamed');
    fireEvent.click(screen.getByRole('button', { name: 'Save' }));
    await waitFor(() =>
      expect(a.count('PATCH', '/api/v1/notification-channels/nch_1')).toBe(1),
    );
    const body = bodyOf(a, 'PATCH');
    expect(body.name).toBe('Renamed');
    expect('secret' in body).toBe(false);
  });

  it('removes a stored password only when asked', async () => {
    const a = api().on('PATCH', '/api/v1/notification-channels/:id', mail);
    renderApp(a, '/settings/notifications');
    const table = await screen.findByRole('table', { name: 'Notification channels' });
    fireEvent.click(within(table).getAllByRole('button', { name: 'Edit' })[1]!);
    const form = await screen.findByRole('region', { name: 'Edit My mail' });
    expect(within(form).getByLabelText<HTMLInputElement>(/Password/).value).toBe('');
    fireEvent.click(within(form).getByRole('checkbox', { name: 'Remove the password' }));
    fireEvent.click(screen.getByRole('button', { name: 'Save' }));
    await waitFor(() =>
      expect(a.count('PATCH', '/api/v1/notification-channels/nch_2')).toBe(1),
    );
    expect((bodyOf(a, 'PATCH').smtp as { password: string }).password).toBe('');
  });

  it('a viewer cannot pick a webhook and is told why', async () => {
    renderApp(
      signedIn(viewer).on('GET', '/api/v1/notification-channels', { items: [] }),
      '/settings/notifications',
    );
    fireEvent.click(await screen.findByRole('button', { name: 'Add channel' }));
    const radio = screen.getByRole<HTMLInputElement>('radio', { name: /^Webhook/ });
    expect(radio.disabled).toBe(true);
    expect(screen.getByText(/Only an admin can do this\./)).toBeTruthy();
  });
});

describe('Notifications: Send test', () => {
  it('is busy and says so while it runs, then shows one result line', async () => {
    let open!: () => void;
    const gate = new Promise<void>((r) => (open = r));
    const a = api().on('POST', '/api/v1/notification-channels/:id/test', {
      ok: true,
      message: 'Delivered. hooks.example.com answered HTTP 204.',
      sent_at: '2026-11-14T10:05:00Z',
    });
    const plain = a.fetch;
    a.fetch = async (input) => {
      const url = typeof input === 'string' ? input : input.url;
      if (url.endsWith('/test')) await gate;
      return plain(input);
    };
    renderApp(a, '/settings/notifications');
    const table = await screen.findByRole('table', { name: 'Notification channels' });
    fireEvent.click(within(table).getAllByRole('button', { name: 'Send test' })[0]!);
    const busy = await within(table).findByRole('button', { name: /Sending/ });
    expect((busy as HTMLButtonElement).disabled).toBe(true);
    // The other channel's button waits and says why.
    expect(within(table).getByText('Another test is running.')).toBeTruthy();
    expect(screen.getByText('Sending a test. Waiting for the answer.')).toBeTruthy();
    open();
    expect(
      await screen.findByText(
        /Team chat: Delivered\. hooks\.example\.com answered HTTP 204\./,
      ),
    ).toBeTruthy();
    await waitFor(() =>
      expect(within(table).queryByRole('button', { name: /Sending/ })).toBeNull(),
    );
    expect(a.count('POST', '/api/v1/notification-channels/nch_1/test')).toBe(1);
  });

  it('says what failed when the delivery failed', async () => {
    const a = api().on('POST', '/api/v1/notification-channels/:id/test', {
      ok: false,
      message: 'Could not reach hooks.example.com. The connection timed out after 10 s.',
      sent_at: '2026-11-14T10:05:00Z',
    });
    renderApp(a, '/settings/notifications');
    const table = await screen.findByRole('table', { name: 'Notification channels' });
    fireEvent.click(within(table).getAllByRole('button', { name: 'Send test' })[0]!);
    expect(
      await screen.findByText(/Test failed\. Team chat: Could not reach/),
    ).toBeTruthy();
  });

  it('shows a refused request as a problem line', async () => {
    const a = api().on(
      'POST',
      '/api/v1/notification-channels/:id/test',
      apiError(
        429,
        'rate_limited',
        'a test of this channel was sent a moment ago; wait 10 seconds',
      ),
    );
    renderApp(a, '/settings/notifications');
    const table = await screen.findByRole('table', { name: 'Notification channels' });
    fireEvent.click(within(table).getAllByRole('button', { name: 'Send test' })[0]!);
    expect(await screen.findByText(/Could not send the test\./)).toBeTruthy();
  });
});

describe('Notifications: delete', () => {
  it('asks first, names the channel and its events, and deletes only after the yes (UX-62)', async () => {
    const a = api().on('DELETE', '/api/v1/notification-channels/:id', undefined);
    renderApp(a, '/settings/notifications');
    const table = await screen.findByRole('table', { name: 'Notification channels' });
    fireEvent.click(within(table).getAllByRole('button', { name: 'Delete' })[0]!);
    const dialog = await screen.findByRole('dialog', { name: 'Delete Team chat?' });
    expect(
      within(dialog).getByText(
        'Channel Team chat stops carrying Machine offline, Job waiting.',
      ),
    ).toBeTruthy();
    expect(a.count('DELETE', '/api/v1/notification-channels/nch_1')).toBe(0);
    fireEvent.click(within(dialog).getByRole('button', { name: 'Delete' }));
    await waitFor(() =>
      expect(a.count('DELETE', '/api/v1/notification-channels/nch_1')).toBe(1),
    );
    const call = a.calls.find((c) => c.method === 'DELETE');
    expect(call?.query.get('confirm')).toBe('true');
    expect(await screen.findByText('Channel Team chat deleted.')).toBeTruthy();
  });

  it('Cancel on the question deletes nothing', async () => {
    const a = api();
    renderApp(a, '/settings/notifications');
    const table = await screen.findByRole('table', { name: 'Notification channels' });
    fireEvent.click(within(table).getAllByRole('button', { name: 'Delete' })[0]!);
    const dialog = await screen.findByRole('dialog', { name: 'Delete Team chat?' });
    fireEvent.click(within(dialog).getByRole('button', { name: 'Cancel' }));
    expect(screen.queryByRole('dialog')).toBeNull();
    expect(a.calls.filter((c) => c.method === 'DELETE')).toHaveLength(0);
  });
});
