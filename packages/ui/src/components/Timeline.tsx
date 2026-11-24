// Component 77: the slot timeline. One part draws every "jobs over time" chart: rows are runner slots grouped by
// machine, bars sit in their row by time, a now line and an axis of clock times run across, and a legend names the
// nine states with a glyph and words (a state is never colour alone, UX-58). Only the chart scrolls sideways, in its
// own focusable box (UX-49); the row labels stay at the inline start. The scale is fixed pixels per minute, so
// history is scrolled, not squeezed. The chart follows now until the reader scrolls away; `Back to now` pins it again.
// Scrolling to the left edge loads older jobs (`onLoadBefore`) and the view stays where it was. A bar with `onOpen`
// is a real button (UX-57). The text alternative is a table under "Show as table". No clock is read here: `nowMs` is
// a prop, so a test uses fixed times.
import {
  Fragment,
  useCallback,
  useId,
  useLayoutEffect,
  useRef,
  useState,
  type CSSProperties,
  type KeyboardEvent,
} from 'react';
import { Button } from './Button';
import { formatDate, formatShortDateTime, formatTime } from './format';
import { Empty } from './State';
import type { TimelineBarData, TimelineProps, TimelineState } from './Timeline.types';
import { VizAlt } from './VizAlt';

const MINUTE = 60_000;
const HOUR = 60 * MINUTE;

/** The scale: one hour is 240 px, so a bar of a minute is 4 px wide. */
export const TIMELINE_PX_PER_MIN = 4;
/** The width of the sticky row labels, in px (the CSS reads it from the `--tl-label` variable set here). */
export const TIMELINE_LABEL_PX = 144;
/** Now sits at this share of the viewport while the chart follows. */
export const TIMELINE_FOLLOW_AT = 0.75;
/** One press of an arrow key scrolls this far: one hour. */
export const TIMELINE_STEP_PX = 60 * TIMELINE_PX_PER_MIN;
/** How far from the pinned place a scroll still counts as following. */
const PIN_TOLERANCE_PX = 8;
const EDGE_PX = 1;

export const TIMELINE_STATES: Record<TimelineState, { glyph: string; word: string }> = {
  running: { glyph: '▶', word: 'running' },
  waiting_runner: { glyph: '◷', word: 'waiting for a runner' },
  waiting_room: { glyph: '▤', word: 'waiting for room' },
  reserved: { glyph: '◆', word: 'reserved' },
  forecast: { glyph: '≈', word: 'forecast' },
  no_runner_fits: { glyph: '⊘', word: 'no runner fits' },
  timer_ran_out: { glyph: '◔', word: 'timer ran out' },
  done: { glyph: '✓', word: 'done' },
  failed: { glyph: '✕', word: 'failed' },
};
const STATE_ORDER = Object.keys(TIMELINE_STATES) as TimelineState[];

export const TIMELINE_WORDS = {
  now: 'now',
  backToNow: 'Back to now',
  legend: 'States',
  loadingBefore: 'Loading older jobs',
  noMoreBefore: 'Showing the last 24 hours',
  keys: 'Left and Right arrows scroll. Home goes to now. End goes to the end of the forecast.',
  slot: 'Slot',
  job: 'Job',
  state: 'State',
  start: 'Start',
  end: 'End or forecast',
  about: 'about',
  to: 'to',
  waitedFrom: 'waited from',
} as const;

function endOf(bar: TimelineBarData, nowMs: number): number {
  if (bar.endMs !== undefined) return bar.endMs;
  if (bar.state === 'running' || bar.state === 'timer_ran_out') return nowMs;
  return bar.endHiMs ?? bar.endLoMs ?? bar.startMs;
}

