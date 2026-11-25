import { describe, expect, it } from 'vitest';
import { overallPct, phasesFromEvents, type ProgressEvent } from './progress';

const m = { name: 'ctl', role: 'controller', os: 'Ubuntu 24.04' } as const;
const ev = (o: Partial<ProgressEvent>): ProgressEvent => ({
  jobId: 'j', phase: 'p', phaseIndex: 0, phaseCount: 2, phaseWeight: 1, pct: 0,
  machine: m, stream: 'stdout', ts: 1, ...o,
});

describe('overallPct', () => {
  it('weights phases', () => {
    expect(overallPct([{ name: 'a', weight: 3, pct: 100 }, { name: 'b', weight: 1, pct: 50 }], 1)).toBe(87.5);
  });
  it('is null when the current phase reports no percent', () => {
    expect(overallPct([{ name: 'a', weight: 1, pct: 100 }, { name: 'b', weight: 1, pct: null }], 1)).toBeNull();
  });
});

describe('phasesFromEvents', () => {
  it('folds events', () => {
    const p = phasesFromEvents([ev({ pct: 40 }), ev({ phaseIndex: 1, phase: 'q', pct: null, reason: 'why' })]);
    expect(p).toHaveLength(2);
    expect(p[1]).toMatchObject({ name: 'q', pct: null, reason: 'why' });
  });
});
