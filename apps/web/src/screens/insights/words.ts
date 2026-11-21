// The words of the insights screens (design system content.md, "Run insights words"). One place, so a table, a
// fact and a chart say the same thing.
import { formatDuration } from '@crsp/ui';
import type { Schemas } from '../../api/api';

export type Finding = Schemas['InsightFinding'];

export const AREA_WORDS: Record<Schemas['InsightArea'], string> = {
  waits: 'Waits',
  pool: 'Pool',
  runs: 'Runs',
  workflows: 'Workflows',
  guardrails: 'Guardrails',
  tests: 'Tests',
};

export const RANGES = [
  { value: '7', label: '7 days' },
  { value: '14', label: '14 days' },
  { value: '30', label: '30 days' },
] as const;

/** "the pool" for a finding that belongs to no project. */
export const projectOf = (f: Pick<Finding, 'repository' | 'scope'>): string =>
  f.repository ?? (f.scope === 'pool' ? 'the pool' : 'not known');

/** The minutes of a finding, always an estimate (R5.9). */
export const minutesWords = (n: number): string => `about ${n} min a week`;

/** The order of Do next: most minutes first, a tie to the older finding (R5.11). */
export function ranked(items: readonly Finding[]): Finding[] {
  return [...items].sort(
    (a, b) =>
      b.minutes_saved_week - a.minutes_saved_week ||
      Date.parse(a.first_seen) - Date.parse(b.first_seen),
  );
}

export const secondsWords = (s: number | undefined): string =>
  s === undefined ? 'not known' : formatDuration(s);

export const GUARDRAIL_WORDS: Record<
  Schemas['GuardrailState']['state'],
  { glyph: string; word: string }
> = {
  pass: { glyph: '✓', word: 'pass' },
  break: { glyph: '✕', word: 'break' },
  off: { glyph: '–', word: 'off' },
  unknown: { glyph: '◷', word: 'unknown' },
};

export function guardrailWords(g: Schemas['GuardrailState']): string {
  const w = GUARDRAIL_WORDS[g.state];
  if (g.state === 'break') return `${w.glyph} ${w.word}${g.count ? ` ${g.count}` : ''}`;
  if (g.state === 'off') return `${w.glyph} ${w.word}${g.reason ? ` (${g.reason})` : ''}`;
  return `${w.glyph} ${w.word}`;
}

export const HOLD_REASON_WORDS: Record<string, string> = {
  image_pull: 'pulling the image',
  runner_start: 'starting the runner',
  registration: 'registering the runner',
  load: 'waiting for load to fall',
  unknown: 'reason not known',
};

export const BACKLOG_WORDS = (jobs: number, until: string | undefined): string =>
  `Waiting for GitHub's hourly limit${until ? ` until ${new Date(until).toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' })}` : ''}; ${jobs} jobs not read yet.`;

/** The measured block of the Job page (OPS66; content.md, "Measured peak words"). */
export const MEASURED_WORDS = {
  title: 'Measured on this run',
  cpu: 'Measured peak CPU',
  memory: 'Measured peak memory',
  samples: 'Samples',
  duration: 'Measured for',
  oom: 'This job was killed for memory. A run like this is not used for sizing.',
  cutShort: 'This run was cut short. A run like this is not used for sizing.',
  noGoodRun: 'No good run yet.',
} as const;

/** The job-key line: the typical peak once 5 good runs are read, else how far it is. */
export function keyLine(k: Schemas['JobKeyMeasured']): string {
  if (k.measured)
    return `Typical peak of this job, p90 of the last ${k.runs} runs: ${(k.cpu_p90_cores ?? 0).toFixed(1)} cores, ${((k.memory_p90_bytes ?? 0) / 1_000_000_000).toFixed(1)} GB`;
  return k.runs === 0
    ? MEASURED_WORDS.noGoodRun
    : `Not measured yet: ${k.runs} of 5 good runs.`;
}
