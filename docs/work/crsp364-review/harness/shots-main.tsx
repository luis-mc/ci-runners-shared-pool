import { StrictMode } from 'react';
import { createRoot } from 'react-dom/client';
import { MemoryRouter } from 'react-router-dom';
import { createApiClient } from '@crsp/contract';
import { App } from '../src/App';
import { FakeEventSource, apiError, machine, overview, repository, runner } from '../src/testing/mock';
import { job, jobDetail, jobsApi, runItem, runSummary } from '../src/testing/jobsData';
import { fullHealth, machineStats } from '../src/testing/machineHealthData';
import '@crsp/ui/ui.css';

const q = new URLSearchParams(location.search);
const startPath = q.get('path') ?? '/';
const load = (mem: number, cpu: number) => ({ memory_pct: mem, cpu_pct: cpu, memory_known: true, cpu_known: true, sampled_at: new Date().toISOString(), stale: false });
const machines = [
  machine({ id: 'm1', name: 'build-1', load: load(40, 20), slots_used: 1 }),
  machine({ id: 'm2', name: 'build-2', os: 'windows', load: load(78, 55), slots_used: 3 }),
  machine({ id: 'm3', name: 'mac-mini', os: 'macos', arch: 'arm64', load: load(93, 87), slots_used: 4 }),
];
const iso = (m: number) => new Date(Date.now() - m * 60000).toISOString();
const day = (n: number) => new Date(Date.now() - n * 86400000).toISOString().slice(0, 10);
const wave = (i: number, base: number, amp: number, ph = 0) => Math.max(0, Math.round(base + amp * Math.sin(i / 2.2 + ph) + ((i * 7) % 5)));

const days = Array.from({ length: 14 }, (_, i) => ({ day: day(13 - i), jobs: wave(i, 40, 15), failed: wave(i, 3, 2), wait_p50: wave(i, 20, 10), wait_p90: wave(i, 90, 40, 1), run_p50: wave(i, 240, 60, 2), run_p90: wave(i, 600, 120, 3) }));
const hours = Array.from({ length: 7 }, (_, w) => Array.from({ length: 24 }, (_, h) => ({ weekday: w, hour: h, count: h < 7 || h > 20 ? (h + w) % 3 : Math.round(2 + 14 * Math.sin(((h - 7) / 13) * Math.PI) * (w === 0 || w === 6 ? 0.3 : 1) + ((h * w) % 4)) }))).flat().filter((x) => x.count > 0);
const forecast_points = Array.from({ length: 30 }, (_, i) => { const f = 120 + i * 18; const a = Math.round(f * (0.7 + ((i * 37) % 60) / 100)); return { job_id: i + 1, forecast_seconds: f, actual_seconds: a, lo: Math.round(f * 0.8), hi: Math.round(f * 1.2), basis: 'history', note: `build ${i + 1}` }; });
const loadHist = { items: Array.from({ length: 48 }, (_, i) => ({ at: new Date(new Date().setHours(0, 0, 0, 0) + i * 1800000).toISOString(), cpu_pct: Math.round(35 + 30 * Math.sin(i / 5) + ((i * 11) % 12)), memory_pct: 50 + (i % 9) })) };

