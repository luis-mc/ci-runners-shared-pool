// Fixtures of the machine health and job stats answers (LOOK58c, machines R5.72 to R5.79).
import type { Schemas } from '../api/api';

type Health = Schemas['MachineHealth'];
type Stats = Schemas['JobStatsSet'];

/** A machine that reported everything. */
export function fullHealth(over: Partial<Health> = {}): Health {
  return {
    machine_id: 'm1',
    reported: true,
    received_at: '2026-11-22T10:00:00Z',
    stale: false,
    boot: { uptime_seconds: 3 * 86_400 + 4 * 3600, last_boot_at: '2026-11-19T06:00:00Z' },
    volumes: [
      {
        role: 'system',
        path: '/',
        total_bytes: 500_000_000_000,
        free_bytes: 200_000_000_000,
        free_pct: 40,
        days_note: 'free space is not falling',
      },
      {
        role: 'docker',
        path: '/var/lib/docker',
        total_bytes: 200_000_000_000,
        free_bytes: 50_000_000_000,
        free_pct: 25,
        days_until_full: 6.5,
      },
    ],
    swap: { total_bytes: 8_000_000_000, used_bytes: 1_000_000_000 },
    oom: { total_kills: 0 },
    connectivity: {
      rtt_ms: 12,
      last_heartbeat_at: '2026-11-22T09:59:30Z',
      reconnects_24h: 1,
      clock_skew_seconds: 2.4,
      github: { reachable: true, latency_ms: 80 },
      dns: { ok: true },
    },
    versions: {
      agent: '1.4.0',
      controller: '1.4.0',
      runner_app: '2.330.0',
      docker_kind: 'Docker',
      docker: '27.1',
    },
    updates: { restart_pending: true, restart_may_hit_jobs: false },
    power: { on_battery: false, sleep_enabled: false },
    docker_use: {
      images_bytes: 20_000_000_000,
      build_cache_bytes: 5_000_000_000,
      volumes_bytes: 1_000_000_000,
      containers_bytes: 500_000_000,
    },
    thermal: { max_temp_celsius: 61.5, throttled: false },
    load: { load1: 1.2, load5: 0.9, load15: 0.7 },
    ...over,
  };
}

/** A Windows machine that could not give most values: each says why. */
export function unknownHealth(): Health {
  return {
    machine_id: 'm1',
    reported: true,
    received_at: '2026-11-22T10:00:00Z',
    stale: false,
    boot: { unknown_reason: 'the boot time is not readable' },
    volumes: [
      { role: 'system', path: 'C:\\', unknown_reason: 'the volume did not answer' },
    ],
    swap: { unknown_reason: 'no page file counter' },
    updates: { unknown_reason: 'Windows Update did not answer' },
    power: {
      source_unknown_reason: 'no battery data',
      sleep_unknown_reason: 'powercfg failed',
    },
    docker_use: { unknown_reason: 'Docker is not running' },
    thermal: { unknown_reason: 'no sensor' },
    load: { load_unknown_reason: 'Windows has no load average' },
  };
}

export function statsSet(
  over: Partial<Stats> = {},
  omit: readonly (keyof Stats)[] = [],
): Stats {
  const out: Stats = {
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
      at: '2026-11-22T08:00:00Z',
    },
    top_failing: [
      {
        repository: 'octo/app',
        job: 'build',
        failures: 3,
        last_at: '2026-11-22T08:00:00Z',
      },
    ],
    ...over,
  };
  for (const k of omit) delete out[k as Exclude<keyof Stats, 'counts' | 'top_failing'>];
  return out;
}

export function machineStats(
  window: '24h' | '7d' = '24h',
  over: Partial<Schemas['MachineJobStats']> = {},
): Schemas['MachineJobStats'] {
  return {
    window,
    from: '2026-11-21T10:00:00Z',
    to: '2026-11-22T10:00:00Z',
    machine: statsSet(),
    runners: [{ runner_id: 'r1', runner_name: 'crsp-linux-1', stats: statsSet() }],
    ...over,
  };
}
