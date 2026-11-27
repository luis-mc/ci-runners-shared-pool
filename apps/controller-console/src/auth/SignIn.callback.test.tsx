// OPS16: a sign-in that cannot work on this address shows the guided step, never GitHub's error page.
import { cleanup, screen } from '@testing-library/react';
import { afterEach, describe, expect, it } from 'vitest';
import { apiError, renderApp, signedIn } from '../testing/mock';

const signedOut = () =>
  signedIn().on('GET', '/api/v1/me', () => apiError(401, 'unauthenticated', 'Sign in.'));

afterEach(cleanup);

describe('Sign-in page, address GitHub does not know', () => {
  it('gives the exact callback URL, Copy and the App page', async () => {
    renderApp(
      signedOut(),
      '/sign-in?error=callback_not_registered&address=' +
        encodeURIComponent('https://192.168.1.162:8460') +
        '&callback=' +
        encodeURIComponent('https://192.168.1.162:8460/auth/github/callback') +
        '&app_url=' +
        encodeURIComponent('https://github.com/settings/apps/crsp-test'),
    );
    expect(
      await screen.findByText(/does not work on https:\/\/192\.168\.1\.162:8460 yet/),
    ).toBeTruthy();
    expect(screen.getByLabelText('Callback URL').textContent).toBe(
      'https://192.168.1.162:8460/auth/github/callback',
    );
    expect(screen.getByRole('button', { name: 'Copy' })).toBeTruthy();
    expect(
      screen.getByRole('button', { name: 'Open the App on GitHub' }).getAttribute('href'),
    ).toBe('https://github.com/settings/apps/crsp-test');
    expect(screen.getByRole('button', { name: 'Try again' })).toBeTruthy();
  });

  it('offers no link to a page that is not on GitHub', async () => {
    renderApp(
      signedOut(),
      '/sign-in?error=callback_not_registered&callback=' +
        encodeURIComponent('https://pool.lan:8460/auth/github/callback') +
        '&app_url=' +
        encodeURIComponent('https://evil.example/x'),
    );
    await screen.findByLabelText('Callback URL');
    expect(screen.queryByRole('button', { name: 'Open the App on GitHub' })).toBeNull();
  });
});
