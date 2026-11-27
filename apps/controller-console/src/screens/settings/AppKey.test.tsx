// github-integration R5.7: the owner replaces the App private key from Settings. The key is sent once, the screen
// never shows it, and a refused key says so.
import { cleanup, fireEvent, screen, waitFor } from '@testing-library/react';
import { afterEach, describe, expect, it } from 'vitest';
import { apiError, renderApp, signedIn, viewer } from '../../testing/mock';

afterEach(cleanup);

const PEM = '-----BEGIN RSA PRIVATE KEY-----\nTESTONLY\n-----END RSA PRIVATE KEY-----\n';

function choose(text: string) {
  const input = screen.getByLabelText<HTMLInputElement>('New private key file');
  const file = new File([text], 'app.pem', { type: 'application/x-pem-file' });
  fireEvent.change(input, { target: { files: [file] } });
}

describe('GitHub App key (R5.7)', () => {
  it('sends the chosen file once and shows a done line without the key', async () => {
    const a = signedIn().on('PUT', '/api/v1/app/private-key', undefined);
    renderApp(a, '/settings/app');
    await screen.findByRole('heading', { name: 'GitHub App key' });
    choose(PEM);
    fireEvent.click(screen.getByRole('button', { name: 'Replace key' }));
    expect(await screen.findByText('The new key is in use.')).toBeTruthy();
    expect(a.calls.find((c) => c.method === 'PUT')?.body).toEqual({ pem: PEM });
    expect(document.body.textContent).not.toContain('TESTONLY');
  });

  it('says what GitHub refused and keeps the page usable', async () => {
    const a = signedIn().on('PUT', '/api/v1/app/private-key', () =>
      apiError(
        400,
        'bad_request',
        'GitHub does not accept this key. The old key was kept.',
      ),
    );
    renderApp(a, '/settings/app');
    await screen.findByRole('heading', { name: 'GitHub App key' });
    choose(PEM);
    fireEvent.click(screen.getByRole('button', { name: 'Replace key' }));
    expect(await screen.findByText(/The old key was kept/)).toBeTruthy();
    expect(document.body.textContent).not.toContain('TESTONLY');
    await waitFor(() =>
      expect(
        screen.getByRole<HTMLButtonElement>('button', { name: 'Replace key' }).disabled,
      ).toBe(false),
    );
  });

  it('asks for a file when none is chosen and sends nothing', async () => {
    const a = signedIn();
    renderApp(a, '/settings/app');
    await screen.findByRole('heading', { name: 'GitHub App key' });
    fireEvent.click(screen.getByRole('button', { name: 'Replace key' }));
    expect(
      await screen.findByText('Choose the private key file from GitHub.'),
    ).toBeTruthy();
    expect(a.count('PUT', '/api/v1/app/private-key')).toBe(0);
  });

  it('is locked for a viewer, with its reason', async () => {
    renderApp(signedIn(viewer), '/settings/app');
    await screen.findByRole('heading', { name: 'GitHub App key' });
    expect(
      screen.getByRole<HTMLButtonElement>('button', { name: 'Replace key' }).disabled,
    ).toBe(true);
    expect(screen.getByLabelText<HTMLInputElement>('New private key file').disabled).toBe(
      true,
    );
  });
});
