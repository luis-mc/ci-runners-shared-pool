// Dependency setup (dependency-setup spec 4, DEP1 to DEP11): the words and the small decisions of the wizard
// "Get this machine ready", as pure functions so they are tested without a screen. The words are in content.md
// ("Dependency setup words").
import { formatDuration, formatSize } from '@crsp/ui';
import { overallPct, phasesFromEvents, type ProgressEvent } from '@crsp/contract';
import type { Schemas } from '../../api/api';

export type DepCheck = Schemas['DependencyCheck'];
export type DepChoice = Schemas['DependencyChoice'];
export type DepFacts = Schemas['DependencyFacts'];
export type DepItem = Schemas['DependencyItem'];
export type DepJob = Schemas['DependencyJob'];
export type DepEvent = Schemas['DependencyEvent'];
export type RunnerOS = Schemas['DependencyCheckRequest']['runner_os'];
export type RunnerKind = Schemas['DependencyCheckRequest']['runner_kind'];

export const STEPS = [
  'Look',
  'Choose',
  'Review',
  'Installing',
  'Verify',
  'Done',
] as const;
export const STEP = {
  check: 0,
  choose: 1,
  review: 2,
  install: 3,
  verify: 4,
  done: 5,
} as const;

/** The first-class item names, when a stored job has no check beside it. */
export const ITEM_NAMES: Record<string, string> = {
  wsl2: 'WSL2',
  'docker-in-wsl2': 'Docker in WSL2',
  'docker-desktop': 'Docker Desktop',
  'docker-engine': 'Docker Engine',
};
export const itemName = (id: string, items: readonly DepItem[] = []): string =>
  items.find((i) => i.id === id)?.name ?? ITEM_NAMES[id] ?? id;

/** The job states that are still going: the page keeps reading them. */
const ACTIVE: readonly DepJob['state'][] = [
  'consented',
  'installing',
  'paused_restart',
  'verifying',
  'preparing',
  'undoing',
];
export const jobActive = (j: DepJob): boolean => ACTIVE.includes(j.state);

/** One API event (snake_case) as the shared progress contract (camelCase, CRSP247). The one mapper of the page. */
export function toProgressEvent(e: DepEvent): ProgressEvent {
  return {
    jobId: e.job_id,
    phase: e.phase,
    phaseIndex: e.phase_index,
    phaseCount: e.phase_count,
    phaseWeight: e.phase_weight,
    pct: e.pct,
    ...(e.reason !== undefined ? { reason: e.reason } : {}),
    ...(e.bytes !== undefined ? { bytes: e.bytes } : {}),
    ...(e.total !== undefined ? { total: e.total } : {}),
    ...(e.rate !== undefined ? { rate: e.rate } : {}),
    ...(e.eta_sec !== undefined ? { etaSec: e.eta_sec } : {}),
    machine: {
      name: e.machine.name,
      role: e.machine.role === 'controller' ? 'controller' : 'agent',
      os: e.machine.os,
    },
    ...(e.command !== undefined ? { command: e.command } : {}),
    // A status event is not process output: the shared contract has the two streams only.
    stream: e.stream === 'stderr' ? 'stderr' : 'stdout',
    ...(e.line !== undefined ? { line: e.line } : {}),
    ts: e.ts,
  };
}

/** The live line of the work step: the phase in hand, bytes done of total, time left (UX-29). */
export function progressLine(e: ProgressEvent | undefined): string {
  if (!e) return 'Starting';
  const rest: string[] = [];
  if (e.bytes !== undefined && e.total !== undefined && e.total > 0)
    rest.push(`${formatSize(e.bytes)} of ${formatSize(e.total)}`);
  else if (e.pct === null && e.reason) rest.push(e.reason);
  if (e.etaSec !== undefined) rest.push(`${formatDuration(e.etaSec)} left`);
  return rest.length ? `${e.phase}: ${rest.join(', ')}` : e.phase;
}

/** The percent of the whole job for the bar, or undefined while the phase reports none. */
export function overallValue(log: readonly DepEvent[]): number | undefined {
  const events = log.map(toProgressEvent);
  if (events.length === 0) return undefined;
  const phases = phasesFromEvents(events);
  const last = events[events.length - 1];
  if (!last) return undefined;
  return overallPct(phases, last.phaseIndex) ?? undefined;
}

/** The output lines of the folded log, as one block to read and copy. */
export function logText(log: readonly DepEvent[]): string {
  return log
    .filter((e) => e.line)
    .map((e) => e.line)
    .join('\n');
}

export const recommended = (c: DepCheck): DepChoice | undefined =>
  c.choices.find((x) => x.recommended) ?? c.choices[0];

const minutesOf = (c: DepChoice): number =>
  c.facts.reduce((s, f) => s + f.typical_minutes, 0);
const restartOf = (c: DepChoice): boolean => c.facts.some((f) => f.restart_likely);
const needsAdminOf = (c: DepChoice): boolean => c.facts.some((f) => f.needs_admin);

