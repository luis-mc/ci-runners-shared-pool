// Fixtures of the Jobs section for the component tests: jobs, runs, groups and chart data in the wire shape of the
// Jobs API, and a mocked API that answers them with the filters the real API takes.
import type { Schemas } from '../api/api';
import { signedIn, type MockApi } from './mock';

export type Job = Schemas['Job'];
export type RunItem = Schemas['RunItem'];
export type RunSummary = Schemas['RunSummary'];
export type JobDetail = Schemas['JobDetail'];

const STALE = { stale: false } as const;

/** An override: any field, and `undefined` to take one away. */
export type Over<T> = { [K in keyof T]?: T[K] | undefined };

export function job(over: Over<Job> = {}): Job {
  return {
    id: 1,
    repository: 'octo/app',
    repository_id: 7,
    run_id: 100,
    run_attempt: 1,
    github_job_id: 1,
    workflow: 'ci.yml',
    job_name: 'test',
    head_sha: 'a1b2c3d4e5f6',
    trigger: 'push',
    labels: ['self-hosted', 'linux'],
    state: 'done',
    result: 'success',
    machine_id: 'm1',
    backend: 'docker',
    created_at: '2026-11-18T09:00:00Z',
    queued_at: '2026-11-18T09:00:00Z',
    started_at: '2026-11-18T09:00:30Z',
    completed_at: '2026-11-18T09:05:00Z',
    github_wait_seconds: 30,
    run_seconds: 270,
    html_url: 'https://github.com/octo/app/actions/runs/100/job/1',
    job_ident: 'octo/app / ci.yml / test',
    node_id: 'test',
    matrix: [],
    branch: 'OPS34-jobs-section',
    task_id: 'OPS34',
    job_type: 'standard',
    needs: [],
    runner: {
      id: 'r1',
      name: 'runner-1',
      kind: 'persistent',
      machine_id: 'm1',
      machine_name: 'build-1',
      backend: 'docker',
      cache: 'warm',
    },
    forecast: { p50: 270, lo: 240, hi: 330, basis: 'job_key', samples: 20 },
    ...over,
  } as Job;
}

export function runSummary(over: Over<RunSummary> = {}): RunSummary {
  return {
    id: 100,
    attempt: 1,
    attempts: [1],
    repository: 'octo/app',
    repository_id: 7,
    workflow: 'ci.yml',
    branch: 'OPS34-jobs-section',
    head_sha: 'a1b2c3d4e5f6',
    task_id: 'OPS34',
    trigger: 'push',
    html_url: 'https://github.com/octo/app/actions/runs/100',
    state: 'done',
    result: 'success',
    created_at: '2026-11-18T09:00:00Z',
    completed_at: '2026-11-18T09:08:00Z',
    jobs_total: 3,
    jobs_done: 3,
    jobs_failed: 0,
    total_seconds: 480,
    overall_state: 'done',
    started_next: [],
    ...over,
  } as RunSummary;
}

/** A run of three jobs: lint, then test and build, which both need lint. */
export function runItem(over: Over<RunItem> = {}): RunItem {
  return {
    id: 100,
    attempt: 1,
    attempts: [1],
    repository: 'octo/app',
    repository_id: 7,
    workflow: 'ci.yml',
    branch: 'OPS34-jobs-section',
    head_sha: 'a1b2c3d4e5f6',
    task_id: 'OPS34',
    trigger: 'push',
    html_url: 'https://github.com/octo/app/actions/runs/100',
    state: 'done',
    result: 'success',
    created_at: '2026-11-18T09:00:00Z',
    completed_at: '2026-11-18T09:08:00Z',
    overall_state: 'done',
    started_next: [],
    jobs: [
      job({
        id: 11,
        job_name: 'lint',
        node_id: 'lint',
        queued_at: '2026-11-18T09:00:00Z',
        started_at: '2026-11-18T09:00:10Z',
        completed_at: '2026-11-18T09:01:00Z',
        run_seconds: 50,
      }),
      job({
        id: 12,
        job_name: 'test',
        node_id: 'test',
        needs: ['lint'],
        queued_at: '2026-11-18T09:01:00Z',
        started_at: '2026-11-18T09:01:10Z',
        completed_at: '2026-11-18T09:05:40Z',
        run_seconds: 270,
      }),
      job({
        id: 13,
        job_name: 'build [linux, arm64]',
        node_id: 'build [linux, arm64]',
        matrix: ['linux', 'arm64'],
        needs: ['lint'],
        queued_at: '2026-11-18T09:01:00Z',
        started_at: '2026-11-18T09:01:20Z',
        completed_at: '2026-11-18T09:08:00Z',
        run_seconds: 400,
      }),
    ],
    edges: [
      ['lint', 'test'],
      ['lint', 'build [linux, arm64]'],
    ],
    critical_path: ['lint', 'build [linux, arm64]'],
    graph: {
      source: 'workflow_file',
      dynamic_nodes: ['build [linux, arm64]'],
    },
    ...over,
  } as RunItem;
}

