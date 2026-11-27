// github-integration R5.51 (OPS25): the owner turns real webhooks on from Settings. The section shows off, on and
// failing, checks the address before it asks the controller, and says why a refusal happened.
import { cleanup, fireEvent, screen, waitFor } from '@testing-library/react';
import { afterEach, describe, expect, it } from 'vitest';
import { apiError, renderApp, signedIn, viewer } from '../../testing/mock';
import { webhookAddressProblem } from './Webhooks';

afterEach(cleanup);

const PATH = '/api/v1/app/webhook';
const on = (extra: object = {}) => ({
  state: 'on',
  address: 'https://hooks.example.com',
  last_delivery_at: new Date(Date.now() - 120_000).toISOString(),
  last_result: 'ok',
  ...extra,
});

async function open(api: ReturnType<typeof signedIn>) {
  renderApp(api, '/settings/access');
  return screen.findByRole('heading', { name: 'Webhooks' });
}

describe('Settings > Access > Webhooks', () => {
  it('says why in plain words, and shows the off state with polling', async () => {
    await open(signedIn().on('GET', PATH, { state: 'off' }));
    expect(await screen.findByText(/a job can start up to 15 seconds late/)).toBeTruthy();
    expect(
      screen.getByText(/A LAN address or a self-signed certificate does not work/),
    ).toBeTruthy();
    expect(screen.getByText('off')).toBeTruthy();
    expect(screen.queryByLabelText('Public https address')).toBeNull();
  });

  it('shows on with the address and the last delivery time', async () => {
    await open(signedIn().on('GET', PATH, on()));
    expect(await screen.findByText('on')).toBeTruthy();
    expect(screen.getByText(/2 minutes ago|2 min/)).toBeTruthy();
    expect(screen.getByLabelText<HTMLInputElement>('Public https address').value).toBe(
      'https://hooks.example.com',
    );
  });

  it('shows failing when the newest delivery failed', async () => {
    await open(
      signedIn().on('GET', PATH, on({ state: 'failing', last_result: 'failed' })),
    );
    expect(await screen.findByText('failing')).toBeTruthy();
  });

  it('refuses a local or plain address without asking the controller', async () => {
    const a = signedIn().on('GET', PATH, { state: 'off' });
    await open(a);
    fireEvent.click(await screen.findByRole('checkbox', { name: /Real webhooks/ }));
    const field = await screen.findByLabelText('Public https address');
    for (const [typed, words] of [
      ['http://hooks.example.com', /must start with https/],
      ['https://192.168.1.20', /is a local address/],
      ['https://crsp.local', /is a local address/],
      ['', /Write the public address/],
    ] as const) {
      fireEvent.change(field, { target: { value: typed } });
      fireEvent.click(screen.getByRole('button', { name: 'Save' }));
      expect((await screen.findAllByText(words)).length).toBeGreaterThan(0);
    }
    expect(a.calls.filter((c) => c.method === 'PUT')).toHaveLength(0);
  });

  it('saves a good address and says webhooks are on', async () => {
    // After Save the page reads the state again, so the answer follows the save.
    let saved = false;
    const a = signedIn()
      .on('GET', PATH, () => (saved ? on() : { state: 'off' }))
      .on('PUT', PATH, () => {
        saved = true;
        return on();
      });
    await open(a);
    fireEvent.click(await screen.findByRole('checkbox', { name: /Real webhooks/ }));
    fireEvent.change(await screen.findByLabelText('Public https address'), {
      target: { value: ' https://hooks.example.com ' },
    });
    fireEvent.click(screen.getByRole('button', { name: 'Save' }));
    expect(await screen.findByText(/Webhooks are on/)).toBeTruthy();
    expect(await screen.findByText('on')).toBeTruthy();
    expect(a.calls.find((c) => c.method === 'PUT')?.body).toEqual({
      address: 'https://hooks.example.com',
    });
  });

  it('shows the guided link when the App does not listen for workflow jobs', async () => {
    const events = 'https://github.com/settings/apps/crsp/permissions';
    let saved = false;
    const a = signedIn()
      .on('GET', PATH, () => (saved ? on({ events_link: events }) : { state: 'off' }))
      .on('PUT', PATH, () => {
        saved = true;
        return on({ events_link: events });
      });
    await open(a);
    fireEvent.click(await screen.findByRole('checkbox', { name: /Real webhooks/ }));
    fireEvent.change(await screen.findByLabelText('Public https address'), {
      target: { value: 'https://hooks.example.com' },
    });
    fireEvent.click(screen.getByRole('button', { name: 'Save' }));
    expect(await screen.findByText(/tick Workflow job/)).toBeTruthy();
    const button = screen.getByRole('button', { name: 'Open the App on GitHub' });
    expect(button.getAttribute('href')).toBe(events);
  });

  it('says what the controller found when the test fails and keeps the form', async () => {
    const a = signedIn()
      .on('GET', PATH, { state: 'off' })
      .on('PUT', PATH, () =>
        apiError(
          400,
          'bad_request',
          'The controller could not reach https://hooks.example.com: its certificate is not trusted.',
        ),
      );
    await open(a);
    fireEvent.click(await screen.findByRole('checkbox', { name: /Real webhooks/ }));
    fireEvent.change(await screen.findByLabelText('Public https address'), {
      target: { value: 'https://hooks.example.com' },
    });
    fireEvent.click(screen.getByRole('button', { name: 'Save' }));
    expect(await screen.findByText(/certificate is not trusted/)).toBeTruthy();
    await waitFor(() =>
      expect(
        screen.getByRole<HTMLButtonElement>('button', { name: 'Save' }).disabled,
      ).toBe(false),
    );
    expect(screen.getByLabelText<HTMLInputElement>('Public https address').value).toBe(
      'https://hooks.example.com',
    );
  });

  it('turns webhooks off with Save and returns to polling', async () => {
    let cleared = false;
    const a = signedIn()
      .on('GET', PATH, () => (cleared ? { state: 'off' } : on()))
      .on('DELETE', PATH, () => {
        cleared = true;
        return { state: 'off' };
      });
    await open(a);
    fireEvent.click(await screen.findByRole('checkbox', { name: /Real webhooks/ }));
    fireEvent.click(screen.getByRole('button', { name: 'Save' }));
    expect(
      await screen.findByText(/crsp finds jobs by polling GitHub again/),
    ).toBeTruthy();
    expect(a.calls.filter((c) => c.method === 'DELETE')).toHaveLength(1);
    expect(a.calls.filter((c) => c.method === 'PUT')).toHaveLength(0);
  });

  it('keeps Save blocked, with its reason, for someone who is not the owner', async () => {
    // `open` already waited for the heading. The old `findByText('Webhooks')` matched the heading and the
    // Facts label at once, so findBy kept retrying until it hit the 5 s test timeout.
    await open(signedIn(viewer).on('GET', PATH, { state: 'off' }));
    const save = await screen.findByRole<HTMLButtonElement>('button', { name: 'Save' });
    expect(save.disabled || save.getAttribute('aria-disabled') === 'true').toBe(true);
    expect(
      (await screen.findAllByText(/Only the owner can do this/)).length,
    ).toBeGreaterThan(0);
  });
});

describe('webhookAddressProblem', () => {
  it('accepts a public https address', () => {
    expect(webhookAddressProblem('https://hooks.example.com')).toBeUndefined();
    expect(webhookAddressProblem('https://hooks.example.com:8443/crsp')).toBeUndefined();
  });
  it('refuses the rest', () => {
    for (const bad of [
      'hooks.example.com',
      'http://x.example.com',
      'https://localhost',
      'https://127.0.0.1',
      'https://10.1.2.3',
      'https://172.20.0.1',
      'https://[::1]',
      'https://crsp',
      'https://nas.lan',
      'https://u:p@x.example.com',
      'https://x.example.com/?a=1',
    ])
      expect(webhookAddressProblem(bad), bad).toBeTruthy();
  });
});
