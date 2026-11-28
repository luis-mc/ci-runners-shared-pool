import { act, cleanup, render, screen } from '@testing-library/react';
import { Fragment, createElement } from 'react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { FakeEventSource, machine, overview } from '../testing/mock';
import { LiveProvider, useLiveState } from './LiveProvider';
import { LiveStore } from './store';

beforeEach(() => {
  vi.useFakeTimers();
  FakeEventSource.reset();
});
afterEach(() => vi.useRealTimers());

function started() {
  const store = new LiveStore(
    (u) => new FakeEventSource(u),
    '/api/v1/events',
    [1000, 2000, 4000],
    () => Date.now(),
  );
  store.start();
  return store;
}

describe('live store (R5.6)', () => {
  it('opens one stream and keeps what a snapshot brings', () => {
    const store = started();
    expect(FakeEventSource.instances).toHaveLength(1);
    expect(store.getState().status).toBe('connecting');
    FakeEventSource.latest().emit({
      id: '1',
      type: 'snapshot',
      at: '2026-11-14T10:00:00Z',
      snapshot: overview(),
    });
    expect(store.getState().status).toBe('live');
    expect(store.getState().overview?.machines[0]?.name).toBe('build-1');
  });

  it('applies machine, load and job events to the overview', () => {
    const store = started();
    const es = FakeEventSource.latest();
    es.emit({ id: '1', type: 'snapshot', at: 'x', snapshot: overview() });
    es.emit({
      id: '2',
      type: 'machine.state',
      at: 'x',
      machine: machine({ id: 'm2', name: 'build-2' }),
    });
    expect(store.getState().overview?.machines.map((m) => m.name)).toEqual([
      'build-1',
      'build-2',
    ]);
    es.emit({
      id: '3',
      type: 'machine.load',
      at: 'x',
      load: {
        machine_id: 'm1',
        load: {
          memory_pct: 77,
          memory_known: true,
          cpu_known: false,
          sampled_at: 'x',
          stale: false,
        },
        slots_used: 3,
      },
    });
    const m1 = store.getState().overview?.machines.find((m) => m.id === 'm1');
    expect(m1?.load?.memory_pct).toBe(77);
    expect(m1?.slots_used).toBe(3);
    const job = {
      id: 5,
      repository: 'octo/app',
      run_id: 1,
      run_attempt: 1,
      github_job_id: 1,
      job_name: 'test',
      labels: [],
      state: 'queued' as const,
    };
    es.emit({ id: '4', type: 'job.queued', at: 'x', job });
    expect(store.getState().overview?.queued_count).toBe(1);
    es.emit({ id: '5', type: 'job.started', at: 'x', job: { ...job, state: 'running' } });
    expect(store.getState().overview?.running).toHaveLength(1);
    expect(store.getState().overview?.queued_count).toBe(0);
    es.emit({ id: '6', type: 'job.finished', at: 'x', job: { ...job, state: 'done' } });
    expect(store.getState().overview?.running).toHaveLength(0);
  });

  it('counts events per kind so a screen can reload its list', () => {
    const store = started();
    FakeEventSource.latest().emit({ id: '1', type: 'runner.state', at: 'x' });
    FakeEventSource.latest().emit({ id: '2', type: 'runner.state', at: 'x' });
    expect(store.getState().versions.runners).toBe(2);
  });
});

describe('live store job bursts (CRSP290, audit F6)', () => {
  const job = (id: string) => ({
    id,
    repository: 'o/r',
    name: 'build',
    labels: [],
    state: 'queued' as const,
  });

  it('moves the jobs version once for a burst, and keeps the overview current at once', () => {
    const store = started();
    const es = FakeEventSource.latest();
    es.emit({ id: '1', type: 'snapshot', at: 'x', snapshot: overview() });
    for (let i = 0; i < 60; i++)
      es.emit({ id: `q${i}`, type: 'job.queued', at: 'x', job: job(`j${i}`) as never });
    expect(store.getState().overview?.queued_count).toBe(60);
    expect(store.getState().versions.jobs ?? 0).toBe(0);
    vi.advanceTimersByTime(399);
    expect(store.getState().versions.jobs ?? 0).toBe(0);
    vi.advanceTimersByTime(1);
    expect(store.getState().versions.jobs).toBe(1);
    es.emit({ id: 'f', type: 'job.finished', at: 'x', job: job('j1') as never });
    vi.advanceTimersByTime(400);
    expect(store.getState().versions.jobs).toBe(2);
  });

  it('does not wait for kinds that do not come in bursts', () => {
    const store = started();
    FakeEventSource.latest().emit({
      id: '1',
      type: 'machine.state',
      at: 'x',
      machine: machine(),
    });
    expect(store.getState().versions.machines).toBe(1);
  });
});

