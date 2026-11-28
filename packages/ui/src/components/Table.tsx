// Component 15: tables and list rows. Use a table only to compare three or more facts (UX-25). Column heads
// sort. A table never scrolls sideways, nor does the page: cells wrap and a row folds to a stack at phone width (UX-49). j and k move the
// active row, Enter opens it (UX-57): the shell sets `data-list-row` and the handler reads it.
import {
  Fragment,
  useId,
  useMemo,
  useState,
  type CSSProperties,
  type ReactNode,
} from 'react';
import { Button } from './Button';
import { RowAction, RowPressAction } from './RowAction';
import { useKeyHints } from './KeyHints';
import { Pane } from './Pane';
import { ShortText } from './ShortText';

export interface Column<T> {
  id: string;
  header: string;
  cell: (row: T) => ReactNode;
  /** A sort value. A column without one does not sort. */
  sort?: ((row: T) => string | number) | undefined;
  /** Left out of the two-line fold at phone width; the row still shows it on a wide screen (UX-25). */
  secondary?: boolean | undefined;
  /**
   * Long free text of this cell (an error, a reason). The cell shows it as one line with an ellipsis and, when it
   * is longer than one line, a button (the table's `expandLabel`) that opens the full text in a full-width row
   * under the row. `short` is a server-given short form; without one the table cuts the text itself.
   */
  long?: ((row: T) => LongText | null | undefined) | undefined;
  /** The full text of a cell whose node is not plain text, shown on hover when a one-line table cuts the cell (UX-72). */
  title?: ((row: T) => string) | undefined;
  /**
   * How a one-line table sizes the column (RES18, UX-72). `fit`: as wide as its widest value, never cut (dates, times,
   * counts, a result, a short id). `grow`: shares the width that is left, cut with an ellipsis, full text on hover.
   * Without one: `grow` for a column with `long` text, else `fit`.
   */
  kind?: ColumnKind | undefined;
  /**
   * The share of the free width a grow column takes (D102): `2` takes twice what a column with `1` takes. Default 1.
   * Giving it makes the column `grow`. Long names (a repository, a workflow) get a bigger share than a branch.
   */
  grow?: number | undefined;
  /** A grow column whose text is cut in the middle (a branch): the start and the last characters stay. */
  middle?: ((row: T) => string) | undefined;
  /** With the table's `fold="lines"`: the line of the phone row this cell is on (1 or 2); the cells of a line sit side by side. */
  line?: 1 | 2 | undefined;
}

export type ColumnKind = 'fit' | 'grow';

/** The kind of a column: its own, else `grow` for long text and `fit` for everything else. */
export function columnKind<T>(c: Pick<Column<T>, 'kind' | 'long' | 'grow'>): ColumnKind {
  return c.kind ?? (c.long || c.grow !== undefined ? 'grow' : 'fit');
}

/** The share of the free width of a grow column: its `grow` weight when it is a positive number, else 1. */
export function columnWeight<T>(c: Pick<Column<T>, 'kind' | 'long' | 'grow'>): number {
  return columnKind(c) === 'grow' && c.grow !== undefined && c.grow > 0 ? c.grow : 1;
}

/** The weight as the custom property the one-line CSS reads; only a grow column that is not 1 sets it. */
function weightStyle<T>(c: Column<T>, oneLine: boolean): CSSProperties | undefined {
  const w = columnWeight(c);
  return oneLine && w !== 1 ? ({ '--ds-grow': w } as CSSProperties) : undefined;
}

const MIDDLE_TAIL = 6;
/** The longest a branch is shown whole in a one-line table; a longer one is shortened in the middle (component 56). */
const MIDDLE_MAX = 20;

/** The two parts of a text cut in the middle: the start that may be cut, and the end that stays. */
export function splitMiddle(text: string, tail = MIDDLE_TAIL): [string, string] {
  if (text.length <= tail * 2) return [text, ''];
  return [text.slice(0, text.length - tail), text.slice(text.length - tail)];
}

