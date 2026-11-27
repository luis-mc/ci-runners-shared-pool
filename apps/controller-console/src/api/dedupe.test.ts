import { describe, expect, it, vi } from 'vitest';
import { dedupeFetch } from './dedupe';

function answer(body: string): Response {
  return new Response(body, { status: 200 });
}

describe('dedupeFetch', () => {
  it('sends one request for the same GET made while it is on its way', async () => {
    const release: Record<string, (r: Response) => void> = {};
    const base = vi.fn(
      (r: Request) =>
        new Promise<Response>((resolve) => (release[new URL(r.url).pathname] = resolve)),
    );
    const f = dedupeFetch(base);
    const a = f('http://x/api/v1/machines');
    const b = f('http://x/api/v1/machines');
    const c = f('http://x/api/v1/other');
    expect(base).toHaveBeenCalledTimes(2);
    release['/api/v1/machines']?.(answer('{"items":[]}'));
    const [ra, rb] = await Promise.all([a, b]);
    expect(await ra.json()).toEqual({ items: [] });
    expect(await rb.json()).toEqual({ items: [] });
    release['/api/v1/other']?.(answer('{}'));
    await c;
  });

  it('keeps nothing once the answer has arrived', async () => {
    const base = vi.fn(() => Promise.resolve(answer('1')));
    const f = dedupeFetch(base);
    await f('http://x/api/v1/machines');
    await f('http://x/api/v1/machines');
    expect(base).toHaveBeenCalledTimes(2);
  });

  it('sends a change as it is', async () => {
    const base = vi.fn(() => Promise.resolve(answer('1')));
    const f = dedupeFetch(base);
    await Promise.all([
      f('http://x/api/v1/machines', { method: 'POST' }),
      f('http://x/api/v1/machines', { method: 'POST' }),
    ]);
    expect(base).toHaveBeenCalledTimes(2);
  });

  it('asks again for a caller whose shared request failed', async () => {
    const base = vi
      .fn<(r: Request) => Promise<Response>>()
      .mockRejectedValueOnce(new Error('aborted'))
      .mockResolvedValueOnce(answer('ok'));
    const f = dedupeFetch(base);
    const first = f('http://x/api/v1/me');
    const second = f('http://x/api/v1/me');
    await expect(first).rejects.toThrow('aborted');
    expect(await (await second).text()).toBe('ok');
  });
});
