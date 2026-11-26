// Component 80: the spark. A row of values drawn with the eight-level block characters ▁▂▃▄▅▆▇█. It is a picture with
// a text alternative that gives the first, last, lowest and highest value (CORE-5); it never carries a meaning alone.
export const SPARK_LEVELS = '▁▂▃▄▅▆▇█';

/** The block characters of a list of values, scaled between the lowest and the highest. */
export function sparkChars(values: readonly number[]): string {
  if (values.length === 0) return '';
  const lo = Math.min(...values);
  const hi = Math.max(...values);
  const span = hi - lo;
  return values
    .map((v) => SPARK_LEVELS[span === 0 ? 0 : Math.round(((v - lo) / span) * 7)] ?? '▁')
    .join('');
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
      <span aria-hidden="true">{sparkChars(values)}</span>
    </span>
  );
}
