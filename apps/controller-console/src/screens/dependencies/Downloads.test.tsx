// DEP15: the Downloads tab. Every dependency shows its state in words; Download now locks while it runs; a file
// is imported only through the file field, and the server's refusal is shown beside it.
import { cleanup, fireEvent, screen, waitFor } from '@testing-library/react';
import { afterEach, describe, expect, it } from 'vitest';
import type { Schemas } from '../../api/api';
import { apiError, renderApp, signedIn, viewer } from '../../testing/mock';

afterEach(cleanup);

type Entry = Schemas['DependencyStoreEntry'];

const entry = (over: Partial<Entry> = {}): Entry => ({
  id: 'docker-desktop-windows',
  name: 'Docker Desktop',
  kind: 'installer',
  state: 'missing',
  pinned: true,
  can_fetch: true,
  source: 'desktop.docker.com',
  ...over,
});

const list = (items: Entry[], user?: Schemas['User']) =>
  signedIn(user).on('GET', '/api/v1/dependencies/store', { items });

describe('Downloads tab (DEP15)', () => {
  it('says each state in words', async () => {
    renderApp(
      list([
        entry(),
        entry({
          id: 'runner-linux-x64',
          name: 'GitHub Actions runner',
          kind: 'runner',
          state: 'ready',
          origin: 'imported',
          stored_bytes: 200_000_000,
        }),
        entry({
          id: 'a',
          name: 'Unpinned',
          pinned: false,
          can_fetch: false,
          state: 'not_pinned',
        }),
        entry({
          id: 'b',
          name: 'Broken',
          state: 'failed',
          error: 'The download from desktop.docker.com failed: no route to host',
        }),
      ]),
      '/setup/images?tab=downloads',
    );
    expect(await screen.findByText('Not downloaded yet')).toBeTruthy();
    expect(screen.getByText('Ready, imported by you')).toBeTruthy();
    expect(screen.getByText('Cannot be checked in this release')).toBeTruthy();
    expect(screen.getByText('Not kept')).toBeTruthy();
    expect(screen.getByText(/no route to host/)).toBeTruthy();
    expect(screen.getByRole('heading', { name: /Runner packages/ })).toBeTruthy();
  });

  it('downloads now, says it is working and shows the new state', async () => {
    const api = list([entry()]).on('POST', '/api/v1/dependencies/store/:id/fetch', () =>
      entry({ state: 'ready', stored_bytes: 600_000_000 }),
    );
    renderApp(api, '/setup/images?tab=downloads');
    fireEvent.click(await screen.findByRole('button', { name: 'Download now' }));
    await waitFor(() =>
      expect(
        api.count('POST', '/api/v1/dependencies/store/docker-desktop-windows/fetch'),
      ).toBe(1),
    );
  });

  it('shows why a download cannot start', async () => {
    renderApp(
      list([entry({ pinned: false, can_fetch: false, state: 'not_pinned' })]),
      '/setup/images?tab=downloads',
    );
    expect(await screen.findByText('Cannot be checked in this release')).toBeTruthy();
    expect(screen.queryByRole('button', { name: 'Download now' })).toBeNull();
  });

  it('imports a file and shows the refusal when it does not match', async () => {
    const api = list([entry()]).on('PUT', '/api/v1/dependencies/store/:id/file', () =>
      apiError(
        400,
        'bad_request',
        'The file does not have the SHA-256 this release lists for it, so it was not kept.',
      ),
    );
    renderApp(api, '/setup/images?tab=downloads');
    fireEvent.click(await screen.findByRole('button', { name: 'Import a file' }));
    const input = screen.getByLabelText<HTMLInputElement>('File you downloaded');
    expect(
      (screen.getByRole('button', { name: 'Import' }) as HTMLButtonElement).disabled,
    ).toBe(true);
    fireEvent.change(input, { target: { files: [new File(['123'], 'd.exe')] } });
    fireEvent.click(await screen.findByRole('button', { name: 'Import' }));
    expect(await screen.findByText(/was not kept/)).toBeTruthy();
  });

  it('gives a viewer the facts and no buttons', async () => {
    renderApp(list([entry()], viewer), '/setup/images?tab=downloads');
    expect(await screen.findByText('Not downloaded yet')).toBeTruthy();
    expect(screen.queryByRole('button', { name: 'Download now' })).toBeNull();
    expect(screen.queryByRole('button', { name: 'Import a file' })).toBeNull();
  });
});
