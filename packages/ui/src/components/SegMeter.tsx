// Component 85: the segment meter (UX-91). A level as a row of segments, 10 by default, each partly or fully filled in
// steps of 5 %, with its value in words (CORE-5): the colour is a hint, the number is the meaning. Blocks in the Cool
// Terminal theme, dots in its dotted-graphs form (CSS only).
export const SEG_MIN = 3;
export const SEG_MAX = 20;
export const SEG_DEFAULT = 10;
const STEP = 5;

/** The fill of each segment, 0 to 1, for a level in per cent: the level is rounded to a step of 5 %. */
export function segmentFills(pct: number, segments: number = SEG_DEFAULT): number[] {
  const n = Math.max(SEG_MIN, Math.min(SEG_MAX, Math.round(segments)));
  const level = Math.round(Math.max(0, Math.min(100, pct)) / STEP) * STEP;
  const size = 100 / n;
  return Array.from({ length: n }, (_, i) =>
    Math.max(0, Math.min(1, (level - i * size) / size)),
  );
}

/** The segments alone, decorative; a parent supplies the meter role and the words. */
export function Segments({
  pct,
  segments = SEG_DEFAULT,
}: {
  pct: number;
  segments?: number | undefined;
}) {
  return (
    <span className="ds-segs" aria-hidden="true">
      {segmentFills(pct, segments).map((f, i) => (
        <span
          key={i}
          className="ds-segs__cell"
          data-fill={f >= 1 ? 'full' : f > 0 ? 'part' : 'empty'}
          style={{ '--ds-seg-fill': `${Math.round(f * 100)}%` } as React.CSSProperties}
        />
      ))}
    </span>
  );
}

export function SegMeter({
  label,
  value,
  max = 100,
  segments = SEG_DEFAULT,
  warnAt = 75,
  badAt = 90,
  text,
}: {
  label: string;
  value: number;
  max?: number | undefined;
  /** How many segments wide: 3 to 20, 10 by default. */
  segments?: number | undefined;
  warnAt?: number | undefined;
  badAt?: number | undefined;
  /** The words after the segments; default is the percent. */
  text?: string | undefined;
}) {
  const pct = max > 0 ? Math.max(0, Math.min(100, (value / max) * 100)) : 0;
  const level = pct >= badAt ? 'bad' : pct >= warnAt ? 'warn' : 'ok';
  const words = text ?? `${Math.round(pct)}%`;
  return (
    <span
      className="ds-segmeter"
      role="meter"
      aria-label={label}
      aria-valuemin={0}
      aria-valuemax={100}
      aria-valuenow={Math.round(pct)}
      aria-valuetext={words}
      data-level={level}
      style={{ '--ds-pct': Math.round(pct) } as React.CSSProperties}
    >
      <Segments pct={pct} segments={segments} />
      <span className="ds-segmeter__text">{words}</span>
    </span>
  );
}
