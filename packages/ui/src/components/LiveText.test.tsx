// Component 84: a live region announces at most once per 10 seconds (UX-17, observability R6.13).
import { act, cleanup, render } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { LIVE_TEXT_GAP_MS, LiveText } from './LiveText';

beforeEach(() => {
  vi.useFakeTimers();
  vi.setSystemTime(new Date('2026-11-26T10:00:00Z'));
});
afterEach(() => {
  cleanup();
  vi.useRealTimers();
});

/** Every text the screen reader would be given, in order: one entry per change of the live region. */
function watch(container: HTMLElement) {
  const region = container.querySelector('[data-live-text]')!;
  const said: string[] = [];
  new MutationObserver(() => said.push(region.textContent ?? '')).observe(region, {
    childList: true,
    characterData: true,
    subtree: true,
  });
  return { region, said };
}

const tick = async (ms: number) => {
  await act(async () => {
    await vi.advanceTimersByTimeAsync(ms);
  });
};

describe('LiveText', () => {
  it('is a polite region that is read whole, and the first text is not announced', () => {
    const { container } = render(<LiveText text="2 running, 1 queued" />);
    const region = container.querySelector('[data-live-text]')!;
    expect(region.getAttribute('aria-live')).toBe('polite');
    expect(region.getAttribute('aria-atomic')).toBe('true');
    expect(region.classList.contains('ds-visually-hidden')).toBe(true);
    expect(region.textContent).toBe('2 running, 1 queued');
  });

  it('announces at most once per 10 s however fast the numbers change', async () => {
    const { container, rerender } = render(<LiveText text="0 running" />);
    const { region, said } = watch(container);
    // 60 changes, one every second: a minute of busy numbers.
    for (let s = 1; s <= 60; s++) {
      rerender(<LiveText text={`${s} running`} />);
      await tick(1000);
    }
    await tick(LIVE_TEXT_GAP_MS);
    // The first change goes out at once; every later one waits for the 10 s gap, so a minute holds at most 7.
    expect(said.length).toBeGreaterThan(0);
    expect(said.length).toBeLessThanOrEqual(7);
    expect(region.textContent).toBe('60 running');
  });

  it('holds a change until the gap has passed and then says only the newest text', async () => {
    const { container, rerender } = render(<LiveText text="a" />);
    const { region, said } = watch(container);
    rerender(<LiveText text="b" />);
    await tick(0);
    expect(region.textContent).toBe('b'); // the first change is not held
    rerender(<LiveText text="c" />);
    await tick(3000);
    rerender(<LiveText text="d" />);
    await tick(3000);
    expect(region.textContent).toBe('b'); // still inside the gap: nothing new was said
    await tick(LIVE_TEXT_GAP_MS - 6000);
    expect(region.textContent).toBe('d'); // one announcement, the newest; "c" was never said
    expect(said.filter((t) => t === 'c')).toHaveLength(0);
    expect(said).toEqual(['b', 'd']);
  });

  it('says nothing again when the text did not change', async () => {
    const { container, rerender } = render(<LiveText text="a" />);
    const { said } = watch(container);
    rerender(<LiveText text="b" />);
    await tick(0);
    rerender(<LiveText text="b" />);
    await tick(LIVE_TEXT_GAP_MS * 2);
    expect(said).toEqual(['b']);
  });
});
