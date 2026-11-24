// LOOK21 (UX-71, component 48): the layers form counts its changes, marks the changed layers, keeps Save and
// Cancel off until a change, Cancel resets, and leaving with changes asks first.
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { createApiClient } from '@crsp/contract';
import { RouterProvider, createMemoryRouter } from 'react-router-dom';
import { afterEach, describe, expect, it } from 'vitest';
import { App } from '../../App';
import { FakeEventSource, machine, signedIn } from '../../testing/mock';
import type { Schemas } from '../../api/api';

afterEach(cleanup);

const layer = (
  id: string,
  name: string,
  enabled: boolean,
): Schemas['Image']['layers'][number] => ({
  id,
  name,
  enabled,
  size_bytes: 1,
  adds: [{ name: id }],
  purpose: `for ${name}`,
  needed_by: { count: 0, repositories: [], failing: 0 },
});

const img = (): Schemas['Image'] =>
  ({
    id: 'i1',
    machine_id: 'm1',
    os: 'linux',
    base: 'ubuntu-24.04',
    base_tools: [{ name: 'git' }],
    extra_packages: [],
    layers: [
      layer('build-tools', 'Build tools', true),
      layer('browsers', 'Browsers', false),
    ],
    state: 'ready',
    built_at: '2026-01-01T00:00:00Z',
    size_bytes: 1,
  }) as unknown as Schemas['Image'];

function renderRouted() {
  FakeEventSource.reset();
  const a = signedIn()
    .on('GET', '/api/v1/machines', { items: [machine()] })
    .on('GET', '/api/v1/images', { items: [img()] })
    .on('GET', '/api/v1/images/:id', img());
  const client = createApiClient('http://controller.test', a.fetch as typeof fetch);
  const router = createMemoryRouter(
    [
      {
        path: '*',
        element: <App client={client} eventSource={(url) => new FakeEventSource(url)} />,
      },
    ],
    { initialEntries: ['/setup/images/i1'] },
  );
  render(<RouterProvider router={router} />);
  return router;
}

const marks = () => document.querySelectorAll('[data-changed]').length;
const button = (name: string) => screen.getByRole<HTMLButtonElement>('button', { name });

describe('Image layers edit bar', () => {
  it('counts changes, marks only the changed layer, enables Save and Cancel', async () => {
    renderRouted();
    await screen.findByRole('checkbox', { name: /Browsers/ });
    expect(marks()).toBe(0);
    expect(button('Save and rebuild').disabled).toBe(true);
    expect(button('Cancel').disabled).toBe(true);
    fireEvent.click(screen.getByRole('checkbox', { name: /Browsers/ }));
    expect(screen.getByText('Unsaved changes (1)')).toBeTruthy();
    expect(marks()).toBe(1);
    expect(
      screen.getByRole('checkbox', { name: /Browsers/ }).closest('[data-changed]'),
    ).toBeTruthy();
    fireEvent.click(screen.getByRole('checkbox', { name: /Build tools/ }));
    expect(screen.getByText('Unsaved changes (2)')).toBeTruthy();
    expect(marks()).toBe(2);
    expect(button('Save and rebuild').disabled).toBe(false);
    fireEvent.click(button('Cancel'));
    expect(marks()).toBe(0);
    expect(screen.queryByText(/Unsaved changes/)).toBeNull();
  });

  it('asks before a link leaves with changes', async () => {
    const router = renderRouted();
    fireEvent.click(await screen.findByRole('checkbox', { name: /Browsers/ }));
    fireEvent.click(screen.getByRole('link', { name: '‹ Images' }));
    expect(await screen.findByText('Discard changes?')).toBeTruthy();
    fireEvent.click(screen.getByRole('button', { name: 'Keep editing' }));
    await waitFor(() => expect(screen.queryByText('Discard changes?')).toBeNull());
    expect(router.state.location.pathname).toBe('/setup/images/i1');
    fireEvent.click(screen.getByRole('link', { name: '‹ Images' }));
    fireEvent.click(await screen.findByRole('button', { name: 'Discard' }));
    await waitFor(() => expect(router.state.location.pathname).toBe('/setup/images'));
  });
});