/**
 * Text that a one-line table may cut (RES18, UX-72): the start is cut with an ellipsis, the end never. Give `tail` for
 * a fixed end (a run number, a time) or `middle` for a plain text cut in the middle (a branch, keeping its last
 * characters). The page passes data; it never draws the cut.
 */
export function Cut({
  children,
  tail,
  middle,
}: {
  children?: ReactNode;
  tail?: string | undefined;
  middle?: string | undefined;
}) {
  // A branch is shortened in the text itself (component 56), never cut by the style: the full name is the hover text.
  if (middle !== undefined) return <ShortText text={middle} max={MIDDLE_MAX} />;
  return (
    <span className="ds-cut">
      <span className="ds-cut__head">{children}</span>
      <span className="ds-cut__tail">{tail}</span>
    </span>
  );
}

/** The row action of a table (component 57): the same words for the same action in every list (UX-72). */
export interface RowActionSpec<T> {
  /** One of the Row action words of content.md. */
  label: string;
  /** The page it opens; null leaves the row without the button. Leave it out for an action that is a press. */
  to?: ((row: T) => string | null) | undefined;
  /**
   * An action that acts on the row and opens no page (Remove): the press, or null to leave the row without the
   * button. The word is still `label`, so it never wraps (UX-72).
   */
  press?: ((row: T) => RowPress | null) | undefined;
  /** What it opens or acts on, for a screen reader. */
  name: (row: T) => string;
}

/** The press of a row action that opens no page. */
export interface RowPress {
  onPress: () => void;
  kind?: 'default' | 'danger' | undefined;
  disabled?: boolean | undefined;
  /** Why it is disabled, shown next to the button. */
  reason?: string | undefined;
}

export interface LongText {
  full: string;
  short?: string | undefined;
}

/** The first clause of a long text: up to the first " (" or ": ", cut at `max` characters with an ellipsis. */
export function summarize(text: string, max = 60): string {
  const flat = text.replace(/\s+/g, ' ').trim();
  const cut = [flat.indexOf(' ('), flat.indexOf(': ')].filter((i) => i > 0);
  const head = cut.length > 0 ? flat.slice(0, Math.min(...cut)) : flat;
  return head.length > max ? `${head.slice(0, max - 1).trimEnd()}…` : head;
}

/** At most `max` items, then a count of the rest; the full list stays in text for a screen reader. */
export function CappedList({
  items,
  max = 2,
}: {
  items: readonly string[];
  max?: number;
}) {
  const more = items.length - max;
  return (
    <>
      <span aria-hidden="true">
        {items.slice(0, max).join(', ')}
        {more > 0 ? ` +${more}` : ''}
      </span>
      <span className="ds-visually-hidden">{items.join(', ')}</span>
    </>
  );
}

const LONG_AT = 40;

/** The hover text of a cut cell: the column's own full text, else the cell when it is plain text. */
function cellTitle<T>(c: Column<T>, row: T): string | undefined {
  if (c.title) return c.title(row);
  const v = c.cell(row);
  return typeof v === 'string' && v.length > 0 ? v : undefined;
}

