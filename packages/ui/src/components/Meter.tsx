// Component 79: the meter. A level drawn with block characters, `■■■■□□□□ 52%`, with its value in words (CORE-5): the
// colour is a hint, the number is the meaning. A level at or above `warnAt` is amber, at or above `badAt` red.
export function Meter({
  label,
  value,
  max = 100,
  cells = 8,
  warnAt = 75,
  badAt = 90,
  text,
}: {
  label: string;
  value: number;
  max?: number | undefined;
  /** How many block characters the meter is wide. */
  cells?: number | undefined;
  warnAt?: number | undefined;
  badAt?: number | undefined;
  /** The words after the blocks; default is the percent. */
  text?: string | undefined;
}) {
  const pct = max > 0 ? Math.max(0, Math.min(100, (value / max) * 100)) : 0;
  const on = Math.round((pct / 100) * cells);
  const level = pct >= badAt ? 'bad' : pct >= warnAt ? 'warn' : 'ok';
  const words = text ?? `${Math.round(pct)}%`;
  return (
    <span
      className="ds-meter"
      role="meter"
      aria-label={label}
      aria-valuemin={0}
      aria-valuemax={100}
      aria-valuenow={Math.round(pct)}
      aria-valuetext={words}
      data-level={level}
    >
      <span className="ds-meter__blocks" aria-hidden="true">
        <span className="ds-meter__on">{'■'.repeat(on)}</span>
        <span className="ds-meter__off">{'■'.repeat(cells - on)}</span>
      </span>
      <span className="ds-meter__text">{words}</span>
    </span>
  );
}
