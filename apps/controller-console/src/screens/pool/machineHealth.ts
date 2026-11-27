// The words of the machine health and job stats on Pool > Machines and the machine page (LOOK58c, machines R5.72 to
// R5.79). A value the API leaves out reads "unknown on <system>", never blank and never 0 (R5.72). The limits are the
// Doctor defaults (doctor R5.66): a finding is not made up here.
import type { Schemas } from '../../api/api';
import { formatDuration, formatNumber, formatPercent, formatSize } from '@crsp/ui';
import type { StatusKind } from '@crsp/ui';

type Health = Schemas['MachineHealth'];
type Volume = Schemas['HealthVolume'];
export type StatsSet = Schemas['JobStatsSet'];
export type StatsWindow = Schemas['MachineJobStats']['window'];

/** The Doctor defaults: a disk with less free space, or one that fills sooner, and a clock this far off. */
export const DISK_FREE_UNDER_PCT = 10;
export const DISK_DAYS_UNDER = 3;
export const CLOCK_SKEW_OVER_S = 30;

export const OS_NAME: Record<string, string> = {
  linux: 'Linux',
  windows: 'Windows',
  macos: 'macOS',
};

export const WINDOWS: readonly { value: StatsWindow; label: string }[] = [
  { value: '24h', label: '24 h' },
  { value: '7d', label: '7 days' },
];

export const WINDOW_WORDS: Record<StatsWindow, string> = {
  '24h': 'the last 24 hours',
  '7d': 'the last 7 days',
};

export const VOLUME_WORDS: Record<Volume['role'], string> = {
  system: 'system disk',
  data_dir: 'crsp data disk',
  docker: 'Docker disk',
  runner_work: 'runner work disk',
};

/** "unknown on Windows". */
export function unknownOn(os: string): string {
  return `unknown on ${OS_NAME[os] ?? os}`;
}

/** One number of the key list: its name, its words, and whether the API knew it. `why` is the API's reason. */
export interface Metric {
  id: string;
  label: string;
  value: string;
  known: boolean;
  why?: string | undefined;
}

function known(id: string, label: string, value: string): Metric {
  return { id, label, value, known: true };
}

function unknown(id: string, label: string, os: string, why?: string): Metric {
  return { id, label, value: unknownOn(os), known: false, why };
}

function one(n: number): string {
  return formatNumber(n, 1);
}

/** "3 days 4 h", "5 h 10 min", "42 s". */
export function uptimeWords(seconds: number): string {
  const days = Math.floor(seconds / 86_400);
  if (days < 1) return formatDuration(seconds);
  const hours = Math.floor((seconds % 86_400) / 3600);
  return `${days} ${days === 1 ? 'day' : 'days'}${hours ? ` ${hours} h` : ''}`;
}

function daysWords(days: number): string {
  if (days < 1) return 'less than a day';
  return `about ${days < 10 ? one(days) : formatNumber(days)} days`;
}

/** The volume with the least free space among those that gave a figure. */
export function lowestVolume(h: Health | undefined): Volume | undefined {
  const withFree = (h?.volumes ?? []).filter((v) => v.free_pct !== undefined);
  return withFree.reduce<Volume | undefined>(
    (low, v) => (low === undefined || (v.free_pct ?? 0) < (low.free_pct ?? 0) ? v : low),
    undefined,
  );
}

function soonestFull(h: Health | undefined): Volume | undefined {
  const withDays = (h?.volumes ?? []).filter((v) => v.days_until_full !== undefined);
  return withDays.reduce<Volume | undefined>(
    (low, v) =>
      low === undefined || (v.days_until_full ?? 0) < (low.days_until_full ?? 0)
        ? v
        : low,
    undefined,
  );
}

/** "Free space is not falling" and the like: the API's own note for a volume that has no days figure. */
function daysNote(h: Health | undefined): string | undefined {
  return (h?.volumes ?? []).find((v) => v.days_note)?.days_note;
}

