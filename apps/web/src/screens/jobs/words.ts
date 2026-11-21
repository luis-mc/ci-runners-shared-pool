// The words of the Jobs section: statuses, durations, ranges, forecasts and their basis (design system content.md,
// "Jobs section words"). One place, so a table, a fact and a chart say the same thing.
import { formatDuration, type StatusKind } from '@crsp/ui';
import type { Schemas } from '../../api/api';

export type Job = Schemas['Job'];
export type Forecast = Schemas['Forecast'];
export type Eta = Schemas['Eta'];

const REASON_WORDS: Record<Schemas['WaitReason'], string> = {
  free_slot: 'waits for a free slot',
  load_over_budget: 'waits until the machine load is under its budget',
  no_runner_with_labels: 'waits for a runner with its labels',
  repository_parallel_limit: "waits for the repository's parallel limit",
  machine_drained: 'waits because its machine is not taking jobs',
  load_not_measured: 'waits because the machine load is not measured yet',
  paused: 'waits because its machine is paused',
  on_battery: 'waits because its machine runs on battery',
  unknown: 'waits, the reason is not known yet',
};

/** Why a queued job waits, in words (UX-29): the server's line when it has one, else the reason's. */
export function waitWords(j: Pick<Job, 'wait_reason' | 'wait_text'>): string {
  if (j.wait_text) return j.wait_text;
  return j.wait_reason ? REASON_WORDS[j.wait_reason] : 'waits to be placed';
}

const REASON_SHORT: Record<Schemas['WaitReason'], string> = {
  free_slot: 'waits for a slot',
  load_over_budget: 'machine load too high',
  no_runner_with_labels: 'no runner with its labels',
  repository_parallel_limit: 'repository limit',
  machine_drained: 'machine not taking jobs',
  load_not_measured: 'load not measured yet',
  paused: 'machine paused',
  on_battery: 'machine on battery',
  unknown: 'reason not known yet',
};

/**
 * The short form of a wait reason for a one-line row (UX-72): a few words, the full line is under the Why button.
 * A line the server sent without a reason has none (the table shortens it at its first clause).
 */
export function waitShort(j: Pick<Job, 'wait_reason' | 'wait_text'>): string | undefined {
  if (j.wait_reason) return REASON_SHORT[j.wait_reason];
  return j.wait_text ? undefined : 'waits to be placed';
}

/** The workflow file name of a path (`.github/workflows/ci.yml` is `ci.yml`): the value of a one-line row (CRSP210). */
export function workflowFile(path: string): string {
  const name = path.split('/').filter(Boolean).pop();
  return name ?? path;
}

/** UX-58: the result as a word, a glyph and a colour. */
export function jobStatus(j: {
  state: Job['state'];
  result?: Job['result'] | undefined;
}): {
  kind: StatusKind;
  word: string;
} {
  if (j.state === 'queued') return { kind: 'waiting', word: 'waiting' };
  if (j.state === 'running') return { kind: 'running', word: 'running' };
  switch (j.result) {
    case 'success':
      return { kind: 'ok', word: 'success' };
    case 'failure':
      return { kind: 'failed', word: 'failure' };
    case 'timed_out':
      return { kind: 'failed', word: 'timed out' };
    case 'lost':
      return { kind: 'failed', word: 'lost' };
    case 'cancelled':
      return { kind: 'off', word: 'cancelled' };
    default:
      return { kind: 'info', word: 'done' };
  }
}

export type FollowUp = Schemas['RunFollowUp'];

/** The word a run shows while a run it started next is queued or running (CIX24). */
export const FOLLOW_UP_RUNNING = 'follow-up running';

/** "Installers, running": a run that another run started, and where it stands. */
export function followUpWords(
  f: Pick<FollowUp, 'workflow_name' | 'state' | 'result'>,
): string {
  return `${f.workflow_name}, ${jobStatus({ state: f.state, result: f.result }).word}`;
}

/** The runs a run started next, one line: "Installers, running" or "Installers, success; Docs, running". */
export function startedNextWords(list: readonly FollowUp[] | undefined): string {
  return (list ?? []).map(followUpWords).join('; ');
}

