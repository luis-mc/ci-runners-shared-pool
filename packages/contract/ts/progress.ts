// The progress event contract (CRSP247). One event per fact of a running job: a phase change, a
// percentage, a download sample or one output line. Emitters (the dependency installer, update,
// runner add) send it; the shared wizard part reads it. The shape is STABLE: add optional fields only.

export type ProgressStream = 'stdout' | 'stderr';
export type MachineRole = 'controller' | 'agent';

/** The machine that runs the command, shown per line or per block of the terminal. */
export interface ProgressMachine {
  name: string;
  role: MachineRole;
  /** Operating system, for example "Ubuntu 24.04" or "Windows 11". */
  os: string;
}

export interface ProgressEvent {
  jobId: string;
  /** Name of the phase now running, in the words of content.md ("Downloading Docker Engine"). */
  phase: string;
  /** Zero-based index of the phase. */
  phaseIndex: number;
  phaseCount: number;
  /** Share of the whole job this phase counts for. Weights are relative; the UI normalises them. */
  phaseWeight: number;
  /** Percent of THIS phase, 0 to 100. `null` means no percent is reported: the bar is indeterminate. */
  pct: number | null;
  /** Why no percent is reported, when `pct` is null ("Waiting for the installer to finish, no % is reported"). */
  reason?: string;
  /** Bytes done and total of a download. Real counts, never estimates. */
  bytes?: number;
  total?: number;
  /** Measured rate in bytes per second. */
  rate?: number;
  /** Seconds left, computed from the measured rate. */
  etaSec?: number;
  machine: ProgressMachine;
  /** The command line being run, set on the first event of a command. */
  command?: string;
  stream: ProgressStream;
  /** One output line. */
  line?: string;
  /** Unix time in milliseconds. */
  ts: number;
}

/** One phase of a weighted bar. */
export interface ProgressPhase {
  name: string;
  weight: number;
  /** 0 to 100, or null when the phase reports no percent. */
  pct: number | null;
  reason?: string | undefined;
}

/** The real percent of the whole job from its phases: finished phases count in full, the current one by its pct.
 * Returns null when the current phase reports no percent (the bar is indeterminate). */
export function overallPct(
  phases: readonly ProgressPhase[],
  current: number,
): number | null {
  const total = phases.reduce((s, p) => s + Math.max(0, p.weight), 0);
  if (total <= 0) return null;
  let done = 0;
  for (let i = 0; i < phases.length; i++) {
    const p = phases[i]!;
    const w = Math.max(0, p.weight);
    if (i < current) done += w;
    else if (i === current) {
      if (p.pct === null) return null;
      done += (w * Math.min(100, Math.max(0, p.pct))) / 100;
    }
  }
  return Math.round((done / total) * 1000) / 10;
}

/** Fold a stream of events into the phases and the terminal lines a screen shows. */
export function phasesFromEvents(events: readonly ProgressEvent[]): ProgressPhase[] {
  const out: ProgressPhase[] = [];
  for (const e of events) {
    const count = Math.max(e.phaseCount, e.phaseIndex + 1);
    while (out.length < count) out.push({ name: '', weight: 1, pct: 0 });
    out[e.phaseIndex] = {
      name: e.phase,
      weight: e.phaseWeight,
      pct: e.pct,
      reason: e.reason,
    };
  }
  return out;
}
