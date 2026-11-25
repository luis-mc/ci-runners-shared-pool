// Component 78: summary line (UX-86). One inline line of counts, number first.
import { Fragment } from 'react';

export type SummaryItem = {
  count: number;
  /** The word after the number, for one ("machine") or for any count when `plural` is not given ("idle"). */
  word: string;
  /** The word for a count other than one ("machines"). */
  plural?: string | undefined;
  /** Colour only when it matters: used when the count is above zero. A zero is always dim. */
  tone?: 'warning' | 'danger' | undefined;
};

function Items({ items }: { items: readonly SummaryItem[] }) {
  return (
    <>
      {items.map((it, i) => (
        <Fragment key={it.word}>
          {i > 0 ? <span aria-hidden="true"> · </span> : null}
          <span
            className="ds-summary__item"
            data-zero={it.count === 0 ? '' : undefined}
            data-tone={it.count > 0 ? it.tone : undefined}
          >
            {it.count} {it.count === 1 ? it.word : (it.plural ?? it.word)}
          </span>
        </Fragment>
      ))}
    </>
  );
}

/**
 * "1 machine · 5 runners — 4 idle · 0 busy · 1 not ready": what there is, then the state of it.
 * A page passes the numbers; the line, the dots, the dash and the colours come from here.
 */
export function SummaryLine({
  total,
  states,
}: {
  total: readonly SummaryItem[];
  states?: readonly SummaryItem[] | undefined;
}) {
  return (
    <p className="ds-summary">
      <Items items={total} />
      {states && states.length > 0 ? (
        <>
          <span aria-hidden="true"> — </span>
          <Items items={states} />
        </>
      ) : null}
    </p>
  );
}
