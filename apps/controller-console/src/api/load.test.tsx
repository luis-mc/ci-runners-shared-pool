import { act, cleanup, renderHook } from '@testing-library/react';
import { afterEach, describe, expect, it } from 'vitest';
import { useLoad } from './hooks';

afterEach(cleanup);

/** Run a step, then let the promises it settled run their handlers. */
async function settle(step: () => void) {
  await act(async () => {
    step();
    await Promise.resolve();
  });
}

function deferred<T>() {
  let resolve!: (v: T) => void;
  const promise = new Promise<T>((r) => (resolve = r));
  return { promise, resolve };
}

describe('useLoad refreshes (CRSP290, audit F6)', () => {
  it('runs one request after the one in flight, however many refreshes came meanwhile', async () => {
    const calls: ReturnType<typeof deferred<number>>[] = [];
    const load = () => {
      const d = deferred<number>();
      calls.push(d);
      return d.promise;
    };
    const { result, rerender } = renderHook(
      ({ v }: { v: number }) => useLoad(load, ['key'], [v]),
      { initialProps: { v: 0 } },
    );
    expect(calls).toHaveLength(1);
    // Five events arrive while the first request is out: nothing new starts yet.
    for (let v = 1; v <= 5; v++) rerender({ v });
    expect(calls).toHaveLength(1);
    await settle(() => calls[0]?.resolve(1));
    expect(result.current.result).toMatchObject({ state: 'ready', data: 1 });
    expect(calls).toHaveLength(2);
    await settle(() => calls[1]?.resolve(2));
    expect(result.current.result).toMatchObject({ state: 'ready', data: 2 });
    expect(calls).toHaveLength(2);
  });

  it('starts a request at once when nothing is in flight', async () => {
    const calls: ReturnType<typeof deferred<number>>[] = [];
    const load = () => {
      const d = deferred<number>();
      calls.push(d);
      return d.promise;
    };
    const { rerender } = renderHook(({ v }: { v: number }) => useLoad(load, ['k'], [v]), {
      initialProps: { v: 0 },
    });
    await settle(() => calls[0]?.resolve(1));
    rerender({ v: 1 });
    expect(calls).toHaveLength(2);
  });

  it('drops the old request and starts again when the key changes', async () => {
    const calls: ReturnType<typeof deferred<string>>[] = [];
    const load = () => {
      const d = deferred<string>();
      calls.push(d);
      return d.promise;
    };
    const { result, rerender } = renderHook(
      ({ k }: { k: string }) => useLoad(load, [k], []),
      { initialProps: { k: 'a' } },
    );
    rerender({ k: 'b' });
    expect(calls).toHaveLength(2);
    await settle(() => calls[0]?.resolve('old'));
    expect(result.current.result.state).toBe('loading');
    await settle(() => calls[1]?.resolve('new'));
    expect(result.current.result).toMatchObject({ state: 'ready', data: 'new' });
  });
});
