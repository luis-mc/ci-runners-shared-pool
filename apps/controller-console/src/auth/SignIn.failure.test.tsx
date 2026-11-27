// OPS17: every sign-in failure code shows its own plain message.
import { cleanup, screen } from '@testing-library/react';
import { afterEach, describe, expect, it } from 'vitest';
import { apiError, renderApp, signedIn } from '../testing/mock';

const signedOut = () =>
  signedIn().on('GET', '/api/v1/me', () => apiError(401, 'unauthenticated', 'Sign in.'));

afterEach(cleanup);

describe('Sign-in page, failure codes', () => {
  it.each([
    ['state_missing', /took too long or was already used/],
    ['state_mismatch', /started on another address/],
    ['exchange_failed', /did not accept the sign-in/],
    ['sign_in_failed', /Sign-in failed\. Try again\./],
    ['something_new', /Sign-in failed\. Try again\./],
  ])('shows words for %s', async (code, words) => {
    renderApp(signedOut(), '/sign-in?error=' + code);
    expect(await screen.findByText(words)).toBeTruthy();
    expect(screen.queryByText(/\(internal\)/)).toBeNull();
  });

  it('names the account that is not on the list', async () => {
    renderApp(signedOut(), '/sign-in?error=not_allowed&login=stranger');
    expect(await screen.findByText(/stranger is not on the list/)).toBeTruthy();
  });
});
