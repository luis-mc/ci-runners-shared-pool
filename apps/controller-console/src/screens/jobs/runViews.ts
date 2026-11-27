// Turns a run and its jobs into the rows and nodes of the shared visual parts (Waterfall, JobGraph).
import {
  formatDuration,
  type JobGraphNode,
  type StatusKind,
  type WaterfallRow,
} from '@crsp/ui';
import type { Schemas } from '../../api/api';
import {
  forecastOf,
  forecastWords,
  jobStatus,
  ranSeconds,
  waitedSeconds,
  type Eta,
  type Forecast,
  type Job,
} from './words';

type RunItem = Schemas['RunItem'];
type StepItem = Schemas['StepItem'];

/** The id of a job inside its run (job name and matrix values); the graph edges use it. */
export const nodeId = (j: Job): string => j.node_id ?? String(j.id);
const needsOf = (j: Job): readonly string[] => j.needs ?? [];

export function stepStatus(s: StepItem): { kind: StatusKind; word: string } {
  if (s.state === 'running') return { kind: 'running', word: 'running' };
  if (s.state === 'queued') return { kind: 'waiting', word: 'waiting' };
  if (s.state === 'skipped' || s.result === 'skipped')
    return { kind: 'off', word: 'skipped' };
  if (s.result === 'failure') return { kind: 'failed', word: 'failure' };
  if (s.result === 'cancelled') return { kind: 'off', word: 'cancelled' };
  return { kind: 'ok', word: 'success' };
}

const sec = (ms: number) => Math.round(ms / 1000);

/** The steps of one job as waterfall rows, in seconds from the job's start (or from now for a job not started). */
export function stepRows(
  job: Job,
  steps: readonly StepItem[],
  now: number,
): { rows: WaterfallRow[]; nowSec: number | undefined } {
  const origin = job.started_at ? Date.parse(job.started_at) : now;
  const nowSec = job.state === 'running' ? sec(now - origin) : undefined;
  let cursor = 0;
  const rows = steps.map((s): WaterfallRow => {
    const st = stepStatus(s);
    const f = s.forecast;
    let start: number;
    let end: number;
    if (s.started_at) {
      start = sec(Date.parse(s.started_at) - origin);
      end = s.completed_at ? sec(Date.parse(s.completed_at) - origin) : sec(now - origin);
    } else {
      start = cursor;
      end = cursor;
    }
    cursor =
      Math.max(cursor, end) +
      (!s.started_at && f && f.basis !== 'not_enough_history' ? f.p50 : 0);
    const bracket =
      f && f.basis !== 'not_enough_history'
        ? { loSec: start + f.lo, hiSec: start + f.hi }
        : undefined;
    const took =
      s.run_seconds !== undefined
        ? `${st.word}, ${formatDuration(s.run_seconds)}`
        : st.word;
    return {
      id: `step-${s.number}`,
      label: `${s.number}. ${s.name}`,
      status: st.kind,
      startSec: start,
      endSec: end,
      ...(bracket ? { forecast: bracket } : {}),
      detail: `${took}, forecast ${f ? forecastWords(f) : 'not available'}`,
    };
  });
  return { rows, nowSec };
}

/** Stage of each node: 0 with no needs, else one more than the deepest need. */
export function stages(jobs: readonly Job[]): Map<string, number> {
  const byId = new Map(jobs.map((j) => [nodeId(j), j]));
  const depth = new Map<string, number>();
  const visit = (id: string, seen: Set<string>): number => {
    const known = depth.get(id);
    if (known !== undefined) return known;
    const j = byId.get(id);
    if (!j || seen.has(id)) return 0;
    seen.add(id);
    const d = needsOf(j)
      .filter((n) => byId.has(n))
      .reduce((m, n) => Math.max(m, visit(n, seen) + 1), 0);
    depth.set(id, d);
    return d;
  };
  for (const j of jobs) visit(nodeId(j), new Set());
  return depth;
}

/** The estimate a not yet finished job adds to the time line; a job with no forecast adds nothing (none is made up). */
function etaOf(f: Forecast): Eta {
  return { p50: f.p50, lo: f.lo, hi: f.hi };
}

/** The arrows of the graph as pairs of node ids. */
export function edgesOf(run: RunItem): [string, string][] {
  return run.edges.flatMap((e) =>
    e[0] !== undefined && e[1] !== undefined ? [[e[0], e[1]] as [string, string]] : [],
  );
}

