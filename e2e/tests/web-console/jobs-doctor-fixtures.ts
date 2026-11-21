// The routes of the jobs and doctor screens for the mocked controller API (CRSP35).
import type { Call, Mock } from './fixtures';

export const queuedJob = {
  id: 5,
  repository: 'octo/app',
  repository_id: 7,
  run_id: 101,
  run_attempt: 1,
  github_job_id: 5,
  workflow: 'ci.yml',
  job_name: 'e2e',
  labels: ['self-hosted', 'linux'],
  state: 'queued',
  wait_reason: 'free_slot',
  wait_text: 'waits for a free slot on any Linux machine',
  created_at: '2026-11-14T09:50:00Z',
  queued_at: '2026-11-14T09:51:00Z',
  head_sha: 'a1b2c3d4e5f6',
  trigger: 'push',
  branch: 'OPS34-jobs-section',
  task_id: 'OPS34',
  node_id: 'e2e',
  queue_position: 1,
  queue_eta: { p50: 300, lo: 240, hi: 420 },
  forecast: { p50: 270, lo: 240, hi: 330, basis: 'job_key', samples: 20 },
};

export const runningJob = {
  ...queuedJob,
  id: 6,
  run_id: 102,
  job_name: 'build',
  node_id: 'build',
  state: 'running',
  wait_reason: undefined,
  wait_text: undefined,
  queue_position: undefined,
  queue_eta: undefined,
  started_at: '2026-11-14T09:58:00Z',
};

export const doneJob = (id: number, over: Record<string, unknown> = {}) => ({
  id,
  repository: 'octo/app',
  repository_id: 7,
  run_id: 100 + id,
  run_attempt: 1,
  github_job_id: id,
  workflow: 'ci.yml',
  job_name: `test-${id}`,
  labels: ['self-hosted', 'linux'],
  state: 'done',
  result: 'success',
  machine_id: 'm1',
  backend: 'docker',
  created_at: '2026-11-13T09:00:00Z',
  started_at: '2026-11-13T09:00:30Z',
  completed_at: '2026-11-13T09:05:00Z',
  github_wait_seconds: 30,
  run_seconds: 270,
  html_url: `https://github.com/octo/app/actions/runs/${100 + id}/job/${id}`,
  head_sha: 'a1b2c3d4e5f6',
  trigger: 'push',
  branch: 'OPS34-jobs-section',
  task_id: 'OPS34',
  node_id: `test-${id}`,
  job_ident: `octo/app / ci.yml / test-${id}`,
  matrix: [],
  needs: [],
  forecast: { p50: 270, lo: 240, hi: 330, basis: 'job_key', samples: 20 },
  runner: {
    id: 'r1',
    name: 'runner-1',
    kind: 'persistent',
    machine_id: 'm1',
    machine_name: 'build-1',
    backend: 'docker',
    cache: 'warm',
  },
  ...over,
});

const runOf = (id: number) => ({
  id: 100 + id,
  attempt: 1,
  attempts: [1],
  repository: 'octo/app',
  repository_id: 7,
  workflow: 'ci.yml',
  branch: 'OPS34-jobs-section',
  head_sha: 'a1b2c3d4e5f6',
  task_id: 'OPS34',
  trigger: 'push',
  html_url: `https://github.com/octo/app/actions/runs/${100 + id}`,
  state: 'done',
  result: 'success',
  created_at: '2026-11-13T09:00:00Z',
  completed_at: '2026-11-13T09:05:00Z',
  jobs_total: 1,
  jobs_done: 1,
  jobs_failed: 0,
  total_seconds: 300,
});

const group = (name: string, filter: string, over: Record<string, unknown> = {}) => ({
  name,
  filter,
  running: 1,
  queued: 1,
  done: 3,
  failed: 0,
  wait_p50: 30,
  wait_p90: 60,
  run_p50: 270,
  run_p90: 330,
  trend: [250, 260, 270, 280],
  ...over,
});

export const finding = (over: Record<string, unknown> = {}) => ({
  check: 'workflow.labels',
  subject: { kind: 'repository', id: '7', name: 'octo/app' },
  severity: 'fail',
  title: 'No online runner carries [self-hosted, linux, arm64]',
  detail: '.github/workflows/ci.yml:12 needs [self-hosted, linux, arm64]',
  fix: { text: 'Add a runner with these labels.', link: '/pool/runners' },
  needs_admin: false,
  milestone: 1,
  checked_at: '2026-11-14T10:00:12Z',
  ...over,
});