const sav = (key: string, n: number) => ({ key, jobs: n * 12, minutes: n * 340, money: n * 2.7, public_minutes: 0 });
const savings = { range: '7d', from: iso(7 * 1440), to: iso(0), total: sav('total', 9), by_repository: [sav('octo/app', 5), sav('octo/site', 3), sav('octo/lib', 1)], by_os: [sav('linux', 6), sav('windows', 2), sav('macos', 1)], by_machine: [sav('build-1', 5), sav('build-2', 3), sav('mac-mini', 1)], not_priced: 0, note: 'An estimate from GitHub hosted prices.', source_url: 'https://docs.github.com/billing', copied_on: '2026-11-01', rates_changed: false };
const wsplit = (key: string, p50: number, p90: number) => ({ key, jobs: 40, wait: { total_seconds: 4000, known: 40, p50_seconds: p50, p90_seconds: p90 }, hold: { total_seconds: 0, known: 0 }, run: { total_seconds: 9000, known: 40, p50_seconds: 200, p90_seconds: 500 } });
const waits = { days: 7, group: 'label', hold_cap_seconds: 120, rows: [wsplit('self-hosted', 20, 95), wsplit('linux', 12, 60), wsplit('gpu', 140, 600)] };
const tlRows = machines.flatMap((m) => [1, 2].map((slot) => ({ key: `${m.id}:${slot}`, machine_id: m.id, machine_name: m.name, slot, label: `${m.name} slot ${slot}` })));
const tlStates = ['done', 'done', 'failed', 'running', 'reserved', 'forecast'] as const;
const tlBars = tlRows.flatMap((r, ri) => Array.from({ length: 4 }, (_, k) => ({ id: `${r.key}-${k}`, row_id: r.key, state: tlStates[(ri + k) % tlStates.length], label: ['build', 'test', 'lint', 'e2e'][k], start: iso(300 - k * 70 - ri * 5), end: iso(300 - k * 70 - ri * 5 - -50), job_id: ri * 4 + k + 1 })));
const timeline = { now: iso(0), from: iso(360), to: iso(0), rows: tlRows, bars: tlBars, more_before: false };
const upd = q.get('upd') ?? 'idle';
const t0 = Date.now();
const base = { state: 'up_to_date', current: '0.1.28', available: null, required: false, policy: 'automatic_when_idle', window: null, next_check_at: iso(-60), failures: null, waiting_for_jobs: false, last_check: { at: iso(20), by: 'luis-mc', kind: 'manual' } };
const auto = { at: iso(35), by: 'updater', kind: 'automatic' };
const statuses: Record<string, Record<string, unknown>> = {
  idle: base,
  downloading: { ...base, state: 'downloading', available: '0.1.29', percent: 35, last_check: auto },
  installing: { ...base, state: 'installing', available: '0.1.29', percent: 60, last_check: auto },
  restarting: { ...base, state: 'installing', available: '0.1.29', percent: 90, last_check: auto },
  done: { ...base, state: 'updated', current: '0.1.29', last_check: auto },
  failed: { ...base, state: 'rolled_back', available: '0.1.29', failures: { version: '0.1.29', count: 1, retry_at: iso(-120) } },
};
const updateStatus = () => (upd === 'restarting' && Date.now() - t0 > 3000 ? apiError(503, 'unavailable', 'The service is restarting.') : statuses[upd]);

