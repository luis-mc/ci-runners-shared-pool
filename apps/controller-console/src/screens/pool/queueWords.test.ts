import { describe, expect, it } from 'vitest';
import { job } from '../../testing/jobsData';
import { queueGroups, queueReason, waitedWords } from './queueWords';

describe('queue words (CRSP391)', () => {
  it('a job no runner carries is No runner fits, one with room is Waiting for room', () => {
    expect(
      queueReason(job({ state: 'queued', wait_reason: 'no_runner_with_labels' })),
    ).toBe('none');
    expect(queueReason(job({ state: 'queued', wait_reason: 'free_slot' }))).toBe('room');
  });

  it('says once for the jobs that ask for the same missing labels', () => {
    const q = [1, 2, 3].map((id) =>
      job({
        id,
        state: 'queued',
        wait_reason: 'no_runner_with_labels',
        wait_text: 'pool-main',
        labels: ['self-hosted', 'pool-main'],
      }),
    );
    const g = queueGroups(q);
    expect(g).toHaveLength(1);
    expect(g[0]!.words).toBe(
      "3 jobs ask for the label pool-main, which no runner has. Check the repository's runs-on variable.",
    );
    expect(g[0]!.to).toBe('/setup/pool');
  });

  it('says how long a job waited, and days past two', () => {
    const now = Date.parse('2026-11-28T12:00:00Z');
    expect(waitedWords({ queued_at: '2026-11-28T07:50:00Z', created_at: '' }, now)).toBe(
      '4 h 10 min',
    );
    expect(waitedWords({ queued_at: '2026-11-24T10:00:00Z', created_at: '' }, now)).toBe(
      '4 d 2 h',
    );
  });
});
