// A mocked API for the component tests: routes by method and path, a call log, and a fake event stream.
import { createApiClient } from '@crsp/contract';
import { render } from '@testing-library/react';
import { MemoryRouter } from 'react-router-dom';
import { App } from '../App';
import type { EventSourceLike } from '../live/store';
import type { Schemas } from '../api/api';

export interface MockCall {
  method: string;
  path: string;
  query: URLSearchParams;
  body: unknown;
}

type ReplyFn = (call: MockCall, params: Record<string, string>) => unknown;
// A reply may be a promise, so a test can hold a request open.
type Reply = ReplyFn | Record<string, unknown> | Response | undefined;
interface Route {
  method: string;
  parts: string[];
  reply: Reply;
}

export class MockApi {
  private routes: Route[] = [];
  readonly calls: MockCall[] = [];

  on(method: string, path: string, reply: Reply): this {
    this.routes.unshift({ method: method.toUpperCase(), parts: path.split('/'), reply });
    return this;
  }

  count(method: string, path: string): number {
    return this.calls.filter((c) => c.method === method.toUpperCase() && c.path === path)
      .length;
  }

  fetch = async (input: Request | string): Promise<Response> => {
    const req = typeof input === 'string' ? new Request(input) : input;
    const url = new URL(req.url);
    const text = await req.clone().text();
    const call: MockCall = {
      method: req.method,
      path: url.pathname,
      query: url.searchParams,
      body: text ? (JSON.parse(text) as unknown) : undefined,
    };
    this.calls.push(call);
    const parts = url.pathname.split('/');
    for (const r of this.routes) {
      if (r.method !== req.method || r.parts.length !== parts.length) continue;
      const params: Record<string, string> = {};
      const ok = r.parts.every((p, i) => {
        if (p.startsWith(':')) {
          params[p.slice(1)] = decodeURIComponent(parts[i] ?? '');
          return true;
        }
        return p === parts[i];
      });
      if (!ok) continue;
      const out = await (typeof r.reply === 'function' ? r.reply(call, params) : r.reply);
      if (out instanceof Response) return out;
      if (out === undefined) return new Response(null, { status: 204 });
      return Response.json(out);
    }
    return Response.json(
      {
        error: {
          code: 'not_found',
          message: `No mock for ${req.method} ${url.pathname}`,
        },
      },
      { status: 404 },
    );
  };
}

export function apiError(
  status: number,
  code: string,
  message: string,
  details: unknown[] = [],
): Response {
  return Response.json({ error: { code, message, details } }, { status });
}

/** A stream the test drives by hand. */
export class FakeEventSource implements EventSourceLike {
  static instances: FakeEventSource[] = [];
  onopen: ((e: unknown) => void) | null = null;
  onerror: ((e: unknown) => void) | null = null;
  onmessage: ((e: { data: string; lastEventId?: string }) => void) | null = null;
  closed = false;
  private listeners = new Map<string, ((e: { data: string }) => void)[]>();
  constructor(readonly url: string) {
    FakeEventSource.instances.push(this);
  }
  addEventListener(type: string, fn: (e: { data: string }) => void): void {
    this.listeners.set(type, [...(this.listeners.get(type) ?? []), fn]);
  }
  close(): void {
    this.closed = true;
  }
  emit(event: Schemas['LiveEvent']): void {
    for (const fn of this.listeners.get(event.type) ?? [])
      fn({ data: JSON.stringify(event) });
  }
  fail(): void {
    this.onerror?.(new Error('dropped'));
  }
  static latest(): FakeEventSource {
    const s = FakeEventSource.instances[FakeEventSource.instances.length - 1];
    if (!s) throw new Error('no stream was opened');
    return s;
  }
  static reset(): void {
    FakeEventSource.instances = [];
  }
}

export const owner: Schemas['User'] = {
  id: 1,
  login: 'octo',
  role: 'owner',
  added_at: '2026-11-01T10:00:00Z',
};
export const viewer: Schemas['User'] = {
  id: 2,
  login: 'view',
  role: 'viewer',
  added_at: '2026-11-02T10:00:00Z',
};

function stoppedOrTaking(state: string | undefined): Schemas['Machine']['taking'] {
  return state === 'drained' || state === 'draining'
    ? { taking_jobs: false, reason: 'stopped' }
    : { taking_jobs: true };
}

