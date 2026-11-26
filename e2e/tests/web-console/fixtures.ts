// A mocked controller API for the console e2e specs. No spec calls the real GitHub API or the real controller API:
// every /api/ request is answered here (CRSP24).
import type { Page, Route } from '@playwright/test';
import { registryApi } from './registry-fixtures';

export interface Call {
  method: string;
  path: string;
  query: URLSearchParams;
  body: unknown;
}

type Handler = (call: Call, params: Record<string, string>) => unknown;

export const owner = {
  id: 1,
  login: 'octo',
  role: 'owner',
  added_at: '2026-11-01T10:00:00Z',
};
export const viewer = {
  id: 2,
  login: 'view',
  role: 'viewer',
  added_at: '2026-11-02T10:00:00Z',
};

export const machine = (over: Record<string, unknown> = {}) => ({
  id: 'm1',
  name: 'build-1',
  os: 'linux',
  arch: 'x64',
  state: 'active',
  online: true,
  taking: { taking_jobs: true },
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
  },
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
  running_jobs: [
    {
      id: 9,
      repository: 'octo/app',
      run_id: 1,
      run_attempt: 1,
      github_job_id: 1,
      job_name: 'e2e',
      labels: [],
      state: 'running',
    },
  ],
  ...over,
});

/** A machine the owner stopped from the console: the one computed state says stopped (OPS29). */
export const stoppedMachine = (over: Record<string, unknown> = {}) =>
  machine({
    state: 'drained',
    accepting: false,
    pause_reason: 'paused',
    taking: { taking_jobs: false, reason: 'stopped' },
    ...over,
  });

/** A machine whose agent needs recovery (operations R5.44, web-console R5.33). */
export const needingRecovery = (over: Record<string, unknown> = {}) =>
  machine({
    recovery: {
      state: 'needs_recovery',
      reason: 'old_protocol',
      has: '0.1.0',
      needs: '0.3.0',
      since: '2026-11-14T08:00:00Z',
      armed: false,
    },
    ...over,
  });

export const repository = (over: Record<string, unknown> = {}) => ({
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
});

export const runner = (over: Record<string, unknown> = {}) => ({
  id: 'r1',
  name: 'crsp-linux-1',
  machine_id: 'm1',
  machine_name: 'build-1',
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
});

export const cache = (over: Record<string, unknown> = {}) => ({
  id: 'c1',
  kind: 'tool_cache',
  machine_id: 'm1',
  runner_id: 'r1',
  runner_name: 'crsp-linux-1',
  size_bytes: 3_200_000_000,
  cap_bytes: 10_000_000_000,
  entry_count: 42,
  state: 'ready',
  ...over,
});

export const overview = (machines: unknown[] = [machine()]) => ({
  machines,
  queued_count: 2,
  running: [],
  last_event_id: '1',
});

/** The slot timeline (LOOK58): two slots of build-1, a finished job, a running job and a forecast job. */
export const timeline = () => {
  const now = Date.now();
  const at = (min: number) => new Date(now + min * 60_000).toISOString();
  return {
    now: at(0),
    from: at(-360),
    to: at(90),
    more_before: true,
    rows: [
      {
        key: 'm1:1',
        machine_id: 'm1',
        machine_name: 'build-1',
        slot: 1,
        label: 'build-1 slot 1',
      },
      {
        key: 'm1:2',
        machine_id: 'm1',
        machine_name: 'build-1',
        slot: 2,
        label: 'build-1 slot 2',
      },
    ],
    bars: [
      {
        id: 'b1',
        job_id: 11,
        row_id: 'm1:1',
        state: 'done',
        label: 'unit tests',
        repository: 'acme/app',
        start: at(-120),
        end: at(-60),
        detail: 'finished',
      },
      {
        id: 'b2',
        job_id: 12,
        row_id: 'm1:1',
        state: 'running',
        label: 'build',
        repository: 'acme/app',
        start: at(-20),
        detail: 'running',
      },
      {
        id: 'b3',
        job_id: 13,
        row_id: 'm1:2',
        state: 'forecast',
        label: 'deploy',
        repository: 'acme/app',
        start: at(10),
        end: at(60),
        end_lo: at(50),
        end_hi: at(70),
        detail: 'starts in about 10 min',
      },
      {
        id: 'b4',
        job_id: 14,
        row_id: 'm1:2',
        state: 'reserved',
        label: 'e2e',
        repository: 'acme/app',
        start: at(-90),
        end: at(-10),
        detail: 'reserved: waits for room in the pool',
      },
    ],
  };
};

