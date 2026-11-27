// Loading with the states of every list: loading, ready, could not load (R5.31). A reload keeps the data on
// screen (UX-35): it never flips back to loading and never moves focus.
import { useCallback, useEffect, useRef, useState } from 'react';
import { ApiError, errorText } from './api';

export type Loaded<T> =
  | { state: 'loading' }
  | { state: 'ready'; data: T; loadedAt: number }
  | { state: 'failed'; error: ApiError | Error; data?: T; loadedAt?: number };

/**
 * `deps` name the data: when one changes, the old answer is dropped and a new request starts. `refreshOn` say the same
 * data may have changed (a live event, a poll tick): a request already in flight is left to finish and one more runs
 * after it, however many refreshes came meanwhile, so a burst of events costs at most two requests (CRSP290, F6).
 */
export function useLoad<T>(
  load: () => Promise<T>,
  deps: readonly unknown[],
  refreshOn: readonly unknown[] = [],
): { result: Loaded<T>; reload: () => void } {
  const [result, setResult] = useState<Loaded<T>>({ state: 'loading' });
  const [tick, setTick] = useState(0);
  const last = useRef<{ data: T; loadedAt: number } | undefined>(undefined);
  const loadRef = useRef(load);
  loadRef.current = load;
  const inFlight = useRef(false);
  const dirty = useRef(false);
  const seen = useRef<readonly unknown[]>(refreshOn);
  useEffect(() => {
    const same =
      seen.current.length === refreshOn.length &&
      seen.current.every((v, i) => Object.is(v, refreshOn[i]));
    seen.current = refreshOn;
    if (same) return;
    if (inFlight.current) dirty.current = true;
    else setTick((t) => t + 1);
  }, [...refreshOn]);
  useEffect(() => {
    let live = true;
    inFlight.current = true;
    dirty.current = false;
    const done = () => {
      if (!live) return;
      inFlight.current = false;
      if (dirty.current) {
        dirty.current = false;
        setTick((t) => t + 1);
      }
    };
    loadRef
      .current()
      .then((data) => {
        if (!live) return;
        last.current = { data, loadedAt: Date.now() };
        setResult({ state: 'ready', ...last.current });
        done();
      })
      .catch((e: unknown) => {
        if (!live) return;
        const error = e instanceof Error ? e : new Error(errorText(e));
        setResult({ state: 'failed', error, ...(last.current ?? {}) });
        done();
      });
    return () => {
      live = false;
      inFlight.current = false;
    };
  }, [...deps, tick]);
  const reload = useCallback(() => setTick((t) => t + 1), []);
  return { result, reload };
}

/** A command: busy until the server answers (UX-13), its result in one line (UX-27). */
export function useCommand<A extends unknown[], R>(
  run: (...args: A) => Promise<R>,
): {
  busy: boolean;
  error: string | undefined;
  run: (...args: A) => Promise<R | undefined>;
  clear: () => void;
} {
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | undefined>(undefined);
  const exec = useCallback(
    async (...args: A) => {
      setBusy(true);
      setError(undefined);
      try {
        return await run(...args);
      } catch (e) {
        setError(errorText(e));
        return undefined;
      } finally {
        setBusy(false);
      }
    },
    [run],
  );
  return { busy, error, run: exec, clear: () => setError(undefined) };
}

export interface Question {
  items: readonly string[];
  waitsFor: readonly string[];
}

/**
 * A destructive step asks first (UX-9, UX-62, R5.30): the question card names each item that goes. Nothing changes
 * until the user says yes. If the server still answers `needs_confirmation` (a job started meanwhile) the card
 * stays open with the jobs it waits for.
 */
export function useQuestion(run: () => Promise<unknown>, onDone: () => void) {
  const [question, setQuestion] = useState<Question | undefined>(undefined);
  const cmd = useCommand(async () => {
    try {
      await run();
      setQuestion(undefined);
      onDone();
    } catch (e) {
      if (e instanceof ApiError && e.code === 'needs_confirmation') {
        setQuestion((prev) => ({
          items: prev?.items ?? [],
          waitsFor: e.details.map((d) => d.text ?? d.id ?? '').filter(Boolean),
        }));
        return;
      }
      throw e;
    }
  });
  return {
    question,
    ask: (items: readonly string[], waitsFor: readonly string[] = []) => {
      cmd.clear();
      setQuestion({ items, waitsFor });
    },
    cancel: () => {
      cmd.clear();
      setQuestion(undefined);
    },
    confirm: cmd.run,
    busy: cmd.busy,
    error: cmd.error,
  };
}
