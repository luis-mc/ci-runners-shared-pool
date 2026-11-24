// The insights API and the machine load, for the screens of Analytics (LOOK65): the screens call them through the generated client.
import type { Mock } from './fixtures';

export const at = (min: number) => new Date(Date.now() + min * 60_000).toISOString();

const finding = (id: number, over: Record<string, unknown> = {}) => ({
  id,
  kind: 'workflow.timeout',
  scope: 'repo',
  repository_id: 7,
  repository: 'octo/app',
  area: 'workflows',
  title: `Split the e2e job into ${id} shards`,
  fix: 'Change .github/workflows/ci.yml: shard e2e.',
  evidence: {},
  minutes_saved_week: 40 * id,
  first_seen: at(-3000),
  last_seen: at(-5),
  ...over,
});

const stats = (n: number) => ({
  total_seconds: n * 60,
  p50_seconds: n * 30,
  p95_seconds: n * 90,
});

export const insightsApi = (mock: Mock): Mock =>
  mock
    .on('GET', '/api/v1/insights/findings', {
      items: [finding(1, { action_kind: 'machine_slots' }), finding(2)],
      total: 2,
    })
    .on('GET', '/api/v1/insights/summary', {
      open: 2,
      minutes_saved_week: 120,
      enough_data: true,
    })
    .on('GET', '/api/v1/insights/digests', { items: [] })
    .on('GET', '/api/v1/jobs/stats', { days: [], hours: [], forecast_points: [] })
    .on('GET', '/api/v1/jobs/groups', { items: [] })
    .on('GET', '/api/v1/jobs', { items: [], total: 0, stale: { stale: false } })
    .on('GET', '/api/v1/insights/waits', {
      days: 7,
      group: 'job_key',
      hold_cap_seconds: 120,
      rows: [
        { key: 'octo/app build', jobs: 4, wait: stats(2), hold: stats(1), run: stats(3) },
      ],
    })
    .on('GET', '/api/v1/insights/timeline', {
      from: at(-360),
      to: at(0),
      lanes: [
        {
          runner_name: 'crsp-linux-1',
          origin: 'crsp',
          machine_name: 'build-1',
          segments: [
            {
              kind: 'job',
              start: at(-50),
              end: at(-30),
              repository: 'octo/app',
              job_ident: 'build',
            },
            { kind: 'idle', start: at(-30), end: at(-20) },
          ],
        },
      ],
    })
    .on('GET', '/api/v1/machines/:id/load', {
      items: [
        { at: at(-60), cpu_pct: 20, memory_pct: 30 },
        { at: at(-30), cpu_pct: 55, memory_pct: 40 },
      ],
    });