/** The health a machine reports (LOOK58c, machines R5.72 to R5.79): everything known, or a Windows machine that could not say. */
export const machineHealth = (over: Record<string, unknown> = {}) => ({
  machine_id: 'm1',
  reported: true,
  received_at: '2026-11-22T10:00:00Z',
  stale: false,
  boot: { uptime_seconds: 270_000, last_boot_at: '2026-11-19T06:00:00Z' },
  volumes: [
    {
      role: 'system',
      path: '/',
      total_bytes: 500_000_000_000,
      free_bytes: 200_000_000_000,
      free_pct: 40,
      days_until_full: 12,
    },
    {
      role: 'docker',
      path: '/var/lib/docker',
      total_bytes: 200_000_000_000,
      free_bytes: 50_000_000_000,
      free_pct: 25,
      days_note: 'free space is not falling',
    },
  ],
  swap: { total_bytes: 8_000_000_000, used_bytes: 1_000_000_000 },
  oom: { total_kills: 0 },
  connectivity: {
    rtt_ms: 12,
    clock_skew_seconds: 1.5,
    reconnects_24h: 0,
    github: { reachable: true, latency_ms: 80 },
    dns: { ok: true },
  },
  versions: {
    agent: '1.4.0',
    controller: '1.4.0',
    runner_app: '2.330.0',
    docker: '27.1',
  },
  updates: { restart_pending: false },
  power: { on_battery: false, sleep_enabled: false },
  docker_use: { images_bytes: 20_000_000_000, build_cache_bytes: 5_000_000_000 },
  thermal: { max_temp_celsius: 61.5, throttled: false },
  load: { load1: 1.2, load5: 0.9, load15: 0.7 },
  ...over,
});

const statsSet = () => ({
  counts: {
    succeeded: 18,
    failed: 2,
    cancelled: 1,
    timed_out: 1,
    over_memory: 1,
    stopped: 1,
    other: 0,
    total: 24,
  },
  avg_run_seconds: 252,
  avg_queue_wait_seconds: 40,
  busy_seconds: 6000,
  busy_pct: 35,
  last_failure: {
    job_id: 77,
    repository: 'octo/app',
    job: 'build',
    run_id: 9,
    class: 'failed',
    at: '2026-11-14T08:00:00Z',
  },
  top_failing: [
    {
      repository: 'octo/app',
      job: 'build',
      failures: 3,
      last_at: '2026-11-14T08:00:00Z',
    },
  ],
});

export const machineStats = (window = '24h') => ({
  window,
  from: '2026-11-13T10:00:00Z',
  to: '2026-11-14T10:00:00Z',
  machine: statsSet(),
  runners: [{ runner_id: 'r1', runner_name: 'crsp-linux-1', stats: statsSet() }],
});

export class Mock {
  readonly calls: Call[] = [];
  private handlers: { method: string; parts: string[]; fn: Handler }[] = [];

  on(method: string, path: string, reply: unknown): this {
    this.handlers.unshift({
      method,
      parts: path.split('/'),
      fn: typeof reply === 'function' ? (reply as Handler) : () => reply,
    });
    return this;
  }

  count(method: string, path: string): number {
    return this.calls.filter((c) => c.method === method && c.path === path).length;
  }

  async install(page: Page): Promise<void> {
    await page.route('**/api/v1/events', (route) =>
      route.fulfill({
        status: 200,
        headers: { 'content-type': 'text/event-stream', 'cache-control': 'no-cache' },
        body: `event: snapshot\nid: 1\ndata: ${JSON.stringify({ id: '1', type: 'snapshot', at: '2026-11-14T10:00:00Z', snapshot: overview() })}\n\n`,
      }),
    );
    await page.route('**/api/v1/**', (route) => this.answer(route));
    // The lifecycle endpoints (/api/v1/update/*): idle and current, until a spec routes its own. Routes run last-added first, so this goes after the catch-all.
    await page.route(
      /\/api\/v1\/update\/(status|history|fleet|policy|install)$/,
      (route) => {
        const path = new URL(route.request().url()).pathname;
        if (route.request().method() !== 'GET') return route.fulfill({ json: {} });
        if (path.endsWith('/status'))
          return route.fulfill({
            json: {
              state: 'up_to_date',
              current: '1.4.0',
              available: null,
              required: false,
              policy: 'automatic_when_idle',
              window: null,
              next_check_at: null,
              failures: null,
              waiting_for_jobs: false,
            },
          });
        return route.fulfill({ json: [] });
      },
    );
  }

