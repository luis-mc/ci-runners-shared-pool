import {
  createContext,
  useContext,
  useEffect,
  useMemo,
  useRef,
  useState,
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

/** A number that changes when events of one kind arrive: use it as a reload key (never as a data source). */
export function useVersion(kind: string): number {
  const s = useLiveState();
  return (s.versions[kind] ?? 0) + s.reconnects * 1000;
}

/** How long a list waits to gather a burst of job events into one reload. */
export const SETTLE_MS = 500;

/**
 * Like useVersion, but a burst of events moves it once per `ms`, not once per event: with dozens of builds
 * running a list reloads a few times a second at most, and the last event of a burst always gets its reload.
 */
export function useSettledVersion(kind: string, ms = SETTLE_MS): number {
  const latest = useVersion(kind);
  const [settled, setSettled] = useState(latest);
  const seen = useRef(latest);
  const timer = useRef<ReturnType<typeof setTimeout> | undefined>(undefined);
  seen.current = latest;
  useEffect(() => {
    if (latest === settled || timer.current) return;
    timer.current = setTimeout(() => {
      timer.current = undefined;
      setSettled(seen.current);
    }, ms);
  }, [latest, settled, ms]);
  useEffect(
    () => () => {
      if (timer.current) clearTimeout(timer.current);
      timer.current = undefined;
    },
    [],
  );
  return settled;
}
