// The words of the Pool section (LOOK56): when a waiting job starts, which jobs need the owner, and the four counts.
import type { Schemas } from '../../api/api';
import { waitWords, type Eta, type Job } from '../jobs/words';

/** "Starts about 14:16": the clock time of a queued job's start, from the estimate read at `readAt` (ms). */
export function startsAbout(eta: Eta | undefined, readAt: number): string {
  if (!eta) return 'Start not known';
  const at = new Date(readAt + eta.p50 * 1000);
  const hh = String(at.getHours()).padStart(2, '0');
  const mm = String(at.getMinutes()).padStart(2, '0');
  return `Starts about ${hh}:${mm}`;
}

/** A waiting job that will not start by itself, or is at risk, is marked "Needs you". */
export function needsYou(j: Pick<Job, 'state' | 'wait_reason'>): boolean {
  return (
    j.state === 'queued' &&
    (j.wait_reason === 'no_runner_with_labels' || j.wait_reason === 'machine_drained')
  );
}

/** What the owner does about a job that needs them, in one sentence. */
export function needsYouWords(j: Pick<Job, 'wait_reason' | 'job_name'>): string {
  return j.wait_reason === 'machine_drained'
    ? `${j.job_name} waits on a machine that is not taking jobs. Let the machine take jobs again, or add a runner on another machine.`
    : `${j.job_name} will not start by itself. Add a runner with its labels, or change the labels of the job.`;
}

export interface PoolCounts {
  running: number;
  starting: number;
  queued: number;
  noRunner: number;
}

/** Running, starting (a runner was given but the job has not begun), queued, and queued with no runner that fits. */
export function poolCounts(o: Pick<Schemas['Overview'], 'running' | 'queued' | 'queued_count'>): PoolCounts {
  const starting = o.running.filter((j) => !j.started_at).length;
  const queued = o.queued ?? [];
  return {
    running: o.running.length - starting,
    starting,
    queued: o.queued_count,
    noRunner: queued.filter((j) => j.wait_reason === 'no_runner_with_labels').length,
  };
}

/** Where a job is placed, in words: "BuildTower · tower-small-1", or none yet. */
export function placeWords(j: Pick<Job, 'machine_id' | 'runner_name' | 'runner'>): string {
  const machine = j.runner?.machine_name ?? j.machine_id;
  const runner = j.runner?.name ?? j.runner_name;
  return [machine, runner].filter(Boolean).join(' · ') || 'none yet';
}

export { waitWords };
