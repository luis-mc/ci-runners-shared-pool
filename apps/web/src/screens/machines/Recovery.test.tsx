// R5.33: the machine list and page show an agent that needs recovery, and the one Recover action.
import { cleanup, fireEvent, screen, waitFor, within } from '@testing-library/react';
import { afterEach, describe, expect, it } from 'vitest';
import { apiError, machine, renderApp, signedIn, viewer } from '../../testing/mock';

afterEach(cleanup);

const needing = (over: Record<string, unknown> = {}) =>
  machine({
    recovery: {
      state: 'needs_recovery',
      reason: 'old_protocol',
      has: '0.1.0',
      needs: '0.3.0',
      since: '2026-11-14T08:00:00Z',
      armed: false,
      ...over,
    },
  });

describe('Agent recovery', () => {
  it('says needs recovery in the State column of the list', async () => {
    const api = signedIn().on('GET', '/api/v1/machines', { items: [needing()] });
    renderApp(api, '/setup/machines');
    const row = (await screen.findByRole('link', { name: 'build-1' })).closest('tr')!;
    expect(within(row).getByText('needs recovery')).toBeTruthy();
  });

  it('shows no Recovery section for a healthy machine', async () => {
    renderApp(
      signedIn().on('GET', '/api/v1/machines/:id', machine()),
      '/setup/machines/m1',
    );
    await screen.findByRole('heading', { name: 'build-1' });
    expect(screen.queryByRole('region', { name: 'Recovery' })).toBeNull();
    expect(screen.queryByRole('button', { name: 'Recover' })).toBeNull();
  });

  it('shows the message, the versions and since when', async () => {
    renderApp(
      signedIn().on('GET', '/api/v1/machines/:id', needing()),
      '/setup/machines/m1',
    );
    const section = await screen.findByRole('region', { name: 'Recovery' });
    expect(within(section).getByText(/too old to work with the controller/)).toBeTruthy();
    expect(within(section).getByText('0.1.0')).toBeTruthy();
    expect(within(section).getByText('0.3.0')).toBeTruthy();
    expect(within(section).getByText('Since')).toBeTruthy();
  });

  it('locks Recover as Recovering while the call runs, then shows it is ready and stays locked', async () => {
    let release: () => void = () => undefined;
    const held = new Promise<void>((r) => (release = r));
    const api = signedIn()
      .on('GET', '/api/v1/machines/:id', needing())
      .on('POST', '/api/v1/machines/:id/recovery', async () => {
        await held;
        return undefined;
      });
    renderApp(api, '/setup/machines/m1');
    fireEvent.click(await screen.findByRole('button', { name: 'Recover' }));
    const busy = await screen.findByRole<HTMLButtonElement>('button', {
      name: 'Recovering',
    });
    expect(busy.disabled).toBe(true);
    fireEvent.click(busy);
    release();
    await screen.findByText(/Recovery is ready/);
    expect(
      screen.getByRole<HTMLButtonElement>('button', { name: 'Recover' }).disabled,
    ).toBe(true);
    expect(api.count('POST', '/api/v1/machines/m1/recovery')).toBe(1);
  });

  it('shows ready and a locked action for a machine that is already armed', async () => {
    renderApp(
      signedIn().on('GET', '/api/v1/machines/:id', needing({ armed: true })),
      '/setup/machines/m1',
    );
    await screen.findByText(/Recovery is ready/);
    expect(
      screen.getByRole<HTMLButtonElement>('button', { name: 'Recover' }).disabled,
    ).toBe(true);
  });

  it('says it could not start when the call is refused, and Recover can be pressed again', async () => {
    const api = signedIn()
      .on('GET', '/api/v1/machines/:id', needing())
      .on('POST', '/api/v1/machines/:id/recovery', () =>
        apiError(409, 'conflict', 'This machine does not need recovery.'),
      );
    renderApp(api, '/setup/machines/m1');
    fireEvent.click(await screen.findByRole('button', { name: 'Recover' }));
    await screen.findByText(/Could not start recovery/);
    expect(screen.queryByText(/Recovery is ready/)).toBeNull();
    await waitFor(() =>
      expect(
        screen.getByRole<HTMLButtonElement>('button', { name: 'Recover' }).disabled,
      ).toBe(false),
    );
  });

  it('keeps Recover off for a viewer and says why', async () => {
    renderApp(
      signedIn(viewer).on('GET', '/api/v1/machines/:id', needing({ reason: 'stuck' })),
      '/setup/machines/m1',
    );
    await screen.findByText(/did not\./);
    expect(
      screen.getByRole<HTMLButtonElement>('button', { name: 'Recover' }).disabled,
    ).toBe(true);
  });
});