export function machine(over: Partial<Schemas['Machine']> = {}): Schemas['Machine'] {
  return {
    id: 'm1',
    name: 'build-1',
    os: 'linux',
    arch: 'x64',
    state: 'active',
    online: true,
    is_controller: false,
    backends: [
      { name: 'docker', present: true, usable: true, allowed: true, version: '27' },
    ],
    settings: {
      budget_memory_pct: 80,
      budget_cpu_pct: 80,
      job_slots: 4,
      light_slots: 2,
      heavy_slots: 2,
      allowed_backends: ['docker'],
      drain_timeout_minutes: 30,
      windows_symlink_right: false,
      mode: 'automatic',
      pause_on_battery: true,
      cpu_cap_pct: 100,
    },
    accepting: true,
    load: {
      memory_pct: 40,
      cpu_pct: 20,
      memory_known: true,
      cpu_known: true,
      sampled_at: '2026-11-14T10:00:00Z',
      stale: false,
    },
    slots_used: 1,
    last_seen_at: '2026-11-14T10:00:00Z',
    ...over,
    // The controller computes one state; a drained machine is stopped unless a test says more.
    taking: over.taking ?? stoppedOrTaking(over.state),
  };
}

export function runner(over: Partial<Schemas['Runner']> = {}): Schemas['Runner'] {
  return {
    id: 'r1',
    name: 'crsp-linux-1',
    machine_id: 'm1',
    machine_name: 'box',
    os: 'linux',
    arch: 'x64',
    kind: 'persistent',
    backend: 'persistent',
    isolation: 'persistent',
    labels: ['self-hosted'],
    scope: { kind: 'repository', repository_id: 7 },
    state: 'idle',
    managed: true,
    keeps_state: true,
    ...over,
  };
}

export function repository(
  over: Partial<Schemas['Repository']> = {},
): Schemas['Repository'] {
  return {
    id: 7,
    full_name: 'octo/app',
    owner: 'octo',
    name: 'app',
    installation_id: 1,
    visibility: 'private',
    pool_on: false,
    user_is_github_admin: true,
    user_admin_known: true,
    variables: [],
    policy: {
      priority: 10,
      max_parallel_jobs: 2,
      allow_fork_pull_requests: false,
      labels: ['self-hosted'],
      runner_groups: [],
    },
    ...over,
  };
}

export function overview(over: Partial<Schemas['Overview']> = {}): Schemas['Overview'] {
  return {
    machines: [machine()],
    queued_count: 0,
    running: [],
    last_event_id: '1',
    ...over,
  };
}

/** A signed-in mock with the routes every screen needs. */
export function signedIn(user: Schemas['User'] = owner): MockApi {
  return new MockApi()
    .on('GET', '/api/v1/me', {
      user,
      csrf: 'tok',
      github_signin: { state: 'active', expires_at: '2026-11-17T18:30:00Z' },
    })
    .on('GET', '/api/v1/me/preferences', { theme: 'dark' })
    .on('GET', '/api/v1/app/webhook', { state: 'off' })
    .on('GET', '/api/v1/settings/public-repositories', {
      enabled: false,
      public_on_pool: 0,
    })
    .on('GET', '/api/v1/job-sizes', {
      repository: '',
      items: [],
      cost_table: [],
      file_error: '',
    })
    .on('GET', '/api/v1/overview', overview())
    .on('GET', '/api/v1/machines', { items: [machine()] })
    .on('GET', '/api/v1/github/installations', {
      items: [
        {
          id: 1,
          account_login: 'octo',
          account_type: 'User',
          suspended: false,
          repository_selection: 'all',
        },
      ],
      stale: { stale: false },
    })
    .on('GET', '/api/v1/setup', {
      has_app: true,
      has_owner: true,
      setup_link_redeemed: true,
      steps: { connect_github: 'done', first_machine: 'done', first_repository: 'done' },
    });
}

export function renderApp(api: MockApi, path = '/', opts: { stream?: boolean } = {}) {
  FakeEventSource.reset();
  const client = createApiClient('http://controller.test', api.fetch as typeof fetch);
  const view = render(
    <MemoryRouter initialEntries={[path]}>
      <App
        client={client}
        eventSource={
          opts.stream === false
            ? () => new FakeEventSource('x')
            : (url) => new FakeEventSource(url)
        }
      />
    </MemoryRouter>,
  );
  return { ...view, client };
}
