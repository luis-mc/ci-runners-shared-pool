// CRSP336, UX-88 to UX-90: the live rows a page builds from what it sees. A repeat adds nothing; a plan step writes
// its command when it begins and its result when it ends.
import { describe, expect, it } from 'vitest';
import { renderHook, act } from '@testing-library/react';
import { planNotes, useLiveLog } from './liveLog';

const plan = (states: string[]) =>
  ({
    id: 'p1',
    state: 'applying',
    steps: states.map((state, i) => ({
      id: `s${i}`,
      action: 'add_runner',
      text: `Step ${i + 1}`,
      needs_yes: false,
      state,
      ...(state === 'failed' ? { error: 'no space' } : {}),
    })),
  }) as never;

describe('live log', () => {
  it('drops a row that repeats the last one', () => {
    const { result } = renderHook(() => useLiveLog([{ name: 'A' }]));
    act(() => {
      result.current.say({ at: 0, pct: null, line: 'x' });
      result.current.say({ at: 0, pct: null, line: 'x' });
      result.current.say({ at: 0, pct: null, line: 'y' });
    });
    expect(result.current.events.map((e) => e.line)).toEqual(['x', 'y']);
    expect(result.current.events[0]?.phase).toBe('A');
  });

  it('writes a plan step as a command, then its result', () => {
    const seen = new Map<string, string>();
    expect(planNotes(plan(['running', 'planned']), seen)).toMatchObject([
      { at: 0, pct: null, command: 'Step 1' },
    ]);
    const next = planNotes(plan(['done', 'running']), seen);
    expect(next).toMatchObject([
      { at: 0, pct: 100, line: 'Done' },
      { at: 1, command: 'Step 2' },
    ]);
    expect(next[0]?.command).toBeUndefined();
    expect(planNotes(plan(['done', 'running']), seen)).toEqual([]);
  });

  it('shows a failed step in the error stream with its reason', () => {
    const notes = planNotes(plan(['failed']), new Map());
    expect(notes[0]).toMatchObject({
      error: true,
      line: 'Failed: no space',
      command: 'Step 1',
    });
  });
});
