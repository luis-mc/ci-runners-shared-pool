// Machines R5.40 to R5.42: remove a machine from the pool. The first press changes nothing and asks what goes;
// the yes sends confirm=true. The controller's own machine has no such button.
import { cleanup, fireEvent, screen, waitFor } from '@testing-library/react';
import { afterEach, describe, expect, it } from 'vitest';
import { apiError, machine, renderApp, signedIn } from '../../testing/mock';

afterEach(cleanup);

const deletes = (api: ReturnType<typeof signedIn>) =>
  api.calls.filter((c) => c.method === 'DELETE' && c.path === '/api/v1/machines/m1');

describe('Remove from the pool', () => {
  it('asks what goes first and removes only after the yes', async () => {
    const api = signedIn()
      .on('GET', '/api/v1/machines/:id', machine({ is_controller: false }))
      .on('GET', '/api/v1/machines', { items: [machine({ is_controller: false })] })
      .on('DELETE', '/api/v1/machines/:id', (call) =>
        (call.query as URLSearchParams).get('confirm') === 'true'
          ? ({ id: 'ch1', machine_id: 'm1', kind: 'remove', state: 'waiting', waiting_for: [] } as never)
          : apiError(409, 'needs_confirmation', 'removing build-1 drains it first', [
              { kind: 'item', text: 'the machine build-1' },
              { kind: 'item', text: 'runner crsp-a (deregistered from GitHub)' },
              { kind: 'item', text: 'its key (revoked at once)' },
            ]),
      );
    renderApp(api, '/setup/machines/m1');
    fireEvent.click(await screen.findByRole('button', { name: 'Remove from the pool' }));
    expect(await screen.findByText('runner crsp-a (deregistered from GitHub)')).toBeTruthy();
    expect(deletes(api).length).toBe(1); // the look changed nothing
    fireEvent.click(screen.getByRole('button', { name: 'Remove' }));
    await waitFor(() => expect(deletes(api).length).toBe(2));
    expect((deletes(api)[1]?.query as URLSearchParams).get('confirm')).toBe('true');
  });

  it('is not offered on the controller machine', async () => {
    const api = signedIn()
      .on('GET', '/api/v1/machines/:id', machine({ is_controller: true }))
      .on('GET', '/api/v1/machines', { items: [machine({ is_controller: true })] });
    renderApp(api, '/setup/machines/m1');
    await screen.findByText(/Settings|Pool size|Policy/);
    expect(screen.queryByRole('button', { name: 'Remove from the pool' })).toBeNull();
  });
});
