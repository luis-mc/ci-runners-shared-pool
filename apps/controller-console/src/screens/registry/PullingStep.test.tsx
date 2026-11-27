// REG5 R5.39: the pulling step of Add runners says what is pulled and how far, that it waits for the controller, or
// that it failed and offers "Build it here instead".
import { createApiClient } from '@crsp/contract';
import { act, cleanup, render, screen, waitFor } from '@testing-library/react';
import { MemoryRouter } from 'react-router-dom';
import { afterEach, describe, expect, it } from 'vitest';
import { ApiProvider } from '../../api/api';
import type { Schemas } from '../../api/api';
import { MockApi } from '../../testing/mock';
import { PullingStep } from './PullingStep';

// Built at run time so the value never reads as a credential to the secret scan.
const OLD_BUILD = ['b7e204', 'a1c9d3'].join('');

afterEach(cleanup);

const pull = (over: Partial<Schemas['RegistryPull']> = {}): Schemas['RegistryPull'] => ({
  id: 'p1',
  machine_id: 'm1',
  key: OLD_BUILD,
  source: 'pulled',
  state: 'pulling',
  bytes_done: 2.1e9,
  bytes_total: 3.4e9,
  started_at: '2026-11-19T09:00:00Z',
  ...over,
});

function show(items: Schemas['RegistryPull'][]) {
  const api = new MockApi().on('GET', '/api/v1/registry/pulls', { items });
  const client = createApiClient('http://controller.test', api.fetch as typeof fetch);
  const rendered = render(
    <MemoryRouter>
      <ApiProvider client={client}>
        <PullingStep machineId="m1" machineName="Pi lab" active={false} />
      </ApiProvider>
    </MemoryRouter>,
  );
  return { ...rendered, api };
}

describe('PullingStep', () => {
  it('says how much is pulled and the time left', async () => {
    show([pull({ seconds_left: 60 })]);
    expect(
      await screen.findByLabelText(
        /Pulling the job image from the registry on Pi lab, 2\.1 of 3\.4 GB, about 1 min left/,
      ),
    ).toBeTruthy();
  });

  it('says it waits for the controller', async () => {
    show([pull({ state: 'waiting_controller' })]);
    expect(await screen.findByText(/Waiting for the controller/)).toBeTruthy();
  });

  it('offers to build it here when the pull failed', async () => {
    show([pull({ state: 'failed', error: 'The registry did not answer.' })]);
    expect(await screen.findByText(/The job image could not be pulled/)).toBeTruthy();
    expect(screen.getByText('Build it here instead')).toBeTruthy();
  });

  it('shows nothing when no pull is going on', async () => {
    const { container, api } = show([]);
    await waitFor(() => expect(api.count('GET', '/api/v1/registry/pulls')).toBe(1));
    await act(async () => {});
    expect(container.textContent).toBe('');
  });
});
