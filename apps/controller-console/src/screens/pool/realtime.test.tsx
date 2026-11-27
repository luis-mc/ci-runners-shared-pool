// Pool > Live is realtime (LOOK56, R5.150): lists reload on stream events, and ask again every 5 seconds only while
// the stream is down; the Pool section's timers count every second.
import { ClockProvider, useNow } from '@crsp/ui';
import { act, cleanup, renderHook } from '@testing-library/react';
import type { ReactNode } from 'react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { LiveProvider } from '../../live/LiveProvider';
import { LiveStore } from '../../live/store';
import { FakeEventSource } from '../../testing/mock';
import { useTick } from '../jobs/api';

beforeEach(() => {
  vi.useFakeTimers();
  FakeEventSource.reset();
});
afterEach(() => {
  cleanup();
  vi.useRealTimers();
});

const snapshot = {
  id: '1',
  type: 'snapshot' as const,
  at: '2026-11-22T10:00:00Z',
  snapshot: { machines: [], queued_count: 0, running: [], last_event_id: '1' },
};

function withStore(store: LiveStore) {
  return ({ children }: { children: ReactNode }) => (
    <LiveProvider store={store}>{children}</LiveProvider>
  );
}

describe('the stream and the 5 s poll', () => {
  it('stays still while the stream is live', () => {
    const store = new LiveStore((url) => new FakeEventSource(url));
    const { result } = renderHook(() => useTick(), { wrapper: withStore(store) });
    act(() => FakeEventSource.latest().emit(snapshot));
    act(() => void vi.advanceTimersByTime(12_000));
    expect(result.current).toBe(0);
  });

  it('polls every 5 s once the stream drops, and stops when it is back', () => {
    const store = new LiveStore(
      (url) => new FakeEventSource(url),
      '/api/v1/events',
      [1000],
    );
    const { result } = renderHook(() => useTick(), { wrapper: withStore(store) });
    act(() => FakeEventSource.latest().emit(snapshot));
    act(() => FakeEventSource.latest().fail());
    expect(store.getState().status).toBe('paused');
    act(() => void vi.advanceTimersByTime(999));
    const before = result.current;
    act(() => void vi.advanceTimersByTime(5_000));
    expect(result.current).toBeGreaterThan(before);
    act(() => FakeEventSource.latest().emit(snapshot));
    const at = result.current;
    act(() => void vi.advanceTimersByTime(12_000));
    expect(result.current).toBe(at);
  });
});

describe('the clock of the Pool section', () => {
  it('moves every second', () => {
    const { result } = renderHook(() => useNow(), {
      wrapper: ({ children }) => <ClockProvider tickMs={1000}>{children}</ClockProvider>,
    });
    const first = result.current;
    act(() => void vi.advanceTimersByTime(3_000));
    expect(result.current - first).toBeGreaterThanOrEqual(3_000);
  });
});
