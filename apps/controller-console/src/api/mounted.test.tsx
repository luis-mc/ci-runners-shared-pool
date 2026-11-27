// A wizard's poll stops when its page goes away: no timer is left running after the page is gone.
import { renderHook } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { useMounted } from './mounted';

describe('useMounted', () => {
  afterEach(() => vi.useRealTimers());

  it('says the page is still there when the wait ends', async () => {
    vi.useFakeTimers();
    const { result } = renderHook(() => useMounted());
    const waited = result.current.sleep(1000);
    await vi.advanceTimersByTimeAsync(1000);
    await expect(waited).resolves.toBe(true);
  });

  it('ends the wait at once when the page goes away, and leaves no timer', async () => {
    vi.useFakeTimers();
    const { result, unmount } = renderHook(() => useMounted());
    const waited = result.current.sleep(60_000);
    unmount();
    await expect(waited).resolves.toBe(false);
    expect(result.current.mounted.current).toBe(false);
    expect(vi.getTimerCount()).toBe(0);
  });
});