export function jobDetail(over: Over<JobDetail> = {}): JobDetail {
  return {
    ...job({ id: 12, job_name: 'test', node_id: 'test', worktree: undefined }),
    steps: [
      {
        number: 1,
        name: 'Set up job',
        state: 'done',
        result: 'success',
        started_at: '2026-11-18T09:00:30Z',
        completed_at: '2026-11-18T09:00:40Z',
        run_seconds: 10,
      },
      {
        number: 2,
        name: '<b>Run tests</b>',
        state: 'done',
        result: 'success',
        started_at: '2026-11-18T09:00:40Z',
        completed_at: '2026-11-18T09:04:50Z',
        run_seconds: 250,
        forecast: { p50: 240, lo: 200, hi: 280, basis: 'job_key', samples: 20 },
      },
    ],
    history: [
      { at: '2026-11-16T09:00:00Z', seconds: 250, result: 'success', branch: 'main' },
      { at: '2026-11-17T09:00:00Z', seconds: 280, result: 'success', branch: 'main' },
      { at: '2026-11-18T09:00:00Z', seconds: 270, result: 'failure', branch: 'x' },
    ],
    attempts: [{ attempt: 1, job_id: 12, result: 'success', state: 'done' }],
    ...over,
  } as JobDetail;
}

export interface JobsData {
  jobs?: Job[];
  runs?: RunSummary[];
  run?: RunItem;
  detail?: JobDetail;
  groups?: Schemas['JobGroupRow'][];
  stats?: Schemas['JobStats'];
  nextCursor?: string;
  total?: number;
}

/** A signed-in mock that answers the Jobs API from the given data and honours the filters the screens send. */
export function jobsApi(data: JobsData = {}): MockApi {
  const jobs = data.jobs ?? [];
  const runs = data.runs ?? [];
  const api = signedIn()
    .on('GET', '/api/v1/github/repositories', {
      items: [
        {
          id: 7,
          full_name: 'octo/app',
          owner: 'octo',
          name: 'app',
          installation_id: 1,
          visibility: 'private',
          pool_on: true,
          user_is_github_admin: true,
          user_admin_known: true,
          variables: [],
          policy: {
            priority: 10,
            max_parallel_jobs: 2,
            allow_fork_pull_requests: false,
            labels: [],
            runner_groups: [],
          },
        },
      ],
      stale: STALE,
    })
    .on('GET', '/api/v1/jobs/:jobId', () => data.detail ?? jobDetail())
    .on('GET', '/api/v1/runs/:runId', () => data.run ?? runItem())
    .on('GET', '/api/v1/jobs', (c) => {
      const state = c.query.get('state');
      const repo = c.query.get('repository_id');
      const items = jobs
        .filter((j) => !state || j.state === state)
        .filter((j) => !repo || String(j.repository_id) === repo);
      const limit = Number(c.query.get('limit') ?? 25);
      return {
        items: items.slice(0, limit),
        total: data.total ?? items.length,
        ...(items.length > limit || data.nextCursor
          ? { next_cursor: data.nextCursor ?? `j${limit}` }
          : {}),
        stale: STALE,
      };
    })
    .on('GET', '/api/v1/runs', (c) => {
      const state = c.query.get('state');
      const items = runs.filter((r) => !state || r.state === state);
      const limit = Number(c.query.get('limit') ?? 25);
      return {
        items: items.slice(0, limit),
        total: data.total ?? items.length,
        ...(items.length > limit ? { next_cursor: `r${limit}` } : {}),
        stale: STALE,
      };
    })
    .on('GET', '/api/v1/jobs/groups', () => ({ items: data.groups ?? [] }))
    .on(
      'GET',
      '/api/v1/jobs/stats',
      () => data.stats ?? { days: [], hours: [], forecast_points: [] },
    )
    .on(
      'GET',
      '/api/v1/exports/jobs',
      () => new Response('repository\n', { status: 200 }),
    )
    .on(
      'GET',
      '/api/v1/exports/job-summaries',
      () => new Response('day\n', { status: 200 }),
    );
  return api;
}