/** What a machine says about itself in a word, for the Doctor limits: the size of a volume in words. */
export function volumeText(v: Volume, os: string): string {
  if (v.free_pct === undefined || v.free_bytes === undefined) return unknownOn(os);
  return `${formatPercent(v.free_pct)} free, ${formatSize(v.free_bytes)}${
    v.total_bytes !== undefined ? ` of ${formatSize(v.total_bytes)}` : ''
  }`;
}

export function dockerBytes(h: Health | undefined): number | undefined {
  const d = h?.docker_use;
  if (!d) return undefined;
  const parts = [
    d.images_bytes,
    d.build_cache_bytes,
    d.volumes_bytes,
    d.containers_bytes,
  ];
  if (parts.every((p) => p === undefined)) return undefined;
  return parts.reduce<number>((sum, p) => sum + (p ?? 0), 0);
}

export function skewWords(seconds: number): string {
  const s = Math.round(seconds);
  if (Math.abs(s) < 1) return 'in sync with the controller';
  return `${formatDuration(Math.abs(s))} ${s > 0 ? 'ahead of' : 'behind'} the controller`;
}

function versionsWords(h: Health | undefined): string | undefined {
  const v = h?.versions;
  if (!v) return undefined;
  const parts = [
    v.agent ? `agent ${v.agent}` : '',
    v.runner_app ? `runner app ${v.runner_app}` : '',
    v.docker ? `${v.docker_kind || 'Docker'} ${v.docker}` : '',
    v.wsl ? `WSL ${v.wsl}` : '',
  ].filter(Boolean);
  return parts.length ? parts.join(', ') : undefined;
}

function connectivityWords(h: Health | undefined): string | undefined {
  const c = h?.connectivity;
  if (!c) return undefined;
  const parts = [
    c.github?.reachable === undefined
      ? ''
      : c.github.reachable
        ? `reaches GitHub${c.github.latency_ms !== undefined ? ` in ${c.github.latency_ms} ms` : ''}`
        : 'cannot reach GitHub',
    c.dns?.ok === false ? 'DNS fails' : '',
    c.rtt_ms !== undefined ? `${c.rtt_ms} ms to the controller` : '',
  ].filter(Boolean);
  return parts.length ? parts.join(', ') : undefined;
}

/**
 * The key numbers of a machine, in the order the card shows them. `health` is undefined while it loads or when the
 * machine has not reported: every value then reads unknown, never blank.
 */
