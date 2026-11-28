import {
  createContext,
  useContext,
  useEffect,
  useMemo,
  useRef,
  useSyncExternalStore,
  type ReactNode,
} from 'react';
import { LiveStore, useLive, type EventSourceFactory, type LiveState } from './store';

const LiveContext = createContext<LiveStore | null>(null);

const browserFactory: EventSourceFactory = (url) =>
  new EventSource(url) as unknown as ReturnType<EventSourceFactory>;

/** Starts the one stream while a user is signed in. */
export function LiveProvider({
  children,
  factory = browserFactory,
  store: given,
}: {
  children: ReactNode;
  factory?: EventSourceFactory;
  store?: LiveStore;
}) {
  const store = useMemo(() => given ?? new LiveStore(factory), [given, factory]);
  useEffect(() => {
    store.start();
    return () => store.stop();
  }, [store]);
  return <LiveContext.Provider value={store}>{children}</LiveContext.Provider>;
}

// Outside a signed-in page (the setup page) there is no stream: screens read an empty store.
const IDLE = new LiveStore(() => {
  throw new Error('The idle store never connects.');
});

export function useLiveStore(): LiveStore {
  return useContext(LiveContext) ?? IDLE;
}

export function useLiveState(): LiveState {
  return useLive(useLiveStore());
}

/**
 * Reads one value out of the live state and re-renders only when that value changes (shallow compare), so a screen
 * that needs the status or a version does not render again for every machine load event (CRSP290, audit F6).
 */
export function useLiveSelect<T>(select: (s: LiveState) => T): T {
  const store = useLiveStore();
  const last = useRef<{ state: LiveState; value: T } | undefined>(undefined);
  const snapshot = (): T => {
    const state = store.getState();
    if (last.current?.state === state) return last.current.value;
    const value = select(state);
    if (last.current && shallowEqual(last.current.value, value)) {
      last.current = { state, value: last.current.value };
    } else {
      last.current = { state, value };
    }
    return last.current.value;
  };
  return useSyncExternalStore(store.subscribe, snapshot, snapshot);
}

function shallowEqual(a: unknown, b: unknown): boolean {
  if (Object.is(a, b)) return true;
  if (typeof a !== 'object' || typeof b !== 'object' || !a || !b) return false;
  const ka = Object.keys(a);
  const kb = Object.keys(b);
  return (
    ka.length === kb.length &&
    ka.every((k) =>
      Object.is((a as Record<string, unknown>)[k], (b as Record<string, unknown>)[k]),
    )
  );
}

/** A number that changes when events of one kind arrive: use it as a reload key (never as a data source). */
export function useVersion(kind: string): number {
  return useLiveSelect((s) => (s.versions[kind] ?? 0) + s.reconnects * 1000);
}
