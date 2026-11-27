// The one live store (R5.6, UX-19). One event stream per tab; every screen reads this store for live state.
// When the stream drops the last data stays with its age, the status says paused, and the stream reconnects
// with backoff; the first event of a new stream is a snapshot, so shown data is replaced before new events are
// trusted (R5.7).
import { useSyncExternalStore } from 'react';
import type { Schemas } from '../api/api';

export type LiveStatus = 'connecting' | 'live' | 'paused';
export type LiveEvent = Schemas['LiveEvent'];
export type Overview = Schemas['Overview'];

export interface LiveState {
  status: LiveStatus;
  /** When the last event arrived (ms), to say how old the data is (UX-53). */
  lastEventAt: number | undefined;
  overview: Overview | undefined;
  /** Counts per kind of event, so a screen reloads its list when its kind changes. */
  versions: Readonly<Record<string, number>>;
  /** Bumped on each reconnect: shown lists load again before they trust new events. */
  reconnects: number;
  /** The last plan or change progress event by id, for the activity strip (UX-34). */
  progress: Readonly<Record<string, LiveEvent>>;
}

export interface EventSourceLike {
  onopen: ((e: unknown) => void) | null;
  onerror: ((e: unknown) => void) | null;
  onmessage: ((e: { data: string; lastEventId?: string }) => void) | null;
  addEventListener(
    type: string,
    fn: (e: { data: string; lastEventId?: string }) => void,
  ): void;
  close(): void;
}

export type EventSourceFactory = (url: string) => EventSourceLike;

const EVENT_TYPES: readonly LiveEvent['type'][] = [
  'snapshot',
  'job.queued',
  'job.waiting',
  'job.started',
  'job.finished',
  'machine.load',
  'machine.state',
  'runner.state',
  'saved.today',
  'doctor.progress',
  'plan.progress',
  'change.progress',
  'image.state',
  'cache.state',
];

const KIND: Record<string, string> = {
  snapshot: 'snapshot',
  'job.queued': 'jobs',
  'job.waiting': 'jobs',
  'job.started': 'jobs',
  'job.finished': 'jobs',
  'machine.load': 'load',
  'machine.state': 'machines',
  'runner.state': 'runners',
  'saved.today': 'saved',
  'doctor.progress': 'doctor',
  'plan.progress': 'plans',
  'change.progress': 'changes',
  'image.state': 'images',
  'cache.state': 'caches',
};

export class LiveStore {
  private state: LiveState = {
    status: 'connecting',
    lastEventAt: undefined,
    overview: undefined,
    versions: {},
    reconnects: 0,
    progress: {},
  };
  private listeners = new Set<() => void>();
  private source: EventSourceLike | undefined;
  private timer: ReturnType<typeof setTimeout> | undefined;
  private attempt = 0;
  private stopped = true;

  constructor(
    private readonly factory: EventSourceFactory,
    private readonly url = '/api/v1/events',
    private readonly backoffMs: readonly number[] = [
      1000, 2000, 4000, 8000, 16000, 30000,
    ],
    private readonly now: () => number = Date.now,
  ) {}

  getState = (): LiveState => this.state;

  subscribe = (fn: () => void): (() => void) => {
    this.listeners.add(fn);
    return () => this.listeners.delete(fn);
  };

  private set(patch: Partial<LiveState>): void {
    this.state = { ...this.state, ...patch };
    for (const l of this.listeners) l();
  }

  start(): void {
    if (!this.stopped) return;
    this.stopped = false;
    this.connect();
  }

  stop(): void {
    this.stopped = true;
    if (this.timer) clearTimeout(this.timer);
    this.timer = undefined;
    this.source?.close();
    this.source = undefined;
  }

  private connect(): void {
    this.set({ status: this.state.lastEventAt ? 'paused' : 'connecting' });
    const src = this.factory(this.url);
    this.source = src;
    src.onopen = () => {
      this.attempt = 0;
    };
    src.onerror = () => {
      src.close();
      if (this.source === src) this.source = undefined;
      if (this.stopped) return;
      this.set({ status: 'paused' });
      const wait =
        this.backoffMs[Math.min(this.attempt, this.backoffMs.length - 1)] ?? 30000;
      this.attempt += 1;
      this.timer = setTimeout(() => {
        if (this.stopped) return;
        this.set({ reconnects: this.state.reconnects + 1 });
        this.connect();
      }, wait);
    };
    const onData = (e: { data: string }) => {
      try {
        this.apply(JSON.parse(e.data) as LiveEvent);
      } catch {
        // A line that is not an event is ignored.
      }
    };
    src.onmessage = onData;
    for (const t of EVENT_TYPES) src.addEventListener(t, onData);
  }