export function metricsOf(h: Health | undefined, os: string): Metric[] {
  const out: Metric[] = [];
  const boot = h?.boot;
  out.push(
    boot?.uptime_seconds !== undefined
      ? known('uptime', 'Uptime', uptimeWords(boot.uptime_seconds))
      : unknown('uptime', 'Uptime', os, boot?.unknown_reason),
  );
  const low = lowestVolume(h);
  out.push(
    low
      ? known(
          'disk',
          'Disk free',
          `${formatPercent(low.free_pct ?? 0)} on the ${VOLUME_WORDS[low.role]}`,
        )
      : unknown(
          'disk',
          'Disk free',
          os,
          h?.volumes.find((v) => v.unknown_reason)?.unknown_reason,
        ),
  );
  const soon = soonestFull(h);
  out.push(
    soon
      ? known(
          'days',
          'Days until full',
          `${daysWords(soon.days_until_full ?? 0)} (${VOLUME_WORDS[soon.role]})`,
        )
      : low
        ? known('days', 'Days until full', daysNote(h) ?? 'not enough samples yet')
        : unknown('days', 'Days until full', os),
  );
  const swap = h?.swap;
  out.push(
    swap?.total_bytes !== undefined && swap.used_bytes !== undefined
      ? known(
          'swap',
          'Swap',
          swap.total_bytes === 0
            ? 'none set up'
            : `${formatSize(swap.used_bytes)} of ${formatSize(swap.total_bytes)} used`,
        )
      : unknown('swap', 'Swap', os, swap?.unknown_reason),
  );
  const conn = connectivityWords(h);
  out.push(
    conn
      ? known('connectivity', 'Connectivity', conn)
      : unknown('connectivity', 'Connectivity', os),
  );
  const up = h?.updates;
  out.push(
    up?.restart_pending !== undefined
      ? known(
          'restart',
          'Restart pending',
          up.restart_pending
            ? up.restart_may_hit_jobs
              ? 'yes, and it may stop running jobs'
              : 'yes'
            : 'no',
        )
      : unknown('restart', 'Restart pending', os, up?.unknown_reason),
  );
  const pw = h?.power;
  out.push(
    pw?.on_battery !== undefined
      ? known('power', 'Power', pw.on_battery ? 'on battery' : 'on mains power')
      : unknown('power', 'Power', os, pw?.source_unknown_reason),
  );
  out.push(
    pw?.sleep_enabled !== undefined
      ? known(
          'sleep',
          'Sleep',
          pw.sleep_enabled ? 'can go to sleep' : 'never goes to sleep',
        )
      : unknown('sleep', 'Sleep', os, pw?.sleep_unknown_reason),
  );
  const skew = h?.connectivity?.clock_skew_seconds;
  out.push(
    skew !== undefined
      ? known('skew', 'Clock skew', skewWords(skew))
      : unknown('skew', 'Clock skew', os),
  );
  const versions = versionsWords(h);
  out.push(
    versions
      ? known('versions', 'Versions', versions)
      : unknown('versions', 'Versions', os),
  );
  const docker = dockerBytes(h);
  out.push(
    docker !== undefined
      ? known('docker', 'Docker disk', `${formatSize(docker)} in use`)
      : unknown('docker', 'Docker disk', os, h?.docker_use?.unknown_reason),
  );
  const t = h?.thermal;
  out.push(
    t?.max_temp_celsius !== undefined
      ? known(
          'temp',
          'Temperature',
          `${one(t.max_temp_celsius)} °C${t.throttled ? ', slowed by heat' : ''}`,
        )
      : unknown('temp', 'Temperature', os, t?.unknown_reason),
  );
  const l = h?.load;
  out.push(
    l?.load1 !== undefined
      ? known(
          'load',
          'Load (1, 5, 15 min)',
          [l.load1, l.load5, l.load15]
            .map((n) => (n === undefined ? '?' : one(n)))
            .join(', '),
        )
      : unknown('load', 'Load (1, 5, 15 min)', os, l?.load_unknown_reason),
  );
  return out;
}

export interface HealthBadge {
  kind: StatusKind;
  word: string;
}

/** The health of a machine as a few words, each a word, a glyph and a colour (UX-58). Worst first. */
export function badgesOf(h: Health | undefined): HealthBadge[] {
  if (!h || !h.reported) return [{ kind: 'info', word: 'health not reported yet' }];
  const out: HealthBadge[] = [];
  if (h.stale) out.push({ kind: 'waiting', word: 'report is old' });
  const low = lowestVolume(h);
  const soon = soonestFull(h);
  if (low && (low.free_pct ?? 100) < DISK_FREE_UNDER_PCT)
    out.push({ kind: 'failed', word: 'disk almost full' });
  else if (soon && (soon.days_until_full ?? Infinity) < DISK_DAYS_UNDER)
    out.push({ kind: 'failed', word: 'disk fills soon' });
  if (h.oom?.recent) out.push({ kind: 'failed', word: 'ran out of memory' });
  if (h.connectivity?.github?.reachable === false)
    out.push({ kind: 'failed', word: 'cannot reach GitHub' });
  if (h.connectivity?.dns?.ok === false) out.push({ kind: 'failed', word: 'DNS fails' });
  if (h.boot?.unexpected_restart)
    out.push({ kind: 'waiting', word: 'restarted unexpectedly' });
  if (h.updates?.restart_may_hit_jobs)
    out.push({ kind: 'waiting', word: 'restart may stop jobs' });
  else if (h.updates?.restart_pending)
    out.push({ kind: 'info', word: 'restart pending' });
  const skew = h.connectivity?.clock_skew_seconds;
  if (skew !== undefined && Math.abs(skew) > CLOCK_SKEW_OVER_S)
    out.push({ kind: 'waiting', word: 'clock is off' });
  if (h.versions?.agent_out_of_date)
    out.push({ kind: 'waiting', word: 'agent out of date' });
  if (h.versions?.runner_out_of_date)
    out.push({ kind: 'waiting', word: 'runner app out of date' });
  if (h.thermal?.throttled) out.push({ kind: 'waiting', word: 'slowed by heat' });
  if (h.power?.on_battery) out.push({ kind: 'waiting', word: 'on battery' });
  if (h.power?.sleep_enabled) out.push({ kind: 'waiting', word: 'can go to sleep' });
  return out.length ? out : [{ kind: 'ok', word: 'healthy' }];
}