function api() {
  const jobs = [
    job({ id: 1, run_id: 100, state: 'running', result: undefined, job_name: 'build', started_at: iso(3), completed_at: undefined, run_seconds: undefined }),
    job({ id: 2, run_id: 101, state: 'completed', result: 'success', job_name: 'test', started_at: iso(30), completed_at: iso(24), run_seconds: 360 }),
    job({ id: 3, run_id: 102, state: 'completed', result: 'failure', job_name: 'e2e', started_at: iso(60), completed_at: iso(50), run_seconds: 600 }),
    job({ id: 4, run_id: 103, state: 'queued', result: undefined, job_name: 'lint', queued_at: iso(2), started_at: undefined, completed_at: undefined, run_seconds: undefined, wait_reason: 'free_slot', wait_text: 'waits for a free slot on build-1', queue_position: 1 }),
  ];
  const groups = ['octo/app', 'octo/site', 'octo/lib', 'octo/docs'].map((name, g) => ({ name, filter: `repository=${name}`, running: g % 2, queued: g === 0 ? 1 : 0, done: 120 - g * 25, failed: 4 + g, wait_p50: 20 + g * 9, wait_p90: 90 + g * 30, run_p50: 240, run_p90: 600, trend: Array.from({ length: 20 }, (_, i) => Math.round(200 + 80 * Math.sin(i / 2 + g) + ((i * 13) % 40))) }));
  const a = jobsApi({ groups: groups as never, jobs, stats: { days, hours, forecast_points } as never, runs: [runSummary({ id: 100 }), runSummary({ id: 101 })], run: runItem(), detail: jobDetail() });
  const kinds = [['persistent', 'persistent'], ['container', 'ephemeral'], ['VM', 'ephemeral']] as const;
  a.on('GET', '/api/v1/machines', { items: machines })
    .on('GET', '/api/v1/overview', overview({ machines, queued_count: 1 }))
    .on('GET', '/api/v1/machines/:machineId/load', loadHist)
    .on('GET', '/api/v1/machines/:machineId/job-stats', machineStats())
    .on('GET', '/api/v1/runners', { items: Array.from({ length: 9 }, (_, i) => runner({ id: `r${i}`, name: `crsp-${['linux', 'windows', 'macos'][i % 3]}-${i}`, machine_name: machines[i % 3]!.name, os: (['linux', 'windows', 'macos'] as const)[i % 3], isolation: kinds[i % 3]![0] as never, kind: kinds[i % 3]![1] as never, state: i % 4 === 0 ? ('busy' as never) : 'idle' })) })
    .on('GET', '/api/v1/runner-classes', { preset: 'standard', presets: ['standard', 'compact', 'memory-heavy', 'custom'], classes: [{ class: 'small', cpus: 2, memory_gib: 4 }, { class: 'xl', cpus: 16, memory_gib: 32 }], hints: [], unheld: [], reserve_after_passes: 3, reserve_after_minutes: 10 })
    .on('GET', '/api/v1/update/status', updateStatus)
    .on('GET', '/api/v1/update/history', [])
    .on('GET', '/api/v1/update/fleet', [])
    .on('GET', '/api/v1/github/app-health', { gone: false })
    .on('GET', '/api/v1/notification-channels', { items: [{ id: 'nch_1', kind: 'webhook', name: 'Team chat', scope: 'pool', enabled: true, events: ['machine.offline', 'job.waiting'], min_severity: 'warning', url: 'https://hooks.example.com/crsp/T000/xyz', secret_set: true, last_test_at: iso(300), last_test_ok: true }] })
    .on('GET', '/api/v1/settings/listen-address', { running: '127.0.0.1:8460', from_env: false, candidates: [] })
    .on('GET', '/api/v1/settings/metrics', { enabled: false, port: 9464, expose: false, key_required: false, push_enabled: false, push_endpoint: '', push_seconds: 60, key_made: false, running: false, pushing: false, scrape_config: 'scrape_configs: crsp' })
    .on('GET', '/api/v1/savings', savings)
    .on('GET', '/api/v1/timeline', timeline)
    .on('GET', '/api/v1/insights/waits', waits)
    .on('GET', '/api/v1/machines/:id', (_c, p) => machines.find((m) => m.id === p.id) ?? machines[0])
    .on('GET', '/api/v1/machines/:id/health', fullHealth())
    .on('GET', '/api/v1/repositories/:id/trial', { repository: 'octo/app', mode: 'shadow', variables: [], total: 0, hosted: { runs: 0, succeeded: 0, avg_wait_sec: 0, avg_run_sec: 0 }, crsp: { runs: 0, succeeded: 0, avg_wait_sec: 0, avg_run_sec: 0 }, jobs: [] })
    .on('GET', '/api/v1/repositories/:id', repository({ pool_on: true }))
    .on('GET', '/api/v1/github/repositories', { items: [repository({ pool_on: true })], stale: { stale: false } });
  return a;
}
const a = api();
const f = (async (input: Request) => {
  const r = await a.fetch(input);
  if (r.status === 404) console.warn('MISSING', new URL(input.url).pathname);
  return r;
}) as unknown as typeof fetch;
const client = createApiClient(location.origin, f);
createRoot(document.getElementById('root')!).render(
  <StrictMode><MemoryRouter initialEntries={[startPath]}><App client={client} eventSource={(u: string) => new FakeEventSource(u)} /></MemoryRouter></StrictMode>,
);
