// The words of the listener gate on the runner page (runners R5.134 to R5.140). They are in content.md,
// "Listener gate words" (OPS78).
import type { Schemas } from '../../api/api';

type GateRunner = Schemas['ListenerGateRunner'];

export const GATE_SECTION = 'Listener';
export const GATE_WAKE_HELP =
  'Every gated runner is switched on for about a minute each week, with no job, so GitHub never removes it for staying offline.';
export const GATE_NO_JOB = 'No job waits for this runner.';
export const GATE_OBSERVE_NOTE =
  'The gate is in observe mode. crsp writes would_start and would_stop rows to the audit log and leaves the listener as it is.';

/** The gate's row for a runner, found by name; undefined when the runner is not gated. */
export function gateRowOf(
  gate: Pick<Schemas['ListenerGate'], 'runners'> | undefined,
  name: string,
): GateRunner | undefined {
  return gate?.runners.find((r) => r.runner_name === name);
}

/** What the Listener fact says: the gate's own words, never an offline fault. */
export function listenerText(row: Pick<GateRunner, 'text'>): string {
  return row.text;
}

/** Why jobs wait for the runner, or the line that none does. */
export function waitingText(row: Pick<GateRunner, 'queue_text'>): string {
  return row.queue_text && row.queue_text.length > 0 ? row.queue_text : GATE_NO_JOB;
}