  /** A first load of the overview, used until the stream's snapshot arrives. */
  seed(o: Overview): void {
    if (!this.state.overview) this.set({ overview: o });
  }

  /**
   * Keep the strip in step with a cache the screen has just read or wiped (UX-19, UX-34): a wiping cache is
   * running work, a ready one is not. It changes no version, so no list reloads because of it.
   */
  trackCache(cache: Schemas['Cache']): void {
    const key = `cache:${cache.id}`;
    const progress = { ...this.state.progress };
    putCache(progress, { id: 'local', type: 'cache.state', at: '', cache });
    const before = JSON.stringify(this.state.progress[key]?.cache);
    if (JSON.stringify(progress[key]?.cache) === before) return;
    this.set({ progress });
  }

  /** Apply one event to the store. */
  apply(ev: LiveEvent): void {
    const kind = KIND[ev.type] ?? ev.type;
    const versions = {
      ...this.state.versions,
      [kind]: (this.state.versions[kind] ?? 0) + 1,
    };
    let overview = this.state.overview;
    const progress = { ...this.state.progress };
    if (ev.type === 'snapshot' && ev.snapshot) {
      overview = ev.snapshot;
    } else if (overview) {
      overview = reduceOverview(overview, ev);
    }
    if (ev.type === 'plan.progress' && ev.plan) progress[`plan:${ev.plan.id}`] = ev;
    if (ev.type === 'change.progress' && ev.change)
      progress[`change:${ev.change.id}`] = ev;
    if (ev.type === 'cache.state' && ev.cache) putCache(progress, ev);
    this.set({ status: 'live', lastEventAt: this.now(), overview, versions, progress });
  }
}

function putCache(progress: Record<string, LiveEvent>, ev: LiveEvent): void {
  const c = ev.cache;
  if (!c) return;
  if (c.state === 'wiping') progress[`cache:${c.id}`] = ev;
  else delete progress[`cache:${c.id}`];
}

function reduceOverview(o: Overview, ev: LiveEvent): Overview {
  switch (ev.type) {
    case 'machine.state':
      if (!ev.machine) return o;
      return {
        ...o,
        machines: o.machines.some((m) => m.id === ev.machine?.id)
          ? o.machines.map((m) => (m.id === ev.machine?.id ? ev.machine : m))
          : [...o.machines, ev.machine],
      };
    case 'machine.load':
      if (!ev.load) return o;
      return {
        ...o,
        machines: o.machines.map((m) =>
          m.id === ev.load?.machine_id
            ? {
                ...m,
                load: ev.load.load,
                slots_used: ev.load.slots_used ?? m.slots_used,
              }
            : m,
        ),
      };
    case 'job.queued':
    case 'job.waiting':
      if (!ev.job) return o;
      return {
        ...o,
        queued: [...(o.queued ?? []).filter((j) => j.id !== ev.job?.id), ev.job],
        // The server's words for this job are out of date now; the screen falls back to the job's own reason.
        waiting: (o.waiting ?? []).filter((w) => w.job_id !== ev.job?.id),
        queued_count: Math.max(
          o.queued_count,
          (o.queued ?? []).filter((j) => j.id !== ev.job?.id).length + 1,
        ),
      };
    case 'job.started':
      if (!ev.job) return o;
      return {
        ...o,
        queued: (o.queued ?? []).filter((j) => j.id !== ev.job?.id),
        waiting: (o.waiting ?? []).filter((w) => w.job_id !== ev.job?.id),
        queued_count: Math.max(
          0,
          o.queued_count - ((o.queued ?? []).some((j) => j.id === ev.job?.id) ? 1 : 0),
        ),
        running: [...o.running.filter((j) => j.id !== ev.job?.id), ev.job],
      };
    case 'job.finished':
      if (!ev.job) return o;
      return { ...o, running: o.running.filter((j) => j.id !== ev.job?.id) };
    default:
      return o;
  }
}

export function useLive(store: LiveStore): LiveState {
  return useSyncExternalStore(store.subscribe, store.getState, store.getState);
}
