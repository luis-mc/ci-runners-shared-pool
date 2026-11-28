// The live run of a wizard step as the shared Terminal and the weighted bar read it (UX-88 to UX-90, CRSP336).
// A page that has no event stream of its own (restart, update, add runners) says what it saw and did, one row per
// change: the request it sent, the state the service reported, the plan step that started or ended. Nothing is
// invented: a phase with no percent says so and the bar stays indeterminate (UX-89).
import type { LiveEvent } from '@crsp/ui';
import { useCallback, useRef, useState } from 'react';
import type { Schemas } from '../api/api';

type Plan = Schemas['Plan'];
type Where = LiveEvent['machine'];

/** The console itself: the request lines are its own. */
export const CONSOLE_SIDE: Where = { name: 'This console', role: 'controller', os: '' };

export interface Phase {
  name: string;
  weight?: number;
}

export interface Note {
  /** Index of the phase in hand. */
  at: number;
  /** 0 to 100, or null when the phase reports none. */
  pct: number | null;
  reason?: string;
  command?: string;
  line?: string;
  error?: boolean;
  machine?: Where;
  bytes?: number;
  total?: number;
  rate?: number;
  etaSec?: number;
}

/** The events of one run, built from what the page sees. `say` appends a row unless it repeats the last one. */
export function useLiveLog(phases: readonly Phase[], base: Where = CONSOLE_SIDE) {
  const [events, setEvents] = useState<LiveEvent[]>([]);
  const last = useRef('');
  const phasesRef = useRef(phases);
  phasesRef.current = phases;
  const baseRef = useRef(base);
  baseRef.current = base;

  const say = useCallback((n: Note, from?: readonly Phase[]) => {
    const key = JSON.stringify([n.at, n.pct, n.command, n.line, n.error, n.bytes]);
    if (key === last.current) return;
    last.current = key;
    const list = from ?? phasesRef.current;
    const p = list[n.at];
    const ev: LiveEvent = {
      phase: p?.name ?? 'Working',
      phaseIndex: n.at,
      phaseCount: list.length,
      phaseWeight: p?.weight ?? 1,
      pct: n.pct,
      machine: n.machine ?? baseRef.current,
      stream: n.error ? 'stderr' : 'stdout',
      ts: Date.now(),
      ...(n.reason !== undefined ? { reason: n.reason } : {}),
      ...(n.command !== undefined ? { command: n.command } : {}),
      ...(n.line !== undefined ? { line: n.line } : {}),
      ...(n.bytes !== undefined ? { bytes: n.bytes } : {}),
      ...(n.total !== undefined ? { total: n.total } : {}),
      ...(n.rate !== undefined ? { rate: n.rate } : {}),
      ...(n.etaSec !== undefined ? { etaSec: n.etaSec } : {}),
    };
    setEvents((all) => [...all, ev]);
  }, []);

  const clear = useCallback(() => {
    last.current = '';
    setEvents([]);
  }, []);

  return { events, say, clear };
}

export const STEP_WORD: Record<string, string> = {
  planned: 'Waiting to start',
  waiting: 'Waiting for the step before it',
  running: 'Running',
  done: 'Done',
  skipped: 'Skipped, nothing to do',
  failed: 'Failed',
};

/** The phases of a plan: one per step, in order. */
export const planPhases = (plan: Plan | undefined): Phase[] =>
  (plan?.steps ?? []).map((s) => ({ name: s.text }));

/** How a page that shows one phase per group of steps (a repository) maps a step to its phase and reads the phase's percent. */
export interface StepGrouping {
  phaseOf: (step: Plan['steps'][number], index: number) => number;
  /** 0 to 100: the steps of the phase that are done or skipped, of all its steps. */
  pctOf: (plan: Plan, phase: number) => number;
}

/** The notes a plan's steps add since `seen` (step id and state, filled here): a step that begins writes its command
 * line, a step that ends writes its result. Call it with each plan the page reads. */
export function planNotes(
  plan: Plan,
  seen: Map<string, string>,
  machine?: Where,
  by?: StepGrouping,
): Note[] {
  const out: Note[] = [];
  plan.steps.forEach((s, index) => {
    const at = by ? by.phaseOf(s, index) : index;
    // A page that groups steps (one phase per repository) knows how far its phase is after each step (CRSP405).
    const pctNow = (fallback: number | null) => (by ? by.pctOf(plan, at) : fallback);
    const before = seen.get(s.id);
    if (before === s.state) return;
    seen.set(s.id, s.state);
    const began = before === 'running' || before === 'waiting';
    const where = machine ? { machine } : {};
    if (s.state === 'running' || s.state === 'waiting')
      out.push({
        at,
        pct: pctNow(null),
        reason:
          s.state === 'waiting'
            ? (STEP_WORD.waiting ?? '')
            : 'The step does not report a percent.',
        command: s.text,
        ...where,
        ...(s.state === 'waiting' && s.waits_for
          ? { line: `Waiting for ${s.waits_for}` }
          : {}),
      });
    else if (s.state === 'done' || s.state === 'skipped')
      out.push({
        at,
        pct: pctNow(100),
        line: STEP_WORD[s.state] ?? s.state,
        ...(began ? {} : { command: s.text }),
        ...where,
      });
    else if (s.state === 'failed')
      out.push({
        at,
        pct: pctNow(null),
        line: `Failed: ${s.error ?? 'no reason given'}`,
        error: true,
        ...(began ? {} : { command: s.text }),
        ...where,
      });
  });
  return out;
}