  private async answer(route: Route): Promise<void> {
    const req = route.request();
    const url = new URL(req.url());
    const text = req.postData();
    const call: Call = {
      method: req.method(),
      path: url.pathname,
      query: url.searchParams,
      body: text ? (JSON.parse(text) as unknown) : undefined,
    };
    this.calls.push(call);
    const parts = url.pathname.split('/');
    for (const h of this.handlers) {
      if (h.method !== call.method || h.parts.length !== parts.length) continue;
      const params: Record<string, string> = {};
      const ok = h.parts.every((p, i) => {
        if (p.startsWith(':')) {
          params[p.slice(1)] = parts[i] ?? '';
          return true;
        }
        return p === parts[i];
      });
      if (!ok) continue;
      const out = await h.fn(call, params);
      if (out === undefined) return route.fulfill({ status: 204 });
      if (out && typeof out === 'object' && 'status' in out && 'error' in out)
        return route.fulfill({
          status: (out as { status: number }).status,
          json: { error: (out as { error: unknown }).error },
        });
      return route.fulfill({ status: 200, json: out });
    }
    return route.fulfill({
      status: 404,
      json: {
        error: { code: 'not_found', message: `No mock for ${call.method} ${call.path}` },
      },
    });
  }
}

export const failure = (
  status: number,
  code: string,
  message: string,
  details: unknown[] = [],
) => ({
  status,
  error: { code, message, details },
});

/** The one image the console fixtures show: not built yet, a repository needs a layer it lacks. */
export const image = () => ({
  id: 'img_m1_linux',
  machine_id: 'm1',
  os: 'linux',
  base: 'ubuntu-24.04',
  base_tools: [
    { name: 'Ubuntu', version: '24.04' },
    { name: 'git' },
    { name: 'curl' },
    { name: 'python3' },
    { name: 'jq' },
    { name: 'zip' },
    { name: 'node', version: '26' },
    { name: 'pnpm' },
  ],
  extra_packages: [{ repository: 'octo/app', packages: ['libssl-dev'] }],
  layers: [
    {
      id: 'playwright',
      name: 'Browsers for Playwright',
      enabled: false,
      size_bytes: 0,
      adds: [{ name: 'chromium' }, { name: 'firefox' }, { name: 'webkit' }],
      purpose: 'your tests drive a browser',
      needed_by: { count: 1, repositories: ['octo/app'], failing: 1 },
    },
    {
      id: 'kvm',
      name: 'KVM virtual machines',
      enabled: true,
      size_bytes: 0,
      adds: [{ name: 'qemu-system-x86_64' }, { name: 'qemu-img' }],
      purpose: 'your jobs boot a virtual machine with qemu',
      needed_by: { count: 0, repositories: [], failing: 0 },
    },
  ],
  size_bytes: 0,
  newest: true,
  state: 'not_built',
});

/** The routes every signed-in screen needs. */
/** The guided dependency setup (dependency-setup spec): a Windows PC with no WSL2 and no Docker, one recommended choice. */
function dependencyCheck(): unknown {
  const facts = (name: string) => ({
    name,
    version: '2.0',
    source: 'download.docker.com',
    download_bytes: 120_000_000,
    disk_bytes: 900_000_000,
    typical_minutes: 5,
    needs_admin: true,
    restart_likely: name === 'WSL2',
    undo: `Remove ${name} from Apps.`,
  });
  return {
    machine_id: 'm1',
    ready: false,
    os: 'windows',
    admin: true,
    disk_free_bytes: 50_000_000_000,
    virtualization_off: false,
    items: [
      {
        id: 'wsl2',
        name: 'WSL2',
        state: 'missing',
        why: 'Containers on Windows run in WSL2.',
      },
      {
        id: 'docker-in-wsl2',
        name: 'Docker',
        state: 'missing',
        why: 'Linux runners run in a container, and containers need Docker.',
      },
    ],
    choices: [
      {
        id: 'wsl-docker',
        title: 'Docker in WSL2',
        item_ids: ['wsl2', 'docker-in-wsl2'],
        recommended: true,
        facts: [facts('WSL2'), facts('Docker')],
        requires_licence_tick: false,
        manual_steps: ['wsl --install --no-distribution'],
      },
    ],
  };
}

