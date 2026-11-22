// operations R5.90: the controller address is chosen in the console. The dialog shows the loading state, the
// three choices (each with its address and who can reach it), the wait while the controller restarts, the done
// state and a failure with its reason.
import { cleanup, fireEvent, screen, waitFor, within } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it } from 'vitest';
import { TIMING } from '../../timing';
import { apiError, renderApp, signedIn, viewer } from '../../testing/mock';

afterEach(cleanup);
beforeEach(() => {
  TIMING.planPollMs = 10;
});

const candidates = [
  {
    id: 'pc',
    label: "This PC's network address",
    address: '192.168.1.20:8460',
    who: 'Runners and machines on your network, and containers on this PC.',
    recommended: true,
  },
  {
    id: 'docker',
    label: 'Docker and WSL on this PC only',
    address: '172.28.0.1:8460',
    who: 'Containers and WSL on this PC. No other machine.',
  },
  {
    id: 'other',
    label: 'Another address',
    who: 'Whoever can reach the address you type. It must be an address of this PC.',
  },
];

const listen = (extra: object = {}) => ({
  running: '127.0.0.1:8460',
  from_env: false,
  candidates,
  ...extra,
});

const signinAddress = (extra: object = {}) => ({
  address: 'https://192.168.1.20:8460',
  callback_url: 'https://192.168.1.20:8460/auth/github/callback',
  app_url: 'https://github.com/settings/apps/crsp-test',
  registered: true,
  ...extra,
});

function open() {
  fireEvent.click(screen.getByRole('button', { name: 'Choose an address' }));
}

// The page has radios of its own (the theme), so the choices are read inside the dialog.
const inDialog = async () => within(await screen.findByRole('dialog'));

describe('Settings > Console > Network', () => {
  it('shows the address and opens the dialog with the three choices, the PC one chosen', async () => {
    renderApp(
      signedIn().on('GET', '/api/v1/settings/listen-address', listen()),
      '/settings/console',
    );
    expect(await screen.findByText('Network')).toBeTruthy();
    expect(await screen.findByText('127.0.0.1:8460')).toBeTruthy();
    open();
    const d = await inDialog();
    expect(await d.findAllByRole('radio')).toHaveLength(3);
    expect(
      d.getByRole('radio', { name: /192\.168\.1\.20:8460.*containers on this PC/ }),
    ).toHaveProperty('checked', true);
    expect(
      d.getByRole('radio', { name: /172\.28\.0\.1:8460.*No other machine/ }),
    ).toBeTruthy();
    expect(d.getByRole('button', { name: 'Cancel' })).toBeTruthy();
    expect(d.getByRole('button', { name: 'Use this address' })).toBeTruthy();
  });

  it('applies the choice, waits while the controller restarts, then says it answers', async () => {
    let gets = 0;
    const a = signedIn()
      .on('GET', '/api/v1/settings/listen-address', () => {
        gets += 1;
        // 1 and 2: the page and the dialog. 3: the controller is gone. 4: still old. 5: answers.
        if (gets === 3) return new Response('down', { status: 502 });
        return listen(gets >= 5 ? { running: '192.168.1.20:8460' } : {});
      })
      .on('POST', '/api/v1/settings/signin-address', () => signinAddress())
      .on('PUT', '/api/v1/settings/listen-address', (c) => ({
        address: (c.body as { address: string }).address,
        restarting: true,
      }));
    renderApp(a, '/settings/console');
    await screen.findByText('127.0.0.1:8460');
    open();
    await (await inDialog()).findAllByRole('radio');
    fireEvent.click(screen.getByRole('button', { name: 'Use this address' }));
    expect(await screen.findByText(/Waiting for running jobs to finish/)).toBeTruthy();
    expect(a.calls.find((c) => c.method === 'PUT')?.body).toEqual({
      address: '192.168.1.20:8460',
    });
    expect(
      await screen.findByText(/The controller answers on 192\.168\.1\.20:8460/),
    ).toBeTruthy();
    expect(screen.getByRole('button', { name: 'Close' })).toBeTruthy();
  });

  it('sends a typed address', async () => {
    const a = signedIn()
      .on('GET', '/api/v1/settings/listen-address', listen())
      .on('POST', '/api/v1/settings/signin-address', () => signinAddress())
      .on('PUT', '/api/v1/settings/listen-address', (c) => ({
        address: (c.body as { address: string }).address,
        restarting: true,
      }));
    renderApp(a, '/settings/console');
    await screen.findByText('127.0.0.1:8460');
    open();
    fireEvent.click(
      await (await inDialog()).findByRole('radio', { name: /Another address/ }),
    );
    const button = screen.getByRole('button', { name: 'Use this address' });
    expect(
      button.hasAttribute('disabled') || button.getAttribute('aria-disabled') === 'true',
    ).toBe(true);
    fireEvent.change(screen.getByLabelText(/Address as host:port/), {
      target: { value: '10.0.0.5:9000' },
    });
    fireEvent.click(screen.getByRole('button', { name: 'Use this address' }));
    await waitFor(() =>
      expect(a.calls.find((c) => c.method === 'PUT')?.body).toEqual({
        address: '10.0.0.5:9000',
      }),
    );
  });

  it('says why when the address is refused', async () => {
    const a = signedIn()
      .on('GET', '/api/v1/settings/listen-address', listen())
      .on('POST', '/api/v1/settings/signin-address', () => signinAddress())
      .on('PUT', '/api/v1/settings/listen-address', () =>
        apiError(400, 'bad_request', '10.1.1.1 is not an address of this PC'),
      );
    renderApp(a, '/settings/console');
    await screen.findByText('127.0.0.1:8460');
    open();
    await (await inDialog()).findAllByRole('radio');
    fireEvent.click(screen.getByRole('button', { name: 'Use this address' }));
    expect(await screen.findByText(/Could not use this address/)).toBeTruthy();
    expect(screen.getByText(/10\.1\.1\.1 is not an address of this PC/)).toBeTruthy();
    expect(screen.getByRole('button', { name: 'Use this address' })).toBeTruthy();
  });

  it('says why when the controller could not listen on the new address', async () => {
    let gets = 0;
    const a = signedIn()
      .on('GET', '/api/v1/settings/listen-address', () => {
        gets += 1;
        return listen(
          gets >= 3
            ? { problem: 'the controller could not listen on 192.168.1.20:8460 (in use)' }
            : {},
        );
      })
      .on('POST', '/api/v1/settings/signin-address', () => signinAddress())
      .on('PUT', '/api/v1/settings/listen-address', {
        address: '192.168.1.20:8460',
        restarting: true,
      });
    renderApp(a, '/settings/console');
    await screen.findByText('127.0.0.1:8460');
    open();
    await (await inDialog()).findAllByRole('radio');
    fireEvent.click(screen.getByRole('button', { name: 'Use this address' }));
    expect(
      await screen.findByText(/The controller did not listen on the new address/),
    ).toBeTruthy();
    expect(screen.getAllByText(/\(in use\)/).length).toBeGreaterThan(0);
  });

  it('shows an address set by the environment read-only', async () => {
    renderApp(
      signedIn().on(
        'GET',
        '/api/v1/settings/listen-address',
        listen({ from_env: true, env_name: 'CRSP_LISTEN_ADDR' }),
      ),
      '/settings/console',
    );
    expect(await screen.findByText(/set by CRSP_LISTEN_ADDR on this PC/)).toBeTruthy();
    const button = screen.getByRole('button', { name: 'Choose an address' });
    expect(
      button.hasAttribute('disabled') || button.getAttribute('aria-disabled') === 'true',
    ).toBe(true);
  });

  it('a viewer cannot choose', async () => {
    renderApp(
      signedIn(viewer).on('GET', '/api/v1/settings/listen-address', listen()),
      '/settings/console',
    );
    const button = await screen.findByRole('button', { name: 'Choose an address' });
    expect(
      button.hasAttribute('disabled') || button.getAttribute('aria-disabled') === 'true',
    ).toBe(true);
  });
});