/** The forecast of a job; a job the controller has not forecast yet counts as "not enough history yet". */
export const NO_FORECAST_VALUE: Forecast = {
  p50: 0,
  lo: 0,
  hi: 0,
  basis: 'not_enough_history',
  samples: 0,
};
export const forecastOf = (j: Pick<Job, 'forecast'>): Forecast =>
  j.forecast ?? NO_FORECAST_VALUE;

/** "about 6 to 9 min", "about 40 to 55 s": a range in seconds, rounded for reading. */
export function rangeWords(lo: number, hi: number): string {
  if (hi < 90) return `about ${Math.round(lo)} to ${Math.round(hi)} s`;
  const m = (s: number) => Math.max(1, Math.round(s / 60));
  const a = m(lo);
  const b = m(hi);
  if (hi >= 3600) return `about ${formatDuration(lo)} to ${formatDuration(hi)}`;
  return a === b ? `about ${a} min` : `about ${a} to ${b} min`;
}

export const NO_FORECAST = 'not enough history yet';

/** One number for a table cell: "about 40 s", "about 7 min", "about 1 h 5 min" (owner decision 4). */
export function oneNumber(seconds: number): string {
  if (seconds < 60) return `about ${Math.max(5, Math.round(seconds / 5) * 5)} s`;
  if (seconds < 120) return `about ${Math.round(seconds / 10) * 10} s`;
  if (seconds < 3600) return `about ${Math.round(seconds / 60)} min`;
  return `about ${formatDuration(Math.round(seconds / 300) * 300)}`;
}

/** "the workflow's typical time", "the repository's typical time": the labelled stand-in under 5 runs (decision 7). */
function typicalLabel(f: Forecast): string {
  return f.basis === 'workflow'
    ? "the workflow's typical time"
    : "the repository's typical time";
}

/**
 * The forecast of a job or a step as a RANGE, for the Job and Run pages. Under 5 runs of the job itself it says so
 * and names the workflow's or the repository's typical time, labelled; with nothing at all, only the first part.
 */
export function forecastWords(f: Forecast): string {
  if (f.basis === 'not_enough_history') return NO_FORECAST;
  if (f.basis === 'job_key') return rangeWords(f.lo, f.hi);
  return `${NO_FORECAST}; ${typicalLabel(f)} is ${rangeWords(f.lo, f.hi)}`;
}

/** The forecast as ONE NUMBER, for a table cell. */
export function forecastOne(f: Forecast): string {
  if (f.basis === 'not_enough_history') return NO_FORECAST;
  if (f.basis === 'job_key') return oneNumber(f.p50);
  return `${NO_FORECAST} (${f.basis === 'workflow' ? 'workflow' : 'repository'} typical ${oneNumber(f.p50)})`;
}

export function ordinal(n: number): string {
  const rest = n % 100;
  if (rest >= 11 && rest <= 13) return `${n}th`;
  return `${n}${({ 1: 'st', 2: 'nd', 3: 'rd' } as Record<number, string>)[n % 10] ?? 'th'}`;
}

/** The start range of a queued job in minutes: "in about 4 to 7 min", "now". */
export function startRange(e: Eta): string {
  if (e.p50 < 20) return 'now';
  const m = (s: number) => Math.max(1, Math.round(s / 60));
  const a = m(e.lo);
  const b = m(e.hi);
  return a === b ? `in about ${a} min` : `in about ${a} to ${b} min`;
}

/** "starts in about 4 to 7 min, 3rd in line" (decision 8). */
export function startsWords(e: Eta | undefined, place: number | undefined): string {
  if (!e) return 'start not known';
  return `starts ${startRange(e)}${place ? `, ${ordinal(place)} in line` : ''}`;
}

/** The task id at the start of a branch name ("RES15-jobs-section" gives "RES15"); none when it does not start with one. */
export function taskIdOf(branch: string): string | undefined {
  return (
    /^([A-Z][A-Z0-9]*-\d+)(?=-|$)/.exec(branch)?.[1] ??
    /^([A-Z]+\d+)(?=-|$)/.exec(branch)?.[1]
  );
}