export function signedIn(user: unknown = owner): Mock {
  return registryApi(signedInBase(user));
}

function signedInBase(user: unknown): Mock {
  return new Mock()
    .on('GET', '/api/v1/me', {
      user,
      csrf: 'tok',
      github_signin: { state: 'active', expires_at: '2026-11-17T18:30:00Z' },
    })
    .on('GET', '/api/v1/me/preferences', { theme: 'dark' })
    .on('GET', '/api/v1/overview', overview())
    .on('GET', '/api/v1/update', {
      current_version: '1.4.0',
      latest_version: '1.4.0',
      update_available: false,
      state: 'idle',
      auto_update: false,
      running_jobs: [],
      drain_timeout_minutes: 60,
    })
    .on('GET', '/api/v1/setup', {
      has_app: true,
      has_owner: true,
      setup_link_redeemed: true,
      steps: { connect_github: 'done', first_machine: 'done', first_repository: 'done' },
    })
    .on('GET', '/api/v1/settings/pool', {
      budget_memory_pct: 80,
      budget_cpu_pct: 80,
      size_slots_from_machine: true,
      default_job_slots: 2,
      drain_timeout_minutes: 30,
    })
    .on('GET', '/api/v1/settings/queue', {
      order: ['priority', 'event_class', 'fair_share', 'queued_at'],
      fair_share: true,
      no_runner_wait_minutes: 2,
    })
    .on('GET', '/api/v1/settings/public-repositories', {
      enabled: false,
      public_on_pool: 0,
    })
    .on('GET', '/api/v1/app/webhook', { state: 'off' })
    .on('GET', '/api/v1/machines', { items: [machine()] })
    .on('GET', '/api/v1/machines/:id', machine())
    .on('POST', '/api/v1/machines/:id/dependencies/check', dependencyCheck())
    .on('GET', '/api/v1/machines/:id/dependencies/jobs', [])
    .on('GET', '/api/v1/timeline', timeline())
    .on('GET', '/api/v1/machines/:id/health', machineHealth())
    .on('GET', '/api/v1/machines/:id/job-stats', (c: { query: URLSearchParams }) =>
      machineStats(c.query.get('window') ?? '24h'),
    )
    .on('GET', '/api/v1/repositories/:id', repository())
    .on('GET', '/api/v1/job-sizes', {
      repository: 'octo/app',
      file_error: '',
      items: [
        ['build', 'normal', 'default', ''],
        ['integration-tests-with-a-long-name', 'heavy', 'override', 'heavy'],
        ['lint', 'light', 'measured', ''],
        ['e2e', 'heavy', 'scan', ''],
      ].map(([job, cls, source, over]) => ({
        repository: 'octo/app',
        job,
        class: cls,
        source,
        weight: { cpu: 2, memory: 4 },
        confidence: source === 'scan' ? 'low' : undefined,
        reasons: [],
        services: [],
        override_console: over,
        override_file: job === 'lint' ? 'light' : '',
        reset_note:
          job === 'e2e'
            ? 'The test count moved more than twice, so the size went back to the scan guess.'
            : '',
        copy_line: `${job}: ${cls}`,
      })),
      cost_table: [
        {
          kind: 'framework',
          key: 'playwright',
          class: 'heavy',
          cpu: 4,
          gib: 8,
          assumption: true,
        },
      ],
    })
    .on('GET', '/api/v1/runners', { items: [runner()] })
    .on('GET', '/api/v1/insights/waits', {
      days: 7,
      group: 'label',
      hold_cap_seconds: 120,
      rows: [
        ['crsp-e2e-7f3a', 240, 660],
        ['crsp-build-c4a7', 540, 1320],
        ['crsp-small-4c1d', 0, 120],
      ].map(([key, p50, p90]) => ({
        key,
        jobs: 12,
        wait: { total_seconds: 3600, p50_seconds: p50, p90_seconds: p90, known: 12 },
        hold: { total_seconds: 0, known: 0 },
        run: { total_seconds: 0, known: 0 },
      })),
    })
    .on('GET', '/api/v1/savings', {
      range: 'today',
      from: '2026-11-25T00:00:00Z',
      to: '2026-11-25T15:00:00Z',
      total: { key: 'all', jobs: 4, minutes: 15, money: 0.12, public_minutes: 5 },
      by_repository: [
        { key: 'octo/app', jobs: 3, minutes: 10, money: 0.12, public_minutes: 0 },
        { key: 'octo/public', jobs: 1, minutes: 5, money: 0, public_minutes: 5 },
      ],
      by_os: [{ key: 'linux', jobs: 4, minutes: 15, money: 0.12, public_minutes: 5 }],
      by_machine: [
        { key: 'standard', jobs: 4, minutes: 15, money: 0.12, public_minutes: 5 },
      ],
      not_priced: 0,
      note: 'Estimate. Jobs of public repositories count their minutes but save no money.',
      source_url: 'https://docs.github.com/en/billing/reference/actions-runner-pricing',
      copied_on: '2026-11-15',
      rates_changed: false,
    })
    .on('GET', '/api/v1/runners/:id', runner())
    .on('GET', '/api/v1/images', { items: [image()] })
    .on('GET', '/api/v1/images/:id', image())
    .on('GET', '/api/v1/caches', { items: [cache()] })
    .on('GET', '/api/v1/github/org-pools', {
      items: [
        {
          installation_id: 20,
          organization: 'acme',
          is_organization: true,
          eligible: true,
          enabled: false,
          group_name: 'crsp-home',
          repositories: [],
        },
      ],
    })
    .on('GET', '/api/v1/github/repositories', {
      items: [repository()],
      stale: { stale: false },
    })
    .on('GET', '/api/v1/users', {
      items: [
        owner,
        {
          id: 3,
          login: 'alice',
          role: 'admin',
          added_by: 'octo',
          added_at: '2026-11-03T09:00:00Z',
        },
      ],
    });
}

