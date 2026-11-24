// Mocked answers of the insights API (CIX22). The join of CIX21 (the API) deletes this file and points api.ts at
// the generated client. Every value has the wire shape of packages/contract/openapi.yaml.
import type { Schemas } from '../../api/api';

type S = Schemas;

const DAY = 86_400_000;
const ago = (ms: number): string => new Date(Date.now() - ms).toISOString();

export const MOCK_FINDINGS: S['InsightFinding'][] = [
  {
    id: 1,
    kind: 'pool.size',
    scope: 'pool',
    subject: 'self-hosted,linux',
    area: 'pool',
    title: 'Jobs wait a median of 4 min for a slot on the linux pool',
    fix: 'Add 1 job slot to linux-box. The p95 wait would fall from about 9 min to about 3 min.',
    evidence: { slots: 4, utilisation: 0.91, p95_wait_seconds: 540 },
    minutes_saved_week: 190,
    action_kind: 'machine_slots',
    first_seen: ago(5 * DAY),
    last_seen: ago(3_600_000),
  },
  {
    id: 2,
    kind: 'workflow.timeout',
    scope: 'repo',
    repository_id: 7,
    repository: 'octo/app',
    subject: '.github/workflows/ci.yml',
    area: 'workflows',
    title: 'Jobs in ci.yml have no timeout',
    fix: 'Give each job a timeout-minutes so a stuck job frees its slot.',
    snippet: 'jobs:\n  test:\n    timeout-minutes: 15\n',
    evidence: { workflow: 'ci.yml', jobs: ['test', 'lint'], line: 12 },
    minutes_saved_week: 45,
    first_seen: ago(9 * DAY),
    last_seen: ago(7_200_000),
  },
  {
    id: 3,
    kind: 'guardrail.g1',
    scope: 'repo',
    repository_id: 8,
    repository: 'octo/api',
    subject: 'unit',
    area: 'guardrails',
    title: '3 unit tests run over their limit',
    fix: 'Make the slowest tests faster or move them to the integration suite.',
    evidence: { tests: 3, limit_seconds: 10 },
    minutes_saved_week: 12,
    guardrail: 'G1',
    first_seen: ago(2 * DAY),
    last_seen: ago(3_600_000),
  },
  {
    id: 4,
    kind: 'repo.slots',
    scope: 'repo',
    repository_id: 7,
    repository: 'octo/app',
    subject: 'max_parallel_jobs',
    area: 'pool',
    title: 'octo/app waited for its parallel limit while slots were free',
    fix: 'Raise the parallel jobs limit of octo/app from 2 to 3.',
    evidence: { limit: 2, free_slots_median: 2 },
    minutes_saved_week: 30,
    action_kind: 'repo_parallel_limit',
    first_seen: ago(4 * DAY),
    last_seen: ago(3_600_000),
  },
];

export function mockFindings(opts: {
  repository_id?: number | undefined;
}): S['InsightFindingList'] {
  const items = MOCK_FINDINGS.filter(
    (f) => opts.repository_id === undefined || f.repository_id === opts.repository_id,
  );
  return { items, total: items.length };
}

export const mockSummary = (): S['InsightSummary'] => ({
  open: MOCK_FINDINGS.length,
  minutes_saved_week: MOCK_FINDINGS.reduce((n, f) => n + f.minutes_saved_week, 0),
  enough_data: true,
});

export const mockDigests = (): S['InsightDigestList'] => ({
  items: [
    {
      week: '2026-W46',
      written_at: ago(2 * DAY),
      text: 'Four things to do next, about 277 min a week.\nThe biggest: add 1 job slot to linux-box.',
    },
  ],
});

export function mockPlan(findingId: number): S['InsightPlan'] {
  const repo = findingId === 4;
  return {
    id: `plan-${findingId}`,
    finding_id: findingId,
    action_kind: repo ? 'repo_parallel_limit' : 'machine_slots',
    state: 'draft',
    changes: repo
      ? [
          {
            target: 'octo/app',
            field: 'max_parallel_jobs',
            from: 2,
            to: 3,
            words: 'Raise the parallel jobs limit of octo/app from 2 to 3.',
          },
        ]
      : [
          {
            target: 'm1',
            target_name: 'linux-box',
            field: 'job_slots',
            from: 4,
            to: 5,
            words: 'Raise the job slots of linux-box from 4 to 5.',
          },
        ],
    results: [],
    estimate: 'p95 wait from about 9 to about 3 min',
    created_at: new Date().toISOString(),
  };
}

