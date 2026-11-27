// Component 80: the spark. A row of values drawn with the eight-level block characters ▁▂▃▄▅▆▇█. It is a picture with
// a text alternative that gives the first, last, lowest and highest value (CORE-5); it never carries a meaning alone.
export const SPARK_LEVELS = '▁▂▃▄▅▆▇█';

/** The same eight levels in braille dots, for the dotted-graphs form of the Cool Terminal theme. */
export const SPARK_DOT_LEVELS = '⢀⣀⣄⣤⣦⣶⣷⣿';

function levels(values: readonly number[], set: string): string {
  if (values.length === 0) return '';
  const lo = Math.min(...values);
  const hi = Math.max(...values);
  const span = hi - lo;
  return values
    .map((v) => set[span === 0 ? 0 : Math.round(((v - lo) / span) * 7)] ?? set[0])
    .join('');
}

/** The braille dot characters of a list of values, scaled between the lowest and the highest. */
export function sparkDots(values: readonly number[]): string {
  return levels(values, SPARK_DOT_LEVELS);
}

/** The block characters of a list of values, scaled between the lowest and the highest. */
export function sparkChars(values: readonly number[]): string {
  return levels(values, SPARK_LEVELS);
}

export function Spark({
  label,
  values,
  tone = 'ok',
  unit = '',
}: {
  label: string;
  values: readonly number[];
  tone?: 'ok' | 'note' | 'needs' | 'problem' | 'quiet' | undefined;
  unit?: string | undefined;
}) {
  const alt =
    values.length === 0
      ? `${label}: no values`
      : `${label}: from ${values[0]}${unit} to ${values[values.length - 1]}${unit}, lowest ${Math.min(...values)}${unit}, highest ${Math.max(...values)}${unit}`;
  return (
    <span className="ds-spark" role="img" aria-label={alt} data-tone={tone}>
      <span className="ds-spark__blocks" aria-hidden="true">
        {sparkChars(values)}
      </span>
      <span className="ds-spark__dots" aria-hidden="true">
        {sparkDots(values)}
      </span>
    </span>
  );
}
