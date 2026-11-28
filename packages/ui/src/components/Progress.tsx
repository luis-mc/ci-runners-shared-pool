import type { CSSProperties, ReactNode } from 'react';
import { formatDuration } from './format';
import { Segments } from './SegMeter';

/** One phase of a weighted bar (UX-88). `pct` is 0 to 100 for this phase, or null when it reports none. */
export interface ProgressPhaseData {
  name: string;
  weight: number;
  pct: number | null;
  reason?: string | undefined;
}

/** The real percent of the whole job: finished phases count in full, the current one by its own percent.
 * null while the current phase reports none (the bar is indeterminate). Same maths as `overallPct` of @crsp/contract. */
export function weightedPct(
  phases: readonly ProgressPhaseData[],
  current: number,
): number | null {
  const total = phases.reduce((s, p) => s + Math.max(0, p.weight), 0);
  if (total <= 0) return null;
  let done = 0;
  for (let i = 0; i < phases.length; i++) {
    const p = phases[i];
    if (!p) continue;
    const w = Math.max(0, p.weight);
    if (i < current) done += w;
    else if (i === current) {
      if (p.pct === null) return null;
      done += (w * Math.min(100, Math.max(0, p.pct))) / 100;
    }
  }
  return Math.round((done / total) * 1000) / 10;
}

/** Component 16 with weighted phases (UX-88): the bar of a whole job. The line under it says the phase; a phase with
 * no percent says why and shows the time elapsed, and the bar is indeterminate. */
export function PhaseProgress({
  phases,
  current,
  elapsedSec,
  statusId,
}: {
  phases: readonly ProgressPhaseData[];
  current: number;
  elapsedSec?: number | undefined;
  /** The id the wizard's busy primary points at (UX-13). */
  statusId?: string | undefined;
}) {
  const phase = phases[current];
  const pct = weightedPct(phases, current);
  const name = phase?.name || 'Working';
  const step = `Step ${Math.min(current + 1, phases.length)} of ${phases.length}: ${name}`;
  const elapsed =
    elapsedSec === undefined ? '' : ` ${formatDuration(elapsedSec)} elapsed`;
  const text =
    pct === null
      ? `${name}. ${phase?.reason ? `${phase.reason} ` : ''}No % is reported.${elapsed}`
      : `${pct}% of the whole job`;
  return (
    <div className="ds-stack" data-gap="tight" data-layout-group>
      <p className="ds-text" data-tone="muted">
        {step}
      </p>
      <Progress label={text} {...(pct === null ? {} : { value: pct, max: 100 })} />
      <p className="ds-text" role="status" id={statusId}>
        {text}
      </p>
    </div>
  );
}
// Component 16: a progress bar. A known total is a filled bar; an unknown total is an animated bar (UX-29).
// LevelBar shows a level against a budget (UX-41) with its value in words.
/** Segments in a progress bar in the Cool Terminal themes (UX-91). */
export const PROGRESS_SEGMENTS = 20;

export function Progress({
  label,
  value,
  max,
}: {
  label: string;
  value?: number | undefined;
  max?: number | undefined;
}) {
  const known = value !== undefined && max !== undefined && max > 0;
  return (
    <div
      className="ds-progress"
      role="progressbar"
      aria-label={label}
      aria-valuemin={known ? 0 : undefined}
      aria-valuemax={known ? max : undefined}
      aria-valuenow={known ? value : undefined}
      aria-valuetext={known ? `${value} of ${max}` : label}
      data-known={known || undefined}
    >
      <Segments
        pct={known ? Math.min(100, ((value ?? 0) / (max ?? 1)) * 100) : 100}
        segments={PROGRESS_SEGMENTS}
      />
      <span
        className="ds-progress__fill"
        style={
          known
            ? { inlineSize: `${Math.min(100, ((value ?? 0) / (max ?? 1)) * 100)}%` }
            : {}
        }
      />
    </div>
  );
}

export function LevelBar({
  label,
  value,
  max,
  text,
  help,
  segments,
}: {
  label: string;
  value: number;
  max: number;
  text: string;
  /** Segments in the Cool Terminal themes (3 to 20, default 10, UX-91). */
  segments?: number | undefined;
  /** A term-help button (component 66) beside the name. */
  help?: ReactNode;
}) {
  const pct = max > 0 ? Math.min(100, (value / max) * 100) : 0;
  return (
    <div className="ds-level" style={{ '--ds-pct': Math.round(pct) } as CSSProperties}>
      <span className="ds-level__name">
        {label}
        {help}
      </span>
      <div
        className="ds-level__bar"
        role="meter"
        aria-label={label}
        aria-valuemin={0}
        aria-valuemax={max}
        aria-valuenow={value}
        aria-valuetext={text}
        data-over={value > max || undefined}
        data-level={pct >= 90 ? 'bad' : pct >= 75 ? 'warn' : 'ok'}
      >
        <span className="ds-level__fill" style={{ inlineSize: `${pct}%` }} />
        <Segments pct={pct} segments={segments} />
      </div>
      <span className="ds-level__text">{text}</span>
    </div>
  );
}
