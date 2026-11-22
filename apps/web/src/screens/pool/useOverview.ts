// The live overview for a screen that opened without the stream's snapshot: one read seeds the store (R6.12).
import { useEffect, useState } from 'react';
import { unwrap, useClient, type Schemas } from '../../api/api';
import { useLiveState, useLiveStore } from '../../live/LiveProvider';

export function useOverview(): {
  overview: Schemas['Overview'] | undefined;
  failed: string | undefined;
  /** When the overview was last read (ms), the base of the start estimates. */
  readAt: number;
} {
  const client = useClient();
  const live = useLiveState();
  const store = useLiveStore();
  const [failed, setFailed] = useState<string | undefined>(undefined);
  const has = live.overview !== undefined;
  useEffect(() => {
    if (has) return;
    let on = true;
    unwrap(client.GET('/api/v1/overview'))
      .then((first) => {
        if (on) store.seed(first);
      })
      .catch((e: unknown) => {
        if (on) setFailed(e instanceof Error ? e.message : 'The controller did not answer.');
      });
    return () => {
      on = false;
    };
  }, [client, has, store]);
  return { overview: live.overview, failed, readAt: live.lastEventAt ?? Date.now() };
}