describe('live store reconnect (R5.7)', () => {
  it('keeps the last data, says paused, and reconnects with backoff', () => {
    const store = started();
    FakeEventSource.latest().emit({
      id: '1',
      type: 'snapshot',
      at: 'x',
      snapshot: overview(),
    });
    FakeEventSource.latest().fail();
    expect(store.getState().status).toBe('paused');
    expect(store.getState().overview?.machines).toHaveLength(1);
    expect(store.getState().lastEventAt).toBeDefined();

    vi.advanceTimersByTime(999);
    expect(FakeEventSource.instances).toHaveLength(1);
    vi.advanceTimersByTime(1);
    expect(FakeEventSource.instances).toHaveLength(2);
    expect(store.getState().reconnects).toBe(1);

    FakeEventSource.latest().fail();
    vi.advanceTimersByTime(1999);
    expect(FakeEventSource.instances).toHaveLength(2);
    vi.advanceTimersByTime(1);
    expect(FakeEventSource.instances).toHaveLength(3);
  });

  it('replaces shown data with the snapshot that opens the new stream, then goes live', () => {
    const store = started();
    FakeEventSource.latest().emit({
      id: '1',
      type: 'snapshot',
      at: 'x',
      snapshot: overview(),
    });
    FakeEventSource.latest().fail();
    vi.advanceTimersByTime(1000);
    FakeEventSource.latest().emit({
      id: '9',
      type: 'snapshot',
      at: 'x',
      replay_gap: true,
      snapshot: overview({ machines: [machine({ name: 'fresh' })] }),
    });
    expect(store.getState().status).toBe('live');
    expect(store.getState().overview?.machines[0]?.name).toBe('fresh');
  });

  it('stops for good when the console leaves', () => {
    const store = started();
    FakeEventSource.latest().fail();
    store.stop();
    vi.advanceTimersByTime(60000);
    expect(FakeEventSource.instances).toHaveLength(1);
  });

  // R5.4 (UX-19, UX-34): the Overview and the running-jobs strip read the one store, so they never disagree.
  it('overview and strip read one store', () => {
    const store = started();
    function Overview() {
      const s = useLiveState();
      return createElement(
        'span',
        { 'data-testid': 'overview' },
        s.overview?.machines.map((m) => m.name).join(','),
      );
    }
    function Strip() {
      const s = useLiveState();
      return createElement(
        'span',
        { 'data-testid': 'strip' },
        s.overview?.machines.map((m) => m.name).join(','),
      );
    }
    render(
      createElement(LiveProvider, {
        store,
        children: createElement(
          Fragment,
          null,
          createElement(Overview),
          createElement(Strip),
        ),
      }),
    );
    // The provider starts the same store the test holds: still one stream, none per screen.
    expect(FakeEventSource.instances).toHaveLength(1);
    act(() => {
      FakeEventSource.latest().emit({
        id: '1',
        type: 'snapshot',
        at: 'x',
        snapshot: overview(),
      });
    });
    expect(screen.getByTestId('overview').textContent).toBe('build-1');
    expect(screen.getByTestId('strip').textContent).toBe('build-1');
    act(() => {
      FakeEventSource.latest().emit({
        id: '2',
        type: 'machine.state',
        at: 'x',
        machine: machine({ id: 'm2', name: 'build-2' }),
      });
    });
    // One event moves both, and no second stream was opened for either.
    expect(screen.getByTestId('overview').textContent).toBe('build-1,build-2');
    expect(screen.getByTestId('strip').textContent).toBe('build-1,build-2');
    expect(FakeEventSource.instances).toHaveLength(1);
    cleanup();
  });
});