export function Table<T>({
  caption,
  columns,
  rows,
  rowKey,
  onOpen,
  selected,
  onToggle,
  toggleLabel,
  inactive,
  title,
  expandLabel = 'Details',
  rowLabel,
  detail,
  fold,
  compact = false,
  action,
  oneLine = false,
  current,
  onSelect,
  onPick,
}: {
  caption: string;
  /** Draws the table inside a pane with this title and the row count; leave out when the page already has one. */
  title?: string | undefined;
  columns: readonly Column<T>[];
  rows: readonly T[];
  rowKey: (row: T) => string;
  onOpen?: ((row: T) => void) | undefined;
  /** Keys of the chosen rows, with `onToggle` to change one (a choice list). */
  selected?: ReadonlySet<string> | undefined;
  onToggle?: ((key: string) => void) | undefined;
  toggleLabel?: ((row: T) => string) | undefined;
  /** A row the screen shows in grey because it cannot be used now (UX-70): text and links grey, links underlined; its reason sits in the row. */
  inactive?: ((row: T) => boolean) | undefined;
  /** The word on the button of a column's `long` text, for example "Full error" or "Details". */
  expandLabel?: string | undefined;
  /** Names a row for the region the button opens (a screen reader reads it with the button's word). */
  rowLabel?: ((row: T) => string) | undefined;
  /**
   * A row that opens in place to more than text: a last column holds a button (`label`) that opens a full-width row
   * under it with `render(row)` (a run opens to its jobs). The button's name carries `rowLabel`.
   */
  detail?:
    | {
        label: string;
        render: (row: T) => ReactNode;
        /**
         * The row itself opens it, with a click or Enter, and there is no button column: a dense list of one-line rows
         * (the queue). The row is announced as expandable (`aria-expanded`) and stays reachable by keyboard.
         */
        rowOpen?: boolean | undefined;
      }
    | undefined;
  /**
   * `lines`: at phone width a row is two lines, not a stack of "name value" pairs: the cells with `line: 1` side by
   * side on the first line, the cells with `line: 2` (muted) on the second. Other tables keep the stack.
   */
  fold?: 'lines' | undefined;
  /**
   * With `oneLine`: a dense list (the queue). A row is one line of text high, not a button high; a value that does not
   * fit wraps and its row grows. Other tables keep the button-high row.
   */
  compact?: boolean | undefined;
  /** A last column with the row action (component 57). */
  action?: RowActionSpec<T> | undefined;
  /** Every cell is one line, cut with an ellipsis, and every row has one height (UX-72). */
  oneLine?: boolean | undefined;
  /** The key of the row a details panel shows: it is marked (`aria-current`) so it can be told from the others. */
  current?: string | undefined;
  /** A row was pressed or took focus: the screen shows it in a details panel. */
  onSelect?: ((row: T) => void) | undefined;
  /** A click on the row (not a focus): a list whose rows open an inspector opens it here (LOOK62). */
  onPick?: ((row: T) => void) | undefined;
}) {
  const base = useId();
  const [open, setOpen] = useState<ReadonlySet<string>>(new Set());
  const toggleOpen = (key: string) =>
    setOpen((o) => {
      const n = new Set(o);
      if (!n.delete(key)) n.add(key);
      return n;
    });
  useKeyHints(
    rows.length > 0
      ? [
          { keys: 'j / k', does: 'Move' },
          ...(onOpen || detail?.rowOpen ? [{ keys: 'Enter', does: 'Open' }] : []),
        ]
      : [],
  );
  const [sortBy, setSortBy] = useState<{ id: string; dir: 1 | -1 } | null>(null);
  const sorted = useMemo(() => {
    const col = columns.find((c) => c.id === sortBy?.id);
    if (!col?.sort || !sortBy) return rows;
    const get = col.sort;
    return [...rows].sort((a, b) => {
      const x = get(a);
      const y = get(b);
      return (x < y ? -1 : x > y ? 1 : 0) * sortBy.dir;
    });
  }, [rows, columns, sortBy]);
  const table = (
    <div className="ds-table-frame">
      <table
        className="ds-table"
        data-one-line={oneLine || undefined}
        data-fold={fold}
        data-compact={(oneLine && compact) || undefined}
      >
        <caption className="ds-visually-hidden">{caption}</caption>
        <thead>
          <tr>
            {onToggle ? <th scope="col" className="ds-table__pick" /> : null}
            {columns.map((c) => (
              <th
                key={c.id}
                scope="col"
                data-kind={oneLine ? columnKind(c) : undefined}
                style={weightStyle(c, oneLine)}
                aria-sort={
                  sortBy?.id === c.id
                    ? sortBy.dir === 1
                      ? 'ascending'
                      : 'descending'
                    : undefined
                }
              >
                {c.sort ? (
                  <button
                    type="button"
                    className="ds-table__sort"
                    onClick={() =>
                      setSortBy((s) =>
                        s?.id === c.id
                          ? { id: c.id, dir: s.dir === 1 ? -1 : 1 }
                          : { id: c.id, dir: 1 },
                      )
                    }
                  >
                    {c.header}
                    {sortBy?.id === c.id ? (sortBy.dir === 1 ? ' ▲' : ' ▼') : ''}
                  </button>
                ) : (
                  c.header
                )}
              </th>
            ))}
            {(detail && !detail.rowOpen) || action ? (
              <th scope="col" className="ds-table__action">
                <span className="ds-visually-hidden">
                  {detail?.label ?? action?.label}
                </span>
              </th>
            ) : null}
          </tr>
        </thead>
        <tbody>
          {sorted.map((row) => {
            const key = rowKey(row);
            const regionId = `${base}-${key.replace(/\s+/g, '_')}`;
            let expandedText: string | undefined;
            const isOpen = open.has(key);
            return (
              <Fragment key={key}>
                <tr
                  data-list-row
                  data-selected={selected?.has(key) || undefined}
                  data-inactive={inactive?.(row) || undefined}
                  aria-current={current === key ? 'true' : undefined}
                  tabIndex={0}
                  aria-expanded={detail?.rowOpen ? isOpen : undefined}
                  aria-controls={detail?.rowOpen ? regionId : undefined}
                  aria-label={
                    detail?.rowOpen && rowLabel
                      ? `${detail.label}: ${rowLabel(row)}`
                      : undefined
                  }
                  onClick={
                    detail?.rowOpen
                      ? (e) => {
                          // a link or button inside the row does its own thing
                          if (!(e.target as HTMLElement).closest('a, button'))
                            toggleOpen(key);
                        }
                      : onSelect || onPick
                        ? () => {
                            onSelect?.(row);
                            onPick?.(row);
                          }
                        : undefined
                  }
                  onFocus={
                    onSelect
                      ? (e) => {
                          if (e.target === e.currentTarget) onSelect(row);
                        }
                      : undefined
                  }
                  onDoubleClick={onOpen ? () => onOpen(row) : undefined}
                  onKeyDown={
                    detail?.rowOpen
                      ? (e) => {
                          if (
                            (e.key === 'Enter' || e.key === ' ') &&
                            e.target === e.currentTarget
                          ) {
                            e.preventDefault();
                            toggleOpen(key);
                          }
                        }
                      : onOpen
                        ? (e) => {
                            if (e.key === 'Enter' && e.target === e.currentTarget)
                              onOpen(row);
                          }
                        : undefined
                  }
                >
                  {onToggle ? (
                    <td className="ds-table__pick">
                      <input
                        type="checkbox"
                        className="ds-checkbox"
                        checked={selected?.has(key) ?? false}
                        aria-label={toggleLabel ? toggleLabel(row) : key}
                        onChange={() => onToggle(key)}
                      />
                    </td>
                  ) : null}
                  {columns.map((c, i) => (
                    <td
                      key={c.id}
                      data-label={c.header}
                      data-kind={oneLine ? columnKind(c) : undefined}
                      style={weightStyle(c, oneLine)}
                      data-lead={(i === 0 && fold !== 'lines') || undefined}
                      data-line={fold === 'lines' ? (c.line ?? 1) : undefined}
                      data-secondary={(i > 0 && c.secondary) || undefined}
                    >
                      <span className="ds-table__value">
                        {oneLine ? (
                          <span className="ds-table__text" title={cellTitle(c, row)}>
                            {c.middle ? (
                              <ShortText text={c.middle(row)} max={MIDDLE_MAX} />
                            ) : (
                              c.cell(row)
                            )}
                          </span>
                        ) : (
                          c.cell(row)
                        )}
                        {(() => {
                          const long = c.long?.(row);
                          if (!long) return null;
                          const shown = long.short ?? summarize(long.full);
                          const needsButton =
                            long.full.length > LONG_AT || shown !== long.full;
                          if (needsButton) expandedText = long.full;
                          return (
                            <>
                              <span
                                data-long
                                className={needsButton ? 'ds-table__clip' : undefined}
                              >
                                {needsButton ? shown : long.full}
                              </span>
                              {needsButton ? (
                                <Button
                                  onPress={() => toggleOpen(key)}
                                  expanded={isOpen}
                                  controls={regionId}
                                >
                                  {expandLabel}
                                </Button>
                              ) : null}
                            </>
                          );
                        })()}
                      </span>
                    </td>
                  ))}
                  {detail && !detail.rowOpen ? (
                    <td data-label={detail.label} className="ds-table__action">
                      <Button
                        onPress={() => toggleOpen(key)}
                        expanded={isOpen}
                        controls={regionId}
                      >
                        {detail.label}
                      </Button>
                    </td>
                  ) : null}
                  {action ? (
                    <td className="ds-table__action">
                      {(() => {
                        const to = action.to?.(row);
                        if (to) {
                          return (
                            <RowAction
                              to={to}
                              label={action.label}
                              name={action.name(row)}
                            />
                          );
                        }
                        const press = action.press?.(row);
                        return press ? (
                          <RowPressAction
                            press={press}
                            label={action.label}
                            name={action.name(row)}
                          />
                        ) : null;
                      })()}
                    </td>
                  ) : null}
                </tr>
                {isOpen && detail ? (
                  <tr className="ds-table__expanded">
                    <td
                      colSpan={
                        columns.length +
                        ((detail && !detail.rowOpen) || action ? 1 : 0) +
                        (onToggle ? 1 : 0)
                      }
                    >
                      <div
                        id={regionId}
                        role="region"
                        aria-label={
                          rowLabel ? `${detail.label}: ${rowLabel(row)}` : detail.label
                        }
                      >
                        {detail.render(row)}
                      </div>
                    </td>
                  </tr>
                ) : null}
                {isOpen && !detail && expandedText !== undefined ? (
                  <tr className="ds-table__expanded">
                    <td colSpan={columns.length + (action ? 1 : 0) + (onToggle ? 1 : 0)}>
                      <div
                        id={regionId}
                        role="region"
                        aria-label={
                          rowLabel ? `${expandLabel}: ${rowLabel(row)}` : expandLabel
                        }
                      >
                        {expandedText}
                      </div>
                    </td>
                  </tr>
                ) : null}
              </Fragment>
            );
          })}
        </tbody>
      </table>
    </div>
  );
  return title ? (
    <Pane title={title} count={rows.length}>
      {table}
    </Pane>
  ) : (
    table
  );
}

/** Component 10: long lists page. */
export function Pager({
  page,
  pages,
  onPage,
  label,
}: {
  page: number;
  pages: number;
  onPage: (page: number) => void;
  label: string;
}) {
  if (pages <= 1) return null;
  return (
    <nav className="ds-pager" aria-label={label} data-layout-group>
      <button
        type="button"
        className="ds-button"
        disabled={page <= 1}
        onClick={() => onPage(page - 1)}
      >
        Back
      </button>
      <span className="ds-pager__where">
        Page {page} of {pages}
      </span>
      <button
        type="button"
        className="ds-button"
        disabled={page >= pages}
        onClick={() => onPage(page + 1)}
      >
        Next
      </button>
    </nav>
  );
}

export function paginate<T>(rows: readonly T[], page: number, size: number): T[] {
  return rows.slice((page - 1) * size, page * size);
}
