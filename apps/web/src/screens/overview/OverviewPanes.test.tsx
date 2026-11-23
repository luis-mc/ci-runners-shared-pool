// LOOK61: the words and numbers of the Overview parts.
import { describe, expect, it } from 'vitest';
import { machine } from '../../testing/mock';
import { jobNames, labelsInUse, sizeWords, startWord } from './OverviewPanes';

const job = (over: Record<string, unknown>) =>
  ({
    id: 1,
    repository: 'o/a',
    run_id: 1,
    run_attempt: 1,
    github_job_id: 1,
    job_name: 'unit',
    labels: [],
    state: 'running',
    ...over,
  }) as Parameters<typeof jobNames>[1][number];

describe('Overview parts (LOOK61)', () => {
  it('writes the pool size of a machine, and nothing without its facts', () => {
    expect(
      sizeWords(machine({ facts: { threads: 6, memory_bytes: 9 * 1024 ** 3 } })),
    ).toBe('6 CPU / 9 GiB');
    expect(sizeWords(machine())).toBeUndefined();
  });

  it('names the jobs on a machine from the running list when the machine has none', () => {
    const m = machine({ id: 'm1' });
    expect(
      jobNames(m, [job({ machine_id: 'm1' }), job({ machine_id: 'm2', job_name: 'x' })]),
    ).toEqual(['unit']);
  });

  it('lists each routing label once, sorted', () => {
    expect(labelsInUse([{ labels: ['b', 'a'] }, { labels: ['a'] }])).toEqual(['a', 'b']);
  });

  it('gives a waiting job one word for why', () => {
    expect(startWord({ wait_reason: 'no_runner_with_labels' }).word).toBe(
      'No runner fits',
    );
    expect(startWord({ wait_reason: 'free_slot' }).word).toBe('Waiting for room');
    expect(startWord({}).word).toBe('Waiting');
  });
});