/** Job times: "4 min 12 s", or "no jobs" when the API has no average (it is absent, never 0). */
export function averageWords(seconds: number | undefined): string {
  return seconds === undefined ? 'no jobs yet' : formatDuration(seconds);
}

export interface StatLine {
  id: string;
  label: string;
  value: string;
}

/** The counts of a stats set, as the lines of the summary. Zero is a real count here: the jobs were counted. */
export function statLines(s: StatsSet): StatLine[] {
  const c = s.counts;
  return [
    { id: 'succeeded', label: 'Succeeded', value: String(c.succeeded) },
    { id: 'failed', label: 'Failed', value: String(c.failed) },
    { id: 'cancelled', label: 'Cancelled', value: String(c.cancelled) },
    { id: 'timed_out', label: 'Timed out', value: String(c.timed_out) },
    { id: 'over_memory', label: 'Over memory', value: String(c.over_memory) },
    { id: 'stopped', label: 'Stopped by crsp', value: String(c.stopped) },
    { id: 'run', label: 'Average run time', value: averageWords(s.avg_run_seconds) },
    {
      id: 'wait',
      label: 'Average queue wait',
      value: averageWords(s.avg_queue_wait_seconds),
    },
    {
      id: 'busy',
      label: 'Busy',
      value:
        s.counts.total === 0 && s.busy_pct === undefined
          ? 'no jobs yet'
          : formatPercent(s.busy_pct ?? 0),
    },
  ];
}

/** One line for a runner: "12 jobs, 2 failed, average run 4 min, busy 35%". */
export function runnerLine(s: StatsSet | undefined): string {
  if (!s || s.counts.total === 0) return 'no jobs in this window';
  const failed = s.counts.failed + s.counts.timed_out + s.counts.over_memory;
  return [
    `${s.counts.total} ${s.counts.total === 1 ? 'job' : 'jobs'}`,
    `${failed} failed or cut short`,
    `average run ${averageWords(s.avg_run_seconds)}`,
    `busy ${formatPercent(s.busy_pct ?? 0)}`,
  ].join(', ');
}

export const FAILURE_CLASS_WORDS: Record<Schemas['JobStatsFailure']['class'], string> = {
  failed: 'failed',
  timed_out: 'timed out',
  over_memory: 'ran over memory',
  stopped: 'stopped by crsp',
};

export interface RunnerStatRow {
  id: string;
  name: string;
  stats: StatsSet | undefined;
}

/** The runners of a machine with their stats: the runners the list names, plus any runner that ran jobs on it. */
export function runnerRowsOf(
  runners: readonly { id: string; name: string }[],
  stats: Schemas['MachineJobStats'],
): RunnerStatRow[] {
  const byId = new Map(stats.runners.map((r) => [r.runner_id, r.stats]));
  return [
    ...runners.map((r) => ({ id: r.id, name: r.name, stats: byId.get(r.id) })),
    ...stats.runners
      .filter((r) => !runners.some((x) => x.id === r.runner_id))
      .map((r) => ({ id: r.runner_id, name: r.runner_name, stats: r.stats })),
  ];
}

/** A value the API may leave out: its words, or "unknown on <system>" with the API's reason. */
export function orUnknown(value: string | undefined, os: string, why?: string): string {
  if (value !== undefined) return value;
  return why ? `${unknownOn(os)}: ${why}` : unknownOn(os);
}