/** Every job of a run as a waterfall row: queue wait, then the run; the forecast bracket for unfinished jobs. */
export function runRows(
  run: RunItem,
  now: number,
  open: (j: Job) => void,
  currentId?: number,
): { rows: WaterfallRow[]; nowSec: number | undefined; totalSec: number } {
  const jobs = run.jobs;
  const queuedAt = (j: Job) => (j.queued_at ? Date.parse(j.queued_at) : now);
  const t0 = Math.min(
    ...jobs.filter((j) => j.state !== 'queued' || j.wait_reason).map(queuedAt),
    now,
  );
  const nowSec = sec(now - t0);
  const depth = stages(jobs);
  const byId = new Map(jobs.map((j) => [nodeId(j), j]));
  // Estimated start and end of each job, in seconds from t0.
  const est = new Map<string, { start: number; end: number }>();
  const resolve = (j: Job, seen: Set<string>): { start: number; end: number } => {
    const known = est.get(nodeId(j));
    if (known) return known;
    let r: { start: number; end: number };
    if (j.started_at) {
      const start = sec(Date.parse(j.started_at) - t0);
      const end = j.completed_at
        ? sec(Date.parse(j.completed_at) - t0)
        : Math.max(nowSec, start + etaOf(forecastOf(j)).p50);
      r = { start, end };
    } else {
      const ready = seen.has(nodeId(j))
        ? nowSec
        : Math.max(
            nowSec + (j.queue_eta ? j.queue_eta.p50 : 0),
            ...needsOf(j).flatMap((n) => {
              const d = byId.get(n);
              return d ? [resolve(d, new Set([...seen, nodeId(j)])).end] : [];
            }),
          );
      r = { start: ready, end: ready + etaOf(forecastOf(j)).p50 };
    }
    est.set(nodeId(j), r);
    return r;
  };
  const crit = new Set(run.critical_path);
  const ordered = [...jobs].sort(
    (a, b) =>
      (depth.get(nodeId(a)) ?? 0) - (depth.get(nodeId(b)) ?? 0) ||
      queuedAt(a) - queuedAt(b),
  );
  const rows = ordered.map((j): WaterfallRow => {
    const s = jobStatus(j);
    const e = resolve(j, new Set());
    const waited = waitedSeconds(j, now);
    const started = Boolean(j.started_at);
    const queuedSec =
      j.queued_at && (j.state !== 'queued' || j.wait_reason)
        ? sec(queuedAt(j) - t0)
        : undefined;
    const f = forecastOf(j);
    const pending = j.state !== 'done';
    const startSec = started ? e.start : Math.max(e.start, nowSec);
    const ran = ranSeconds(j, now);
    return {
      id: `job-${j.id}`,
      label: j.job_name,
      sublabel: j.runner
        ? `${j.runner.name} on ${j.runner.machine_name ?? 'its machine'}`
        : (j.wait_text ?? 'not placed yet'),
      group: `Stage ${(depth.get(nodeId(j)) ?? 0) + 1}`,
      status: s.kind,
      startSec,
      endSec: started ? (j.state === 'done' ? e.end : nowSec) : startSec,
      ...(queuedSec !== undefined && queuedSec < startSec
        ? { waitFromSec: queuedSec }
        : {}),
      ...(pending && f.basis !== 'not_enough_history'
        ? {
            forecast: {
              loSec: startSec + f.lo - (ran ?? 0),
              hiSec: startSec + f.hi - (ran ?? 0),
            },
          }
        : {}),
      ...(j.id === currentId ? { current: true } : {}),
      ...(crit.has(nodeId(j)) ? { critical: true } : {}),
      detail: [
        s.word,
        waited !== undefined ? `waited ${formatDuration(waited)}` : undefined,
        ran !== undefined ? `ran ${formatDuration(ran)}` : undefined,
        `forecast ${forecastWords(f)}`,
      ]
        .filter(Boolean)
        .join(', '),
      onOpen: () => open(j),
    };
  });
  const totalSec = Math.max(
    1,
    ...rows.flatMap((r) => [r.endSec, r.forecast?.hiSec ?? 0]),
  );
  return { rows, nowSec: run.state === 'done' ? undefined : nowSec, totalSec };
}

export function graphNodes(
  run: RunItem,
  now: number,
  open: (j: Job) => void,
  currentId?: number,
): JobGraphNode[] {
  const crit = new Set(run.critical_path);
  const dynamic = new Set(run.graph.dynamic_nodes);
  return run.jobs.map((j) => {
    const s = jobStatus(j);
    const ran = ranSeconds(j, now);
    return {
      id: nodeId(j),
      label: j.job_name,
      sublabel: [
        j.state === 'done' || j.state === 'running'
          ? `${s.word}, ${ran !== undefined ? formatDuration(ran) : ''}`
          : j.queue_position
            ? `queued, number ${j.queue_position}`
            : `waits for ${needsOf(j).length} ${needsOf(j).length === 1 ? 'job' : 'jobs'}`,
        dynamic.has(nodeId(j)) ? 'expands at run time' : undefined,
      ]
        .filter(Boolean)
        .join('; '),
      status: s.kind,
      ...(j.id === currentId ? { current: true } : {}),
      ...(crit.has(nodeId(j)) ? { critical: true } : {}),
      onOpen: () => open(j),
    };
  });
}