function endText(bar: TimelineBarData): string {
  if (bar.endMs !== undefined) return formatShortDateTime(bar.endMs);
  if (bar.state === 'running' || bar.state === 'timer_ran_out') return TIMELINE_WORDS.now;
  if (bar.endLoMs !== undefined && bar.endHiMs !== undefined)
    return `${TIMELINE_WORDS.about} ${formatTime(bar.endLoMs)} ${TIMELINE_WORDS.to} ${formatTime(bar.endHiMs)}`;
  const one = bar.endHiMs ?? bar.endLoMs;
  return one !== undefined ? `${TIMELINE_WORDS.about} ${formatTime(one)}` : '';
}

function hourTicks(fromMs: number, toMs: number): number[] {
  const first = new Date(fromMs);
  first.setMinutes(0, 0, 0);
  let t = first.getTime();
  if (t < fromMs) t += HOUR;
  const out: number[] = [];
  for (; t <= toMs; t += HOUR) out.push(t);
  return out;
}

export function Timeline({
  label,
  rows,
  bars,
  nowMs,
  fromMs,
  toMs,
  moreBefore,
  onLoadBefore,
  loadingBefore = false,
  compact = false,
  emptyText,
}: TimelineProps) {
  const hintId = useId();
  const box = useRef<HTMLDivElement | null>(null);
  const [following, setFollowing] = useState(true);
  const prevFrom = useRef(fromMs);
  const asked = useRef(false);
  const wasLoading = useRef(false);

  const x = (ms: number): number => ((ms - fromMs) / MINUTE) * TIMELINE_PX_PER_MIN;
  const width = Math.max(0, x(toMs));

  /** The scroll position that puts now at 75 % of the viewport, kept inside the scrollable range. */
  const pinTarget = useCallback(
    (el: HTMLElement): number => {
      const want = TIMELINE_LABEL_PX + x(nowMs) - TIMELINE_FOLLOW_AT * el.clientWidth;
      const max = el.scrollWidth > 0 ? el.scrollWidth - el.clientWidth : Infinity;
      return Math.max(0, Math.min(want, max));
    },
    // x depends on fromMs
    [nowMs, fromMs],
  );

  const syncFollow = useCallback(
    (el: HTMLElement) => {
      setFollowing(Math.abs(el.scrollLeft - pinTarget(el)) <= PIN_TOLERANCE_PX);
    },
    [pinTarget],
  );

  const scrollTo = (el: HTMLElement, left: number) => {
    el.scrollLeft = Math.max(0, left);
    syncFollow(el);
  };

  // The view stays where it was when older time is added at the left, then the pin is applied while following.
  useLayoutEffect(() => {
    const el = box.current;
    if (!el) return;
    if (fromMs < prevFrom.current) {
      el.scrollLeft += ((prevFrom.current - fromMs) / MINUTE) * TIMELINE_PX_PER_MIN;
    }
    prevFrom.current = fromMs;
    if (following) el.scrollLeft = pinTarget(el);
  }, [fromMs, nowMs, toMs, following, pinTarget]);

  // A resize of the box (a rotated phone, a wider window) keeps now at the pin while following.
  useLayoutEffect(() => {
    const el = box.current;
    if (!el || !following || typeof ResizeObserver === 'undefined') return;
    const watch = new ResizeObserver(() => {
      el.scrollLeft = pinTarget(el);
    });
    watch.observe(el);
    return () => watch.disconnect();
  }, [following, pinTarget]);

  // A new load may be asked for once the earlier one ended or the start moved.
  useLayoutEffect(() => {
    asked.current = false;
  }, [fromMs]);
  useLayoutEffect(() => {
    if (wasLoading.current && !loadingBefore) asked.current = false;
    wasLoading.current = loadingBefore;
  }, [loadingBefore]);

  const maybeLoadBefore = (el: HTMLElement) => {
    if (!moreBefore || loadingBefore || asked.current || !onLoadBefore) return;
    const atEdge = el.scrollLeft <= EDGE_PX;
    const tooShort = el.scrollWidth > 0 && el.scrollWidth <= el.clientWidth;
    if (atEdge || tooShort) {
      asked.current = true;
      onLoadBefore();
    }
  };

  // Content that does not fill the viewport cannot be scrolled to its edge, so it asks for older jobs at once.
  useLayoutEffect(() => {
    if (box.current) maybeLoadBefore(box.current);
  }, [fromMs, moreBefore, loadingBefore, width]);

  if (rows.length === 0 || bars.length === 0) return <Empty title={emptyText} />;

  const onKeyDown = (e: KeyboardEvent<HTMLDivElement>) => {
    if (e.target !== e.currentTarget) return;
    const el = e.currentTarget;
    if (e.key === 'ArrowLeft') scrollTo(el, el.scrollLeft - TIMELINE_STEP_PX);
    else if (e.key === 'ArrowRight') scrollTo(el, el.scrollLeft + TIMELINE_STEP_PX);
    else if (e.key === 'Home') {
      setFollowing(true);
      el.scrollLeft = pinTarget(el);
    } else if (e.key === 'End') {
      const max = el.scrollWidth > 0 ? el.scrollWidth - el.clientWidth : width;
      scrollTo(el, max);
    } else return;
    e.preventDefault();
  };

  const rowKeys = new Set(rows.map((r) => r.key));
  const barsOf = (key: string) => bars.filter((b) => b.rowKey === key);
  const ticks = hourTicks(fromMs, toMs);
  const nowX = x(nowMs);

  return (
    <div className="ds-timeline" data-compact={compact || undefined} data-layout-group>
      <div className="ds-timeline__bar">
        {following ? null : (
          <Button
            onPress={() => {
              const el = box.current;
              setFollowing(true);
              if (el) el.scrollLeft = pinTarget(el);
            }}
          >
            {TIMELINE_WORDS.backToNow}
          </Button>
        )}
        {loadingBefore ? (
          <span className="ds-timeline__note" role="status">
            {TIMELINE_WORDS.loadingBefore}
          </span>
        ) : !moreBefore ? (
          <span className="ds-timeline__note">{TIMELINE_WORDS.noMoreBefore}</span>
        ) : null}
      </div>
      <div
        ref={box}
        className="ds-timeline__scroll"
        role="region"
        aria-label={label}
        aria-describedby={hintId}
        tabIndex={0}
        onScroll={(e) => {
          syncFollow(e.currentTarget);
          maybeLoadBefore(e.currentTarget);
        }}
        onKeyDown={onKeyDown}
      >
        <div
          className="ds-timeline__canvas"
          style={
            {
              '--tl-label': `${TIMELINE_LABEL_PX}px`,
              '--tl-width': `${width}px`,
            } as CSSProperties
          }
        >
          <div className="ds-timeline__axis" aria-hidden="true">
            <span className="ds-timeline__label" />
            <span className="ds-timeline__track">
              {ticks.map((t) => (
                <span
                  key={t}
                  className="ds-timeline__tick"
                  style={{ insetInlineStart: `${x(t)}px` }}
                >
                  {new Date(t).getHours() === 0
                    ? `${formatDate(t)} ${formatTime(t)}`
                    : formatTime(t)}
                </span>
              ))}
              <span
                className="ds-timeline__now-label"
                style={{ insetInlineStart: `${nowX}px` }}
              >
                {TIMELINE_WORDS.now} {formatTime(nowMs)}
              </span>
            </span>
          </div>
          <ul className="ds-timeline__rows">
            {rows.map((row, i) => {
              const newGroup =
                row.group !== undefined && row.group !== rows[i - 1]?.group;
              return (
                <Fragment key={row.key}>
                  {newGroup ? (
                    <li className="ds-timeline__group">
                      <span className="ds-timeline__group-name">{row.group}</span>
                    </li>
                  ) : null}
                  <li className="ds-timeline__row" data-row={row.key}>
                    <span className="ds-timeline__label">{row.label}</span>
                    <span className="ds-timeline__track">
                      {barsOf(row.key).map((bar) => {
                        const start = x(bar.startMs);
                        const end = x(endOf(bar, nowMs));
                        const wait =
                          bar.waitFromMs !== undefined && bar.waitFromMs < bar.startMs
                            ? x(bar.waitFromMs)
                            : undefined;
                        const s = TIMELINE_STATES[bar.state];
                        const body = (
                          <>
                            <span className="ds-timeline__glyph" aria-hidden="true">
                              {s.glyph}
                            </span>
                            <span className="ds-timeline__bar-text" aria-hidden="true">
                              {bar.label}
                            </span>
                            <span className="ds-visually-hidden">
                              {[bar.label, bar.sublabel, s.word, bar.detail]
                                .filter(Boolean)
                                .join(', ')}
                            </span>
                          </>
                        );
                        const place = {
                          insetInlineStart: `${start}px`,
                          inlineSize: `${Math.max(0, end - start)}px`,
                        };
                        return (
                          <Fragment key={bar.id}>
                            {wait !== undefined ? (
                              <span
                                className="ds-timeline__wait"
                                aria-hidden="true"
                                style={{
                                  insetInlineStart: `${wait}px`,
                                  inlineSize: `${start - wait}px`,
                                }}
                              />
                            ) : null}
                            {bar.onOpen ? (
                              <button
                                type="button"
                                className="ds-timeline__item"
                                data-state={bar.state}
                                data-list-row
                                title={bar.detail}
                                style={place}
                                onClick={bar.onOpen}
                              >
                                {body}
                              </button>
                            ) : (
                              <span
                                className="ds-timeline__item"
                                data-state={bar.state}
                                title={bar.detail}
                                style={place}
                              >
                                {body}
                              </span>
                            )}
                            {bar.endLoMs !== undefined && bar.endHiMs !== undefined ? (
                              <span
                                className="ds-timeline__bracket"
                                aria-hidden="true"
                                style={{
                                  insetInlineStart: `${x(bar.endLoMs)}px`,
                                  inlineSize: `${Math.max(2, x(bar.endHiMs) - x(bar.endLoMs))}px`,
                                }}
                              />
                            ) : null}
                          </Fragment>
                        );
                      })}
                      <span
                        className="ds-timeline__now"
                        aria-hidden="true"
                        style={{ insetInlineStart: `${nowX}px` }}
                      />
                    </span>
                  </li>
                </Fragment>
              );
            })}
          </ul>
        </div>
      </div>
      <p id={hintId} className="ds-timeline__hint">
        {TIMELINE_WORDS.keys}
      </p>
      {compact ? null : (
        <ul className="ds-timeline__legend" aria-label={TIMELINE_WORDS.legend}>
          {STATE_ORDER.map((st) => (
            <li key={st} className="ds-timeline__legend-item" data-legend={st}>
              <span className="ds-timeline__swatch" data-state={st} aria-hidden="true" />
              <span className="ds-timeline__glyph" aria-hidden="true">
                {TIMELINE_STATES[st].glyph}
              </span>
              <span>{TIMELINE_STATES[st].word}</span>
            </li>
          ))}
        </ul>
      )}
      <VizAlt
        caption={label}
        head={[
          TIMELINE_WORDS.slot,
          TIMELINE_WORDS.job,
          TIMELINE_WORDS.state,
          TIMELINE_WORDS.start,
          TIMELINE_WORDS.end,
        ]}
        rows={bars
          .filter((b) => rowKeys.has(b.rowKey))
          .map((b) => [
            rows.find((r) => r.key === b.rowKey)?.label ?? b.rowKey,
            b.label,
            `${TIMELINE_STATES[b.state].word}, ${b.detail}`,
            formatShortDateTime(b.startMs),
            endText(b),
          ])}
      />
    </div>
  );
}
