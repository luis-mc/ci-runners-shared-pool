// CRSP244: the restarting state, the one-time reload and the route error page.
import { act, cleanup, render, screen } from '@testing-library/react';
import { createMemoryRouter, RouterProvider } from 'react-router-dom';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { ApiError } from '../api/api';
import type { Loaded } from '../api/hooks';
import {
  Load,
  RouteError,
  isStaleBuildError,
  reloadOnceForNewBuild,
  RESTART_RETRY_MS,
} from './common';

afterEach(() => {
  cleanup();
  vi.useRealTimers();
  vi.unstubAllGlobals();
  window.sessionStorage.clear();
});

const down = new ApiError('unavailable', 'Failed to fetch', 0);

describe('service restarting', () => {
  it('shows a calm note, not a Problem, over the last copy and asks again quietly', () => {
    vi.useFakeTimers();
    const reload = vi.fn();
    const failed = {
      state: 'failed',
      error: down,
      data: 'last copy',
      loadedAt: 1,
    } as unknown as Loaded<string>;
    render(
      <Load result={failed} what="the update status" reload={reload}>
        {(d) => <p>{d}</p>}
      </Load>,
    );
    expect(screen.getByText(/restarting, back in a moment/)).toBeTruthy();
    expect(screen.queryByText(/Problem/)).toBeNull();
    expect(screen.queryByText(/Failed to fetch/)).toBeNull();
    act(() => void vi.advanceTimersByTime(RESTART_RETRY_MS));
    expect(reload).toHaveBeenCalledTimes(1);
  });

  it('keeps the last copy under a note while it restarts', () => {
    const failed = {
      state: 'failed',
      error: down,
      data: 'last copy',
      loadedAt: 1,
    } as unknown as Loaded<string>;
    render(
      <Load result={failed} what="history" reload={() => undefined}>
        {(d) => <p>{d}</p>}
      </Load>,
    );
    expect(screen.getByText('last copy')).toBeTruthy();
    expect(screen.getByText(/restarting, back in a moment/)).toBeTruthy();
    expect(screen.queryByText('Problem')).toBeNull();
  });
});

describe('new build', () => {
  it('knows a stale chunk error', () => {
    expect(
      isStaleBuildError(
        new TypeError(
          'Failed to fetch dynamically imported module https://x/assets/Setup-1.js',
        ),
      ),
    ).toBe(true);
    expect(isStaleBuildError(new Error('boom'))).toBe(false);
  });

  it('reloads once, then not again within the gap', () => {
    const reload = vi.fn();
    vi.stubGlobal('location', { reload });
    expect(reloadOnceForNewBuild(1_000_000)).toBe(true);
    expect(reloadOnceForNewBuild(1_000_500)).toBe(false);
    expect(reload).toHaveBeenCalledTimes(1);
  });
});

function boundary(error: Error) {
  const router = createMemoryRouter([
    { path: '/', element: <Throws error={error} />, errorElement: <RouteError /> },
  ]);
  return render(<RouterProvider router={router} />);
}
function Throws({ error }: { error: Error }): never {
  throw error;
}

describe('route error page', () => {
  it('shows plain words and a Refresh button for a fault', () => {
    vi.spyOn(console, 'error').mockImplementation(() => undefined);
    boundary(new Error('secret stack'));
    expect(screen.getByText(/could not open/)).toBeTruthy();
    expect(screen.getByRole('button', { name: 'Refresh' })).toBeTruthy();
    expect(screen.queryByText(/Unexpected Application Error/)).toBeNull();
  });

  it('reloads once for a stale chunk, and shows the page the second time', () => {
    vi.spyOn(console, 'error').mockImplementation(() => undefined);
    const reload = vi.fn();
    vi.stubGlobal('location', { reload });
    const err = new TypeError(
      'Failed to fetch dynamically imported module /assets/Setup-DY_8ykM8.js',
    );
    boundary(err);
    expect(reload).toHaveBeenCalledTimes(1);
    cleanup();
    boundary(err);
    expect(reload).toHaveBeenCalledTimes(1);
    expect(screen.getByRole('button', { name: 'Refresh' })).toBeTruthy();
  });
});
