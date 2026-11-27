import { createContext, useContext, useEffect, useMemo, type ReactNode } from 'react';
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