describe('Choose an address dialog', () => {
  // OPS16: GitHub only signs people in on addresses the App has a callback URL for.
  it('shows the guided step before switching to an address GitHub does not know, and Try again switches once it does', async () => {
    let known = false;
    const a = signedIn()
      .on('GET', '/api/v1/settings/listen-address', listen())
      .on('POST', '/api/v1/settings/signin-address', () =>
        signinAddress({ registered: known }),
      )
      .on('PUT', '/api/v1/settings/listen-address', (c) => ({
        address: (c.body as { address: string }).address,
        restarting: true,
      }));
    renderApp(a, '/settings/console');
    await screen.findByText('127.0.0.1:8460');
    open();
    await (await inDialog()).findAllByRole('radio');
    fireEvent.click(screen.getByRole('button', { name: 'Use this address' }));
    expect(
      await screen.findByText('Add this sign-in address to your GitHub App'),
    ).toBeTruthy();
    expect(screen.getByLabelText('Callback URL').textContent).toBe(
      'https://192.168.1.20:8460/auth/github/callback',
    );
    expect(screen.getByRole('button', { name: 'Copy' })).toBeTruthy();
    expect(
      screen.getByRole('button', { name: 'Open the App on GitHub' }).getAttribute('href'),
    ).toBe('https://github.com/settings/apps/crsp-test');
    // Nothing was switched.
    expect(a.calls.some((c) => c.method === 'PUT')).toBe(false);
    // Try again before it was added: still the guide, still no switch.
    fireEvent.click((await inDialog()).getByRole('button', { name: 'Try again' }));
    await waitFor(() =>
      expect(
        a.calls.filter((c) => c.path === '/api/v1/settings/signin-address'),
      ).toHaveLength(2),
    );
    expect(a.calls.some((c) => c.method === 'PUT')).toBe(false);
    // Added on GitHub: Try again confirms and the controller switches.
    known = true;
    fireEvent.click(await (await inDialog()).findByRole('button', { name: 'Try again' }));
    expect(await screen.findByText(/Waiting for running jobs to finish/)).toBeTruthy();
    expect(a.calls.find((c) => c.method === 'PUT')?.body).toEqual({
      address: '192.168.1.20:8460',
    });
  });

  it('does not offer a link that is not a GitHub page', async () => {
    const a = signedIn()
      .on('GET', '/api/v1/settings/listen-address', listen())
      .on('POST', '/api/v1/settings/signin-address', () =>
        signinAddress({ registered: false, app_url: 'https://evil.example/x' }),
      );
    renderApp(a, '/settings/console');
    await screen.findByText('127.0.0.1:8460');
    open();
    await (await inDialog()).findAllByRole('radio');
    fireEvent.click(screen.getByRole('button', { name: 'Use this address' }));
    await screen.findByText('Add this sign-in address to your GitHub App');
    expect(screen.queryByRole('button', { name: 'Open the App on GitHub' })).toBeNull();
  });
});
