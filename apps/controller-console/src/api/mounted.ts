// A wizard that polls a plan must stop when its page goes away: a timer or a read that fires after the page
// is gone writes to a screen nobody sees (and, in a test, to a window that no longer exists).
import { useCallback, useEffect, useRef } from 'react';

export interface Mounted {
  /** True while the page is on screen. */
  mounted: { current: boolean };
  /** Waits `ms`, then says whether the page is still on screen. Leaving the page ends the wait at once. */
  sleep: (ms: number) => Promise<boolean>;
}

export function useMounted(): Mounted {
  const mounted = useRef(true);
  const wake = useRef<(() => void) | undefined>(undefined);
  useEffect(() => {
    mounted.current = true;
    return () => {
      mounted.current = false;
      wake.current?.();
    };
  }, []);
  const sleep = useCallback(
    (ms: number) =>
      new Promise<boolean>((resolve) => {
        const timer = setTimeout(() => {
          wake.current = undefined;
          resolve(mounted.current);
        }, ms);
        wake.current = () => {
          clearTimeout(timer);
          wake.current = undefined;
          resolve(false);
        };
      }),
    [],
  );
  return { mounted, sleep };
}