/** The names of what a choice installs, in the words of the offer: "WSL2 and Docker". */
export function itemWords(c: DepChoice, items: readonly DepItem[]): string {
  const names = c.item_ids.map((id) => itemName(id, items));
  return names.length <= 2
    ? names.join(' and ')
    : `${names.slice(0, -1).join(', ')} and ${names[names.length - 1] ?? ''}`;
}

/** The primary of the first run: "Install WSL2 and Docker". The time and the restart go in {@link installNote}, so the button stays one short line on a phone. */
export function installLabel(c: DepChoice, items: readonly DepItem[]): string {
  return `Install ${itemWords(c, items)}`;
}

/** The line above the primary: "About 10 minutes, one restart." */
export function installNote(c: DepChoice): string {
  const m = minutesOf(c);
  const extra = [
    m > 0 ? `about ${m} ${m === 1 ? 'minute' : 'minutes'}` : '',
    restartOf(c) ? 'one restart' : '',
  ].filter(Boolean);
  if (!extra.length) return '';
  const text = extra.join(', ');
  return `${text[0]!.toUpperCase()}${text.slice(1)}.`;
}

export const reviewLabel = (c: DepChoice): string =>
  `Install ${c.item_ids.length} ${c.item_ids.length === 1 ? 'item' : 'items'}`;

/** The headline of the first run (spec 4). */
export function headline(c: DepCheck): string {
  if (c.start_only) return 'Docker is installed but not running.';
  return `${c.os?.toLowerCase().includes('windows') ? 'This PC' : 'This machine'} cannot run Linux containers yet`;
}

const STATE_WORDS: Record<DepItem['state'], string> = {
  ok: 'Ready',
  missing: 'Missing',
  stopped: 'Installed but not running',
  wrong_kind: 'Installed, but the wrong kind',
};

/** The facts of the first run: one row per item with its state and why the plan needs it (DEP2). */
export function itemFacts(items: readonly DepItem[]): { name: string; value: string }[] {
  return items.map((i) => ({
    name: i.name,
    value: `${STATE_WORDS[i.state]}. ${i.why}`,
  }));
}

/** The lines of one facts card on Review (DEP3). */
export function factRows(f: DepFacts): { name: string; value: string }[] {
  return [
    { name: 'Version', value: f.version },
    { name: 'Comes from', value: f.source },
    { name: 'Download', value: formatSize(f.download_bytes) },
    { name: 'Disk space', value: formatSize(f.disk_bytes) },
    {
      name: 'Time',
      value: `about ${f.typical_minutes} ${f.typical_minutes === 1 ? 'minute' : 'minutes'}`,
    },
    { name: 'Administrator', value: f.needs_admin ? 'Needed' : 'Not needed' },
    { name: 'Restart', value: f.restart_likely ? 'Likely' : 'Not expected' },
    ...(f.licence_note ? [{ name: 'Licence', value: f.licence_note }] : []),
    { name: 'To undo', value: f.undo },
  ];
}

export const reviewSummary = (c: DepChoice, disk: number): string => {
  const down = c.facts.reduce((s, f) => s + f.download_bytes, 0);
  const use = c.facts.reduce((s, f) => s + f.disk_bytes, 0);
  return `Download ${formatSize(down)}, disk space ${formatSize(use)} of ${formatSize(disk)} free, about ${minutesOf(c)} minutes${needsAdminOf(c) ? ', needs an administrator' : ''}${restartOf(c) ? ', one restart' : ''}.`;
};

/** Failed: what, why, what next, in words (UX-6). */
export function failedWords(j: DepJob, items: readonly DepItem[]): string {
  const what = `Installing ${j.item_ids.map((id) => itemName(id, items)).join(' and ')} did not finish.`;
  const why = j.error
    ? `${j.error.replace(/\.?$/, '.')}`
    : 'crsp has no reason from the machine.';
  const next =
    j.added.length > 0
      ? 'Try again, follow the steps yourself, or undo what crsp added.'
      : 'Nothing was added. Try again, or follow the steps yourself.';
  return `${what} ${why} ${next}`;
}

/** The lines the undo question names, one per item (UX-62). */
export function undoItems(j: DepJob, items: readonly DepItem[] = []): string[] {
  if (j.added.length === 0) return ['Nothing that crsp installed is left to remove'];
  return j.added.map((a) =>
    `${itemName(a.item_id, items)} ${a.version}: ${a.kind} ${a.ref}`.trim(),
  );
}

/** What the Done step lists: what really changed (DEP10), from the job's record. */
export function doneRows(
  j: DepJob,
  items: readonly DepItem[] = [],
): { name: string; value: string }[] {
  const rows = j.added.map((a) => ({
    name: itemName(a.item_id, items),
    value: `${a.kind} ${a.ref} ${a.version}`.trim(),
  }));
  return [
    ...(rows.length ? rows : [{ name: 'Installed by crsp', value: 'Nothing' }]),
    { name: 'Engine version', value: j.engine_version ?? 'Not reported' },
  ];
}
