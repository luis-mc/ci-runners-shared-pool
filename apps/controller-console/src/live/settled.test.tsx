import { act, cleanup, render, screen } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { FakeEventSource, overview } from '../testing/mock';
import { LiveProvider, SETTLE_MS, useSettledVersion } from './LiveProvider';
import { LiveStore } from './store';

beforeEach(() => {
  vi.useFakeTimers();
  FakeEventSource.reset();
});
afterEach(() => {
  cleanup();
  vi.useRealTimers();
});

const job = (id: number, state: 'queued' | 'running' | 'done') => ({
  id,
  repository: 'octo/app',
  run_id: 100 + id,
  run_attempt: 1,
  github_job_id: id,
  job_name: 'test',
  labels: [],
  state,
});

function Probe({ onLoad }: { onLoad: () => void }) {
  const v = useSettledVersion('jobs');
  onLoad();
  return <p>v{v}</p>;
}

function setup() {
  const store = new LiveStore((u) => new FakeEventSource(u));
  const loads = vi.fn();
  render(
    <LiveProvider store={store}>
      <Probe onLoad={loads} />
    </LiveProvider>,
  );
  const es = FakeEventSource.latest();
  act(() => es.emit({ id: '1', type: 'snapshot', at: 'x', snapshot: overview() }));
  return { es, loads, store };
}

describe('open job lists follow job events (observability R5.4)', () => {
  it('job.queued, job.started and job.finished each count as a jobs change', () => {
    const { es, store } = setup();
    act(() => es.emit({ id: '2', type: 'job.queued', at: 'x', job: job(5, 'queued') }));
    act(() => es.emit({ id: '3', type: 'job.started', at: 'x', job: job(5, 'running') }));
    act(() => es.emit({ id: '4', type: 'job.finished', at: 'x', job: job(5, 'done') }));
    expect(store.getState().versions.jobs).toBe(3);
  });

  it('a burst of events moves a list once, after the settle time', () => {
    const { es } = setup();
    expect(screen.getByText('v0')).toBeTruthy();
    act(() => {
      for (let i = 0; i < 40; i++)
        es.emit({
          id: String(10 + i),
          type: 'job.queued',
          at: 'x',
          job: job(i, 'queued'),
        });
    });
    expect(screen.getByText('v0')).toBeTruthy(); // not yet: the burst is being gathered
    act(() => {
      vi.advanceTimersByTime(SETTLE_MS + 1);
    });
    expect(screen.getByText('v40')).toBeTruthy(); // one move to the last version
  });

  it('an event after a settle gets its own reload', () => {
    const { es } = setup();
    act(() => es.emit({ id: '2', type: 'job.queued', at: 'x', job: job(1, 'queued') }));
    act(() => {
      vi.advanceTimersByTime(SETTLE_MS + 1);
    });
    expect(screen.getByText('v1')).toBeTruthy();
    act(() => es.emit({ id: '3', type: 'job.finished', at: 'x', job: job(1, 'done') }));
    act(() => {
      vi.advanceTimersByTime(SETTLE_MS + 1);
    });
    expect(screen.getByText('v2')).toBeTruthy();
  });

  it('events of other kinds do not move a job list', () => {
    const { es } = setup();
    act(() => es.emit({ id: '2', type: 'runner.state', at: 'x' }));
    act(() => {
      vi.advanceTimersByTime(SETTLE_MS * 4);
    });
    expect(screen.getByText('v0')).toBeTruthy();
  });

  it('a finished job leaves the queued strip even when started was missed', () => {
    const { es, store } = setup();
    act(() => es.emit({ id: '2', type: 'job.queued', at: 'x', job: job(9, 'queued') }));
    expect(store.getState().overview?.queued_count).toBe(1);
    act(() => es.emit({ id: '3', type: 'job.finished', at: 'x', job: job(9, 'done') }));
    expect(store.getState().overview?.queued_count).toBe(0);
    expect(store.getState().overview?.queued ?? []).toHaveLength(0);
  });
});
