// R5.6 (UX-53): "x ago" reads one shared clock that pauses while the page is hidden and catches up when shown.
// The clock is the shared UI package's ClockProvider and useNow; the console has no clock of its own.
import { ClockProvider, formatAgo, useNow } from '@crsp/ui';
import { act, cleanup, render, screen } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

const START = new Date(2026, 10, 26, 10, 0, 0).getTime();

function Age({ since }: { since: number }) {
  const now = useNow();
  return <span data-testid="age">{formatAgo(since, now)}</span>;
}

function setVisibility(state: 'visible' | 'hidden') {
  Object.defineProperty(document, 'visibilityState', {
    value: state,
    configurable: true,
  });
  document.dispatchEvent(new Event('visibilitychange'));
}

beforeEach(() => {
  vi.useFakeTimers();
  vi.setSystemTime(START);
  setVisibility('visible');
});
afterEach(() => {
  cleanup();
  vi.useRealTimers();
});

describe('shared clock (R5.6, UX-53)', () => {
  it('ages tick and pause while hidden', () => {
    render(
      <ClockProvider tickMs={30_000}>
        <Age since={START} />
        <Age since={START} />
      </ClockProvider>,
    );
    const ages = () => screen.getAllByTestId('age').map((e) => e.textContent);
    expect(ages()).toEqual(['just now', 'just now']);

    // Visible: the ages tick together off one clock.
    act(() => {
      vi.advanceTimersByTime(3 * 60_000);
    });
    expect(ages()).toEqual(['3 min ago', '3 min ago']);

    // Hidden: no tick, the age holds.
    act(() => setVisibility('hidden'));
    act(() => {
      vi.advanceTimersByTime(10 * 60_000);
    });
    expect(ages()).toEqual(['3 min ago', '3 min ago']);

    // Shown again: it catches up at once, to the real time.
    act(() => setVisibility('visible'));
    expect(ages()).toEqual(['13 min ago', '13 min ago']);
  });
});