export const mockSizing = (days: number): S['PoolSizing'] => ({
  days,
  method: 'Allen-Cunneen with Sakasegawa',
  label_sets: [
    {
      labels: ['self-hosted', 'linux'],
      slots: 4,
      utilisation: 0.91,
      jobs_per_hour: 18,
      p50_wait_seconds: 240,
      p95_wait_seconds: 540,
      estimates: [
        {
          extra_slots: 1,
          p95_wait_seconds: 180,
          mean_wait_seconds: 60,
          utilisation: 0.73,
        },
        {
          extra_slots: 2,
          p95_wait_seconds: 90,
          mean_wait_seconds: 30,
          utilisation: 0.61,
        },
      ],
      p95_wait_by_day: [
        { day: new Date(Date.now() - 2 * DAY).toISOString().slice(0, 10), value: 480 },
        { day: new Date(Date.now() - DAY).toISOString().slice(0, 10), value: 540 },
      ],
    },
  ],
});

export function mockTests(view: string): S['TestInsightList'] {
  const row = (n: number): S['TestInsight'] => ({
    test_id: `t${n}`,
    name: `handles case ${n}`,
    repository: 'octo/api',
    job_idents: ['ci.yml / test'],
    kind: 'unit',
    runs: 40,
    failures: view === 'flaky' ? 4 : 0,
    p50_ms: 800 + n * 100,
    p95_ms: 12_000 + n * 100,
    ...(view === 'slowest' ? { over_limit_seconds: 10 } : {}),
    last_seen: ago(3_600_000),
  });
  return { items: [row(1), row(2)], total: 2, reports_found: true };
}

export const mockGuardrails = (): S['GuardrailStatusList'] => ({
  items: [
    {
      repository_id: 8,
      repository: 'octo/api',
      guardrails: (['G1', 'G2', 'G3', 'G4', 'G5', 'G6', 'G7'] as const).map((g, i) => ({
        guardrail: g,
        state:
          i === 0
            ? ('break' as const)
            : i === 6
              ? ('unknown' as const)
              : ('pass' as const),
        ...(i === 0 ? { count: 3 } : {}),
        source: 'guard_report' as const,
        reported_at: ago(DAY),
      })),
      known_count_by_day: Array.from({ length: 5 }, (_, i) => ({
        day: new Date(Date.now() - (4 - i) * DAY).toISOString().slice(0, 10),
        value: 9 - i,
      })),
    },
  ],
});

export const mockSavings = (): S['RunSavings'] => ({
  savings: [
    {
      minutes: 4,
      words: 'The run would end 4 min sooner if build started as soon as it was queued.',
      job: 'build',
      change: 'no_wait',
    },
  ],
  waste: [],
});

export const mockResources = (): S['JobResources'] => ({
  samples: [0, 1, 2, 3].map((i) => ({
    at: new Date(Date.parse('2026-11-18T09:00:30Z') + i * 60_000).toISOString(),
    cpu_cores: 1 + i * 0.5,
    memory_bytes: (1 + i) * 500_000_000,
    known: true,
  })),
  cpu_p95_cores: 2.5,
  cpu_peak_cores: 2.5,
  memory_p95_bytes: 2_000_000_000,
  memory_peak_bytes: 2_000_000_000,
  measured: {
    peak_cpu_cores: 2.4,
    peak_memory_bytes: 1_900_000_000,
    sample_count: 240,
    duration_seconds: 240,
    oom_killed: false,
    cut_short: false,
    good: true,
    source: 'cgroup',
    key: {
      runs: 12,
      cpu_p90_cores: 2.6,
      memory_p90_bytes: 2_100_000_000,
      measured: true,
    },
  },
});

export const mockChecks = (): S['WorkflowCheckList'] => ({
  checked_at: ago(3_600_000),
  items: [
    {
      workflow: 'ci.yml',
      check_id: 'workflow.timeout',
      job: 'test',
      status: 'fail',
      line: 12,
      detail: 'The job has no timeout-minutes.',
    },
    { workflow: 'ci.yml', check_id: 'workflow.cache', job: 'build', status: 'ok' },
  ],
});