/** The task of a run or a job: the id the controller read from the branch, else none. */
export function taskOf(x: { task_id?: string | undefined }): string {
  return x.task_id ?? 'none in the branch name';
}

/** The commit and the event word, never the pull request number (the facts list it on its own line): "a1b2c3d, push". */
export function sourceWords(r: {
  head_sha?: string | undefined;
  trigger?: string | undefined;
}): string {
  const sha = r.head_sha ? r.head_sha.slice(0, 7) : 'no commit';
  return `${sha}, ${r.trigger ?? 'push'}`;
}

/** The commit of a run: its first 7 characters. */
export function commitWords(r: { head_sha?: string | undefined }): string {
  return r.head_sha ? r.head_sha.slice(0, 7) : 'no commit';
}

/** What started a run: a pull request, or the trigger (push, schedule). */
export function eventWords(r: {
  trigger?: string | undefined;
  pull_request?: number | undefined;
}): string {
  return r.pull_request ? `PR #${r.pull_request}` : (r.trigger ?? 'push');
}

/** What is kept and for how long (owner decision 2), said in the Done, Charts and group footers. */
export const RETENTION_WORDS =
  'crsp keeps steps for 30 days, jobs for 90 days and daily summaries for 400 days.';

/** Where the forecast comes from, in words ("median of the last 30 runs on linux-box"). */
export function basisWords(f: Forecast): string {
  const tail = f.changed_lately ? ', changed lately, so the last 10 runs count' : '';
  switch (f.basis) {
    case 'job_key':
      return `median of the last ${f.samples} runs on ${f.machine ?? 'all machines'}${tail}`;
    case 'workflow':
      return `median of the last ${f.samples} runs of the jobs of this workflow${tail}`;
    case 'repository':
      return `median of the last ${f.samples} runs of this repository${tail}`;
    default:
      return `${f.samples} of 5 runs needed, so no number is made up`;
  }
}

export function etaWords(e: Eta): string {
  return e.p50 < 20 ? 'now' : `in ${rangeWords(e.lo, e.hi)}`;
}

/** An ETA as one number for a table: "in about 7 min". */
export function etaOne(e: Eta): string {
  return e.p50 < 20 ? 'now' : `in ${oneNumber(e.p50)}`;
}

export function secondsOrNone(n: number | undefined): string {
  return n === undefined ? 'not yet' : formatDuration(n);
}

/** Seconds a job waited: the time since it queued while it waits, the recorded wait once it has started. */
export function waitedSeconds(j: Job, now: number): number | undefined {
  if (j.state === 'queued')
    return j.queued_at ? Math.max(0, (now - Date.parse(j.queued_at)) / 1000) : undefined;
  return j.github_wait_seconds;
}

/** Seconds a job ran: so far while it runs, the recorded time once it is done. */
export function ranSeconds(j: Job, now: number): number | undefined {
  if (j.state === 'running' && j.started_at)
    return Math.max(0, (now - Date.parse(j.started_at)) / 1000);
  return j.run_seconds;
}

export const RUNNER_KIND_WORDS = {
  persistent: 'persistent, state kept between jobs',
  ephemeral: 'ephemeral, fresh for every job',
} as const;

/** "owner/name" to the address of the repository page. */
export function repoPath(full: string): string {
  return `/repositories/${full}`;
}

/** The median of a list of numbers; 0 for none. */
export function median(xs: readonly number[]): number {
  if (xs.length === 0) return 0;
  const s = [...xs].sort((a, b) => a - b);
  const mid = Math.floor(s.length / 2);
  return s.length % 2
    ? (s[mid] as number)
    : ((s[mid - 1] as number) + (s[mid] as number)) / 2;
}

/** The share of finished jobs that ran inside their forecast range, 0 to 1. */
export function insideRange(
  points: readonly { actual: number; lo: number; hi: number }[],
): number {
  if (points.length === 0) return 0;
  return (
    points.filter((p) => p.actual >= p.lo && p.actual <= p.hi).length / points.length
  );
}
