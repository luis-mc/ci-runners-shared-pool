// LOOK21 (UX-71, component 48): the repository form counts its changes, marks each changed field, keeps Save and
// Cancel off until a change, Cancel puts the saved values back, and leaving with changes asks first.
import {
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
  within,
} from '@testing-library/react';
import { createApiClient } from '@crsp/contract';
import { RouterProvider, createMemoryRouter } from 'react-router-dom';
import { afterEach, describe, expect, it } from 'vitest';
import { App } from '../../App';
import { FakeEventSource, repository, signedIn, type MockApi } from '../../testing/mock';

afterEach(cleanup);

const api = (): MockApi => {
  const r = repository();
  return signedIn()
    .on('GET', '/api/v1/github/repositories', { items: [r], stale: { stale: false } })
    .on('GET', '/api/v1/repositories/:id', r);
};

function renderRouted(a: MockApi) {
  FakeEventSource.reset();
  const client = createApiClient('http://controller.test', a.fetch as typeof fetch);
  const router = createMemoryRouter(
    [
      {
        path: '*',
        element: <App client={client} eventSource={(url) => new FakeEventSource(url)} />,
      },
    ],
    { initialEntries: ['/setup/pool/octo/app'] },
  );
  render(<RouterProvider router={router} />);
  return router;
}

const crumb = () =>
  within(screen.getByRole('navigation', { name: 'You are here' })).getByRole('link', {
    name: 'Repositories',
  });
const marks = () => document.querySelectorAll('[data-changed]').length;
const button = (name: string) => screen.getByRole<HTMLButtonElement>('button', { name });

/** The repository page draws its title while it loads (RES21 B18): wait for the data, not for the title. */
async function repoHeading(name: string) {
  const heading = await screen.findByRole('heading', { level: 1, name });
  await waitFor(() => expect(screen.queryByText(/Loading the repository/)).toBeNull());
  return heading;
}

describe('Repository form edit bar', () => {
  it('starts clean: no count, no marks, Save and Cancel off', async () => {
    renderRouted(api());
    await repoHeading('octo/app');
    expect(screen.queryByText(/Unsaved changes/)).toBeNull();
    expect(marks()).toBe(0);
    expect(button('Save').disabled).toBe(true);
    expect(button('Cancel').disabled).toBe(true);
  });

  it('counts changes and marks only the changed fields', async () => {
    renderRouted(api());
    const labels = await screen.findByLabelText<HTMLInputElement>('Labels');
    fireEvent.change(labels, { target: { value: 'self-hosted, big' } });
    expect(screen.getByText('Unsaved changes (1)')).toBeTruthy();
    expect(marks()).toBe(1);
    expect(labels.closest('[data-changed]')).toBeTruthy();
    expect(screen.getByLabelText(/Runner groups/).closest('[data-changed]')).toBeNull();
    fireEvent.click(screen.getByRole('checkbox', { name: /public forks/ }));
    expect(screen.getByText('Unsaved changes (2)')).toBeTruthy();
    expect(marks()).toBe(2);
    expect(button('Save').disabled).toBe(false);
    expect(button('Cancel').disabled).toBe(false);
  });

  it('Cancel puts the saved values back and stays on the page', async () => {
    const router = renderRouted(api());
    const labels = await screen.findByLabelText<HTMLInputElement>('Labels');
    const was = labels.value;
    fireEvent.change(labels, { target: { value: 'other' } });
    fireEvent.click(button('Cancel'));
    expect(labels.value).toBe(was);
    expect(marks()).toBe(0);
    expect(button('Save').disabled).toBe(true);
    expect(router.state.location.pathname).toBe('/setup/pool/octo/app');
  });

  it('asks before a link leaves with changes: Keep editing stays, Discard leaves', async () => {
    const router = renderRouted(api());
    const labels = await screen.findByLabelText<HTMLInputElement>('Labels');
    fireEvent.change(labels, { target: { value: 'other' } });
    fireEvent.click(crumb());
    expect(await screen.findByText('Discard changes?')).toBeTruthy();
    fireEvent.click(screen.getByRole('button', { name: 'Keep editing' }));
    await waitFor(() => expect(screen.queryByText('Discard changes?')).toBeNull());
    expect(router.state.location.pathname).toBe('/setup/pool/octo/app');
    expect(labels.value).toBe('other');
    fireEvent.click(crumb());
    fireEvent.click(await screen.findByRole('button', { name: 'Discard' }));
    await waitFor(() => expect(router.state.location.pathname).toBe('/setup/pool'));
  });

  it('does not ask when nothing changed', async () => {
    const router = renderRouted(api());
    await screen.findByLabelText('Labels');
    fireEvent.click(crumb());
    await waitFor(() => expect(router.state.location.pathname).toBe('/setup/pool'));
    expect(screen.queryByText('Discard changes?')).toBeNull();
  });
});
