// The one machine list (UX-19): the header, the Overview and the Machines page read it through this hook.
// It reloads when a machine event arrives, on a short timer, and when the tab is shown again, so a machine
// that joined, or a controller whose own PC came up, never leaves a screen saying "no machines".
import { useEffect, useRef, useState } from 'react';
import { unwrap, useClient, type Schemas } from './api';
import { useLoad, type Loaded } from './hooks';
import { useVersion } from '../live/LiveProvider';

export const REFRESH_MS = 15_000;

/** A number that goes up on a timer and when the tab comes back: use it as a reload key. */
export function useRefreshTick(ms: number = REFRESH_MS): number {
  const [tick, setTick] = useState(0);
  useEffect(() => {
    const bump = () => setTick((t) => t + 1);
    const timer = setInterval(bump, ms);
    const shown = () => {
      if (document.visibilityState === 'visible') bump();
    };
    document.addEventListener('visibilitychange', shown);
    window.addEventListener('focus', bump);
    return () => {
      clearInterval(timer);
      document.removeEventListener('visibilitychange', shown);
      window.removeEventListener('focus', bump);
    };
  }, [ms]);
  return tick;
}

/** How long after the service answered again the list may still be a restart's empty or offline list. */
export const RECONNECT_GRACE_MS = 90_000;

/**
 * True while the service restarts or has just come back (CRSP205, CORE-3, UX-6, UX-29): the list could not be
 * read, or it was read again within the grace time after a failed read and shows no machine online yet. The
 * agents join again a moment after the controller listens; that wait is its own state, never "no machines".
 */
export function isReconnecting(
  result: Loaded<Schemas['MachineList']>,
  lastFailedAt: number | undefined,
  now: number,
): boolean {
  if (result.state === 'failed') return true;
  if (result.state !== 'ready' || lastFailedAt === undefined) return false;
  if (now - lastFailedAt >= RECONNECT_GRACE_MS) return false;
  return !result.data.items.some((m) => m.online);
}

export function useMachines(): {
  result: Loaded<Schemas['MachineList']>;
  reload: () => void;
  /** The service is restarting or has just come back: do not say "no machines" (CRSP205). */
  reconnecting: boolean;
} {
  const client = useClient();
  const v = useVersion('machines');
  const tick = useRefreshTick();
  const loaded = useLoad(() => unwrap(client.GET('/api/v1/machines')), [v, tick]);
  const failedAt = useRef<number | undefined>(undefined);
  const [, rerender] = useState(0);
  if (loaded.result.state === 'failed') failedAt.current = Date.now();
  const reconnecting = isReconnecting(loaded.result, failedAt.current, Date.now());
  useEffect(() => {
    if (!reconnecting || failedAt.current === undefined) return;
    // Leave the state when the grace time ends even if nothing else changes.
    const left = RECONNECT_GRACE_MS - (Date.now() - failedAt.current);
    const t = setTimeout(() => rerender((n) => n + 1), Math.max(left, 0) + 50);
    return () => clearTimeout(t);
  }, [reconnecting, loaded.result]);
  return { ...loaded, reconnecting };
}
