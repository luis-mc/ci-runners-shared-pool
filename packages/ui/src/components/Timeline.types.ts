// Prop contract of the slot timeline (LOOK58, component "Timeline"). One part draws every "jobs over time" chart:
// Pool > Live, Queue Analysis, Pool Overview, the Jobs why-waiting inspector and the machine page. Screens pass data
// only; the axis, now line, bar states, legend, scroll, paging, follow and keys live in Timeline.tsx.
// Times are epoch milliseconds so the part needs no date parsing; `nowMs` is injected (an injected clock in tests).

/** `done` and `failed` are finished jobs; the other seven are the states of the owner's brief. */
export type TimelineState =
  | 'running'
  | 'waiting_runner'
  | 'waiting_room'
  | 'reserved'
  | 'forecast'
  | 'no_runner_fits'
  | 'timer_ran_out'
  | 'done'
  | 'failed';

export interface TimelineRowData {
  /** `<machine>:<slot>`; the row `<machine>:waiting` holds jobs with no slot yet. */
  key: string;
  /** "M1 slot 1". */
  label: string;
  /** Rows of one group are adjacent; the group name is a heading ("M1"). */
  group?: string | undefined;
}

export interface TimelineBarData {
  id: string;
  rowKey: string;
  state: TimelineState;
  label: string;
  sublabel?: string | undefined;
  startMs: number;
  /** Absent while the job runs: the bar ends at now. */
  endMs?: number | undefined;
  /** The forecast range of the end, drawn as a bracket. */
  endLoMs?: number | undefined;
  endHiMs?: number | undefined;
  /** The light part before the bar: the job waited from here. */
  waitFromMs?: number | undefined;
  /** The bar in words, for the tooltip, the screen reader and the table ("waits for room, starts about 14:16"). */
  detail: string;
  onOpen?: (() => void) | undefined;
}

export interface TimelineProps {
  /** What it shows: "Jobs of the pool". */
  label: string;
  rows: readonly TimelineRowData[];
  bars: readonly TimelineBarData[];
  nowMs: number;
  /** The earliest time loaded and the end of the forecast (never more than 24 h after now). */
  fromMs: number;
  toMs: number;
  /** True while older jobs exist that are not loaded (up to 24 h back). Scrolling to the left edge calls onLoadBefore. */
  moreBefore: boolean;
  onLoadBefore?: (() => void) | undefined;
  loadingBefore?: boolean | undefined;
  /** Rows shorter and without the legend, for the why-waiting inspector. */
  compact?: boolean | undefined;
  emptyText: string;
}