/** Repositories while "Enable for public repositories" is off: private, public, and a public one still on the pool (a Problem). */
export function publicRows() {
  const base = {
    installation_id: 1,
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
  };
  return {
    items: [
      {
        ...base,
        id: 1,
        full_name: 'octo/app',
        owner: 'octo',
        name: 'app',
        visibility: 'private',
        pool_on: true,
      },
      {
        ...base,
        id: 2,
        full_name: 'octo/site',
        owner: 'octo',
        name: 'site',
        visibility: 'public',
        pool_on: false,
      },
      {
        ...base,
        id: 3,
        full_name: 'octo/docs',
        owner: 'octo',
        name: 'docs',
        visibility: 'public',
        pool_on: true,
        problem: {
          code: 'public_pool_on_while_off',
          text: 'This public repository uses the pool while public repositories are off.',
          actions: [
            { id: 'turn_on_public_repositories', label: 'Turn on public repositories' },
            { id: 'turn_off_pool', label: 'Turn off the pool' },
          ],
        },
      },
    ],
    stale: { stale: false },
  };
}

/**
 * The jobs numbers of Analytics > Insights, shaped like the LOOK54 prototype (LOOK66): 4 jobs waiting, a slow end of 8
 * minutes, 93% passed, and three busy repositories. `route` adds them to a mock.
 */
export const insightsJobs = <M extends { on: (m: string, p: string, r: unknown) => M }>(
  mock: M,
): M => {
  const day = (back: number, jobs: number, failed: number, waitP90: number) => ({
    day: new Date(Date.now() - back * 86_400_000).toISOString().slice(0, 10),
    jobs,
    failed,
    wait_p50: 60,
    wait_p90: waitP90,
    run_p50: 300,
    run_p90: 600,
  });
  const group = (name: string, done: number) => ({
    name,
    filter: `repository=${name}`,
    done,
    running: 0,
    queued: 0,
    failed: 0,
    wait_p50: 60,
    wait_p90: 300,
    run_p50: 300,
    run_p90: 600,
    trend: [],
  });
  return mock
    .on('GET', '/api/v1/jobs/stats', {
      days: [
        ...[13, 12, 11, 10, 9, 8, 7].map((b) => day(b, 5, 1, 600)),
        ...[6, 5, 4, 3, 2, 1, 0].map((b) =>
          day(b, 6, b === 3 ? 1 : 0, b % 2 ? 480 : 420),
        ),
      ],
      hours: [],
      forecast_points: [],
    })
    .on('GET', '/api/v1/jobs', { items: [], total: 4 })
    .on('GET', '/api/v1/jobs/groups', {
      items: [group('octo/app', 78), group('octo/desktop', 34), group('octo/docs', 6)],
    });
};
