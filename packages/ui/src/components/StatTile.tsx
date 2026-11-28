// Component 58: a stat tile, one key number with its change and its trend (UX-75). The tile is one link when it leads
// somewhere. The sparkline is hidden from assistive technology; the tile says the same in words (UX-17).
import { Link } from 'react-router-dom';
import { Sparkline } from './Charts';
import type { Unit } from './Viz.types';

export type StatChange = 'better' | 'worse' | 'same';

const GLYPH: Record<StatChange, string> = { better: '✓', worse: '◷', same: '' };

export function StatTile({
  label,
  value,
  unit,
  change,
  changeText,
  trend,
  trendUnit = 'count',
  to,
  linkName,
  loading = false,
}: {
  label: string;
  /** The value as text; leave out for "no data yet". */
  value?: string | undefined;
  /** The unit that follows the value (`jobs`, `min`). */
  unit?: string | undefined;
  /** How the value moved, as a word, a glyph and a tone (UX-58); never the Problem tone. */
  change?: StatChange | undefined;
  /** The change in words, such as `up 2 vs the previous 7 days`. */
  changeText?: string | undefined;
  /** The values of the trend, oldest first. */
  trend?: readonly number[] | undefined;
  trendUnit?: Unit | undefined;
  /** Where the tile leads, and the name of that target. */
  to?: string | undefined;
  linkName?: string | undefined;
  loading?: boolean | undefined;
}) {
  const none = value === undefined;
  const zero = !none && /^0(\D.*)?$/.test(value);
  const body = (
    <>
      <span className="ds-stat__label">{label}</span>{' '}
      <span className="ds-stat__value">
        {loading ? 'Loading' : none ? '–' : value}
        {!loading && !none && unit ? (
          <>
            {' '}
            <span className="ds-stat__unit">{unit}</span>
          </>
        ) : null}
      </span>
      {(none && !loading) || changeText ? (
        <>
          {' '}
          <span className="ds-stat__change" data-change={change}>
            {none && !loading ? (
              'no data yet'
            ) : (
              <>
                {change && GLYPH[change] ? (
                  <span aria-hidden="true">{GLYPH[change]} </span>
                ) : null}
                {changeText}
                {change && change !== 'same' ? (
                  <span className="ds-visually-hidden">, {change}</span>
                ) : null}
              </>
            )}
          </span>
        </>
      ) : null}
      {trend && trend.length > 1 ? (
        <span className="ds-stat__trend" aria-hidden="true">
          <Sparkline label={`${label} trend`} values={trend} unit={trendUnit} />
        </span>
      ) : null}
      {to && linkName ? <span className="ds-visually-hidden">. {linkName}</span> : null}
    </>
  );
  return to ? (
    <Link to={to} className="ds-stat" data-layout-group data-zero={zero || undefined}>
      {body}
    </Link>
  ) : (
    <div className="ds-stat" data-layout-group data-zero={zero || undefined}>
      {body}
    </div>
  );
}

/** The tiles of one row: they share one width and one height; two to a row at phone width. */
export function StatGrid({ children }: { children: React.ReactNode }) {
  return (
    <div className="ds-stat-grid" data-layout-group>
      {children}
    </div>
  );
}