/** The queue, a running job, a history of three jobs, their runs, the groups, the charts and a doctor with two findings. */
export function jobsAndDoctor(
  mock: Mock,
  history = [1, 2, 3].map((i) => doneJob(i)),
): Mock {
  const page = (items: unknown[], _call: Call) => ({
    items,
    total: items.length,
    stale: { stale: false },
  });
  return mock
    .on('GET', '/api/v1/jobs', (call: Call) => {
      const state = call.query.get('state');
      if (state === 'queued') return page([queuedJob], call);
      if (state === 'running') return page([runningJob], call);
      return page(history, call);
    })
    .on('GET', '/api/v1/jobs/:id', (_call: Call, params: Record<string, string>) => ({
      ...doneJob(Number(params.id) || 1),
      steps: [
        {
          number: 1,
          name: 'Set up job',
          state: 'done',
          result: 'success',
          started_at: '2026-11-13T09:00:30Z',
          completed_at: '2026-11-13T09:00:40Z',
          run_seconds: 10,
        },
      ],
      history: [
        { at: '2026-11-11T09:00:00Z', seconds: 250, result: 'success', branch: 'main' },
        { at: '2026-11-12T09:00:00Z', seconds: 280, result: 'success', branch: 'main' },
      ],
      attempts: [
        { attempt: 1, job_id: Number(params.id) || 1, result: 'success', state: 'done' },
      ],
    }))
    .on('GET', '/api/v1/runs', (call: Call) => {
      const state = call.query.get('state');
      if (state === 'queued') return page([], call);
      if (state === 'running')
        return page(
          [
            {
              ...runOf(2),
              state: 'running',
              result: undefined,
              completed_at: undefined,
              total_seconds: undefined,
              jobs_total: 2,
              jobs_done: 1,
              eta: { p50: 300, lo: 240, hi: 420 },
            },
          ],
          call,
        );
      return page(
        history.map((j) => runOf(Number(j.id))),
        call,
      );
    })
    .on('GET', '/api/v1/runs/:id', (_call: Call, params: Record<string, string>) => ({
      ...runOf(Number(params.id) || 1),
      overall_state: 'done',
      started_next: [],
      jobs: [doneJob(Number(params.id) || 1)],
      edges: [],
      critical_path: ['test-1'],
      graph: { source: 'workflow_file', dynamic_nodes: [] },
    }))
    .on('GET', '/api/v1/jobs/groups', (call: Call) => {
      const by = call.query.get('by');
      const rows: Record<string, unknown[]> = {
        repository: [group('octo/app', 'repository=octo/app')],
        machine: [group('build-1', 'machine=m1')],
        runner: [group('runner-1', 'runner=runner-1')],
        'runner-type': [
          group('persistent/docker', 'runner_kind=persistent&backend=docker'),
        ],
        'job-type': [group('test', 'job_type=test', { light: false })],
      };
      return { items: rows[by ?? 'repository'] ?? [] };
    })
    .on('GET', '/api/v1/jobs/stats', {
      days: [
        {
          day: '2026-11-12',
          jobs: 3,
          failed: 0,
          wait_p50: 30,
          wait_p90: 50,
          run_p50: 270,
          run_p90: 330,
        },
        {
          day: '2026-11-13',
          jobs: 4,
          failed: 1,
          wait_p50: 20,
          wait_p90: 40,
          run_p50: 260,
          run_p90: 320,
        },
      ],
      hours: [{ weekday: 3, hour: 9, count: 4 }],
      forecast_points: [
        {
          job_id: 1,
          forecast_seconds: 270,
          actual_seconds: 280,
          lo: 240,
          hi: 330,
          basis: 'job_key',
        },
      ],
    })
    .on('GET', '/api/v1/doctor/findings', {
      items: [
        finding(),
        finding({
          check: 'machine.online',
          severity: 'warn',
          title: 'build-2 is offline',
          detail: undefined,
          subject: { kind: 'machine', id: 'm2', name: 'build-2' },
          fix: { text: 'Start the agent.', command: 'crsp agent start' },
        }),
      ],
      last_run_at: '2026-11-14T10:00:12Z',
    });
}
