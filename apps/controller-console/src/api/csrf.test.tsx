// The console sends the session's CSRF token on every change and never on a read (R6.2). The
// token comes from getSession and is held by the API wrapper, never stored in the browser.
import { createApiClient } from '@crsp/contract';
import { render, waitFor } from '@testing-library/react';
import { describe, expect, it } from 'vitest';
import { ApiProvider, useApi } from './api';

function seen() {
  const calls: { method: string; token: string | null; credentials: string }[] = [];
  const fetchImpl: typeof fetch = (input) => {
    const req = input as Request;
    calls.push({
      method: req.method,
      token: req.headers.get('X-CSRF-Token'),
      credentials: req.credentials,
    });
    return Promise.resolve(
      new Response('{}', {
        status: 200,
        headers: { 'Content-Type': 'application/json' },
      }),
    );
  };
  return { calls, client: createApiClient('http://controller', fetchImpl) };
}

function Probe({
  token,
  run,
}: {
  token: string | undefined;
  run: (c: ReturnType<typeof useApi>) => void;
}) {
  const api = useApi();
  api.setCsrf(token);
  run(api);
  return null;
}

describe('CSRF token on requests (R6.2)', () => {
  it('is sent on POST, PUT, PATCH and DELETE, and not on GET', async () => {
    const { calls, client } = seen();
    render(
      <ApiProvider client={client}>
        <Probe
          token="tok-1"
          run={(api) => {
            void api.client.GET('/api/v1/machines', {} as never);
            void api.client.POST('/api/v1/auth/logout', {} as never);
            void api.client.PUT('/api/v1/me/preferences', {
              body: { theme: 'dark' },
            } as never);
            void api.client.PATCH('/api/v1/users/{userId}', {
              params: { path: { userId: 1 } },
              body: { role: 'admin' },
            } as never);
            void api.client.DELETE('/api/v1/users/{userId}', {
              params: { path: { userId: 2 } },
            } as never);
          }}
        />
      </ApiProvider>,
    );
    await waitFor(() => expect(calls.length).toBe(5));
    const by = (m: string) => calls.find((c) => c.method === m);
    expect(by('GET')?.token).toBeNull();
    for (const m of ['POST', 'PUT', 'PATCH', 'DELETE'])
      expect(by(m)?.token, m).toBe('tok-1');
    // Same-origin requests carry the session cookie; the page never reads it.
    for (const c of calls) expect(c.credentials).not.toBe('omit');
  });

  it('sends no token before a session exists', async () => {
    const { calls, client } = seen();
    render(
      <ApiProvider client={client}>
        <Probe
          token={undefined}
          run={(api) => void api.client.POST('/api/v1/auth/logout', {} as never)}
        />
      </ApiProvider>,
    );
    await waitFor(() => expect(calls.length).toBe(1));
    expect(calls[0]?.token).toBeNull();
  });
});
