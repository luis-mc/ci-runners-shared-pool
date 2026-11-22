// R5.113, web-console R5.110: Switch to crsp runners. The preview shows the files and the diff, the pull request opens
// only on the second press, the button is busy while it runs, an error is said, and Done links to the pull request.
import { cleanup, fireEvent, screen, waitFor, within } from '@testing-library/react';
import { afterEach, describe, expect, it } from 'vitest';
import { apiError, renderApp, repository, signedIn, viewer } from '../../testing/mock';

afterEach(cleanup);

const change = {
  repository_id: 7,
  full_name: 'octo/app',
  state: 'change',
  files: [{ path: '.github/workflows/ci.yml', changes: 1, diff: 'd' }],
  diff: '-old runs-on\n+new runs-on\n',
  skipped: [],
};
const opened = {
  repository_id: 7,
  full_name: 'octo/app',
  state: 'opened',
  number: 3,
  url: 'https://github.com/octo/app/pull/3',
};

function world(api = signedIn()) {
  return api.on('GET', '/api/v1/github/repositories', {
    items: [repository({ pool_on: true })],
    stale: { stale: false },
  });
}
const pane = async () =>
  (
    await screen.findByRole('heading', { level: 2, name: 'Switch to crsp runners' })
  ).closest('.ds-pane') as HTMLElement;

describe('Switch to crsp runners', () => {
  it('on the list: shows the diff first and opens nothing', async () => {
    const api = world().on('GET', '/api/v1/runs-on-pr', { items: [change] });
    renderApp(api, '/repositories');
    const p = await pane();
    fireEvent.click(
      within(p).getByRole('button', { name: 'Switch all to crsp runners' }),
    );
    expect(await within(p).findByText('1 file changes in octo/app.')).toBeTruthy();
    expect(within(p).getByLabelText('Change in octo/app').textContent).toContain(
      '+new runs-on',
    );
    expect(api.count('POST', '/api/v1/runs-on-pr')).toBe(0);
    expect(
      api.calls
        .find((c) => c.method === 'GET' && c.path === '/api/v1/runs-on-pr')
        ?.query.has('repository_id'),
    ).toBe(false);
  });

  it('opens the pull request after the second press, busy while it runs, then links to it', async () => {
    let release: (v: unknown) => void = () => undefined;
    const api = world()
      .on('GET', '/api/v1/runs-on-pr', { items: [change] })
      .on('POST', '/api/v1/runs-on-pr', () => new Promise((r) => (release = r)));
    renderApp(api, '/repositories');
    const p = await pane();
    fireEvent.click(
      within(p).getByRole('button', { name: 'Switch all to crsp runners' }),
    );
    fireEvent.click(
      await within(p).findByRole('button', { name: 'Open the pull request' }),
    );
    const busy = await within(p).findByRole<HTMLButtonElement>('button', {
      name: 'Opening',
    });
    expect(busy.disabled).toBe(true);
    release({ items: [opened] });
    expect(await within(p).findByText('Pull request opened on octo/app.')).toBeTruthy();
    expect(
      within(p).getByRole('button', { name: 'Open on GitHub' }).getAttribute('href'),
    ).toBe('https://github.com/octo/app/pull/3');
    expect(api.calls.find((c) => c.method === 'POST')?.body).toEqual({ all: true });
  });

  it('on a repository page asks for that repository only', async () => {
    const api = world()
      .on('GET', '/api/v1/repositories/:id', repository({ pool_on: true }))
      .on('GET', '/api/v1/runs-on-pr', { items: [change] })
      .on('POST', '/api/v1/runs-on-pr', { items: [{ ...opened, state: 'existing' }] });
    renderApp(api, '/repositories/octo/app');
    const p = await pane();
    fireEvent.click(within(p).getByRole('button', { name: 'Switch to crsp runners' }));
    fireEvent.click(
      await within(p).findByRole('button', { name: 'Open the pull request' }),
    );
    expect(
      await within(p).findByText('A pull request is already open on octo/app.'),
    ).toBeTruthy();
    expect(
      api.calls
        .find((c) => c.method === 'GET' && c.path === '/api/v1/runs-on-pr')
        ?.query.get('repository_id'),
    ).toBe('7');
    expect(api.calls.find((c) => c.method === 'POST')?.body).toEqual({
      repository_id: 7,
    });
  });

  it('says nothing to change and offers no open button', async () => {
    const api = world().on('GET', '/api/v1/runs-on-pr', {
      items: [{ ...change, state: 'nothing_to_change', files: [], diff: '' }],
    });
    renderApp(api, '/repositories');
    const p = await pane();
    fireEvent.click(
      within(p).getByRole('button', { name: 'Switch all to crsp runners' }),
    );
    expect(await within(p).findByText('Nothing to change in octo/app.')).toBeTruthy();
    expect(within(p).queryByRole('button', { name: /Open the pull request/ })).toBeNull();
  });

  it('shows the error of a failed open and keeps the button', async () => {
    const api = world()
      .on('GET', '/api/v1/runs-on-pr', { items: [change] })
      .on(
        'POST',
        '/api/v1/runs-on-pr',
        apiError(403, 'forbidden', 'you do not have admin rights on octo/app in GitHub'),
      );
    renderApp(api, '/repositories');
    const p = await pane();
    fireEvent.click(
      within(p).getByRole('button', { name: 'Switch all to crsp runners' }),
    );
    fireEvent.click(
      await within(p).findByRole('button', { name: 'Open the pull request' }),
    );
    expect(
      await within(p).findByText(/Could not open the pull request\..*admin rights/),
    ).toBeTruthy();
    await waitFor(() =>
      expect(
        within(p).getByRole<HTMLButtonElement>('button', {
          name: 'Open the pull request',
        }).disabled,
      ).toBe(false),
    );
  });

  it('is disabled for a viewer and says why (UX-38)', async () => {
    renderApp(world(signedIn(viewer)), '/repositories');
    const p = await pane();
    expect(
      within(p).getByRole<HTMLButtonElement>('button', {
        name: 'Switch all to crsp runners',
      }).disabled,
    ).toBe(true);
  });
});
