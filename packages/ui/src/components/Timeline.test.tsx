// @vitest-environment jsdom
// LOOK58b: the slot timeline. jsdom has no layout, so scroll sizes are stubbed with Object.defineProperty.
import { cleanup, fireEvent, render, screen } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import {
  TIMELINE_FOLLOW_AT,
  TIMELINE_LABEL_PX,
  TIMELINE_PX_PER_MIN,
  TIMELINE_STATES,
  TIMELINE_STEP_PX,
  Timeline,
} from './Timeline';
import type { TimelineBarData, TimelineProps, TimelineState } from './Timeline.types';

afterEach(cleanup);

const MIN = 60_000;
const NOW = Date.UTC(2026, 10, 22, 12, 0, 0);
const FROM = NOW - 24 * 60 * MIN;
const TO = NOW + 6 * 60 * MIN;
const px = (ms: number, from = FROM) => ((ms - from) / MIN) * TIMELINE_PX_PER_MIN;
const CLIENT = 800;
const SCROLL_W = TIMELINE_LABEL_PX + px(TO);
const PIN = TIMELINE_LABEL_PX + px(NOW) - TIMELINE_FOLLOW_AT * CLIENT;

const rows = [
  { key: 'm1:1', label: 'M1 slot 1', group: 'M1' },
  { key: 'm1:2', label: 'M1 slot 2', group: 'M1' },
  { key: 'm2:1', label: 'M2 slot 1', group: 'M2' },
];
const ALL = Object.keys(TIMELINE_STATES) as TimelineState[];
const bars: TimelineBarData[] = ALL.map((state, i) => ({
  id: `b${i}`,
  rowKey: rows[i % 3]!.key,
  state,
  label: `job ${state}`,
  startMs: NOW - (60 - i * 5) * MIN,
  endMs:
    state === 'running' || state === 'forecast' ? undefined : NOW - (50 - i * 5) * MIN,
  detail: `detail of ${state}`,
}));

function props(over: Partial<TimelineProps> = {}): TimelineProps {
  return {
    label: 'Jobs of the pool',
    rows,
    bars,
    nowMs: NOW,
    fromMs: FROM,
    toMs: TO,
    moreBefore: true,
    emptyText: 'No jobs yet',
    ...over,
  };
}

// jsdom has no layout: scroll sizes and scrollLeft live on the prototype, so they exist before the first effect.
const lefts = new WeakMap<Element, number>();
const saved = Object.getOwnPropertyDescriptors(Element.prototype);
function stubLayout() {
  Object.defineProperty(Element.prototype, 'clientWidth', {
    configurable: true,
    get: () => CLIENT,
  });
  Object.defineProperty(Element.prototype, 'scrollWidth', {
    configurable: true,
    get: () => SCROLL_W,
  });
  Object.defineProperty(Element.prototype, 'scrollLeft', {
    configurable: true,
    get(this: Element) {
      return lefts.get(this) ?? 0;
    },
    set(this: Element, v: number) {
      lefts.set(this, v);
    },
  });
}
afterEach(() => {
  for (const k of ['clientWidth', 'scrollWidth', 'scrollLeft'] as const) {
    const d = saved[k];
    if (d) Object.defineProperty(Element.prototype, k, d);
  }
});
const region = () => screen.getByRole('region', { name: 'Jobs of the pool' });
function mount(over: Partial<TimelineProps> = {}) {
  stubLayout();
  return render(<Timeline {...props(over)} />);
}
function again(view: ReturnType<typeof render>, over: Partial<TimelineProps> = {}) {
  view.rerender(<Timeline {...props(over)} />);
}

describe('rendering', () => {
  it('draws a row per slot under its group and a bar per job in its row', () => {
    render(<Timeline {...props()} />);
    expect(screen.getAllByText('M1').length).toBe(1);
    expect(screen.getByText('M2')).toBeTruthy();
    const row = document.querySelector('[data-row="m1:1"]')!;
    expect(row.querySelectorAll('.ds-timeline__item').length).toBe(3);
    expect(document.querySelectorAll('.ds-timeline__row').length).toBe(3);
  });

  it('puts a bar by its start and end, and a running bar ends at now', () => {
    render(<Timeline {...props()} />);
    const done = document.querySelector<HTMLElement>(
      '[data-state="done"].ds-timeline__item',
    )!;
    expect(done.style.insetInlineStart).toBe(`${px(bars[7]!.startMs)}px`);
    expect(done.style.inlineSize).toBe(`${px(bars[7]!.endMs!) - px(bars[7]!.startMs)}px`);
    const run = document.querySelector<HTMLElement>(
      '[data-state="running"].ds-timeline__item',
    )!;
    expect(run.style.inlineSize).toBe(`${px(NOW) - px(bars[0]!.startMs)}px`);
  });

  it('draws the light wait part and the forecast bracket', () => {
    const b: TimelineBarData = {
      id: 'f',
      rowKey: 'm1:1',
      state: 'forecast',
      label: 'next',
      startMs: NOW + 10 * MIN,
      endLoMs: NOW + 20 * MIN,
      endHiMs: NOW + 30 * MIN,
      waitFromMs: NOW - 5 * MIN,
      detail: 'starts about 12:10',
    };
    render(<Timeline {...props({ bars: [b] })} />);
    expect(document.querySelector('.ds-timeline__wait')).toBeTruthy();
    const br = document.querySelector<HTMLElement>('.ds-timeline__bracket')!;
    expect(br.style.inlineSize).toBe(`${10 * TIMELINE_PX_PER_MIN}px`);
  });

  it('places the now line at the scale and shows its time on the axis', () => {
    render(<Timeline {...props()} />);
    const line = document.querySelector<HTMLElement>('.ds-timeline__now')!;
    expect(line.style.insetInlineStart).toBe(`${px(NOW)}px`);
    expect(document.querySelector('.ds-timeline__now-label')!.textContent).toContain(
      'now',
    );
    expect(document.querySelectorAll('.ds-timeline__tick').length).toBeGreaterThan(24);
  });

  it('sets the content width from the range, 240 px an hour', () => {
    render(<Timeline {...props()} />);
    const canvas = document.querySelector<HTMLElement>('.ds-timeline__canvas')!;
    expect(canvas.style.getPropertyValue('--tl-width')).toBe(`${30 * 240}px`);
  });

  it('lists the nine states with a glyph and words in the legend', () => {
    render(<Timeline {...props()} />);
    expect(document.querySelectorAll('.ds-timeline__legend-item').length).toBe(9);
    for (const st of ALL) {
      const item = document.querySelector(`[data-legend="${st}"]`)!;
      expect(item.textContent).toContain(TIMELINE_STATES[st].glyph);
      expect(item.textContent).toContain(TIMELINE_STATES[st].word);
    }
    expect(new Set(ALL.map((s) => TIMELINE_STATES[s].glyph)).size).toBe(9);
  });

  it('gives every bar its state word and detail for a screen reader', () => {
    render(<Timeline {...props()} />);
    expect(
      screen.getAllByText(/waiting for room, detail of waiting_room/).length,
    ).toBeGreaterThan(0);
  });

  it('makes a bar with onOpen a real button', () => {
    const open = vi.fn();
    const withOpen = bars.map((b, i) => (i === 0 ? { ...b, onOpen: open } : b));
    render(<Timeline {...props({ bars: withOpen })} />);
    const btn = document.querySelector<HTMLButtonElement>('button.ds-timeline__item')!;
    expect(btn.hasAttribute('data-list-row')).toBe(true);
    fireEvent.click(btn);
    expect(open).toHaveBeenCalledTimes(1);
    expect(document.querySelectorAll('button.ds-timeline__item').length).toBe(1);
  });

  it('folds a table with slot, job, state, start and end or forecast', () => {
    render(<Timeline {...props()} />);
    expect(screen.getByText('Show as table')).toBeTruthy();
    const heads = Array.from(
      document.querySelectorAll('.ds-viz-table th[scope="col"]'),
    ).map((h) => h.textContent);
    expect(heads).toEqual(['Slot', 'Job', 'State', 'Start', 'End or forecast']);
    expect(document.querySelectorAll('.ds-viz-table tbody tr').length).toBe(9);
  });

  it('says about when a forecast has a range', () => {
    const b: TimelineBarData = {
      id: 'f',
      rowKey: 'm1:1',
      state: 'forecast',
      label: 'next',
      startMs: NOW + 10 * MIN,
      endLoMs: NOW + 20 * MIN,
      endHiMs: NOW + 30 * MIN,
      detail: 'starts about 12:10',
    };
    render(<Timeline {...props({ bars: [b] })} />);
    expect(screen.getByText(/^about \d\d:\d\d to \d\d:\d\d$/)).toBeTruthy();
  });

  it('shortens the rows and hides the legend when compact', () => {
    render(<Timeline {...props({ compact: true })} />);
    expect(document.querySelector('.ds-timeline')!.getAttribute('data-compact')).toBe(
      'true',
    );
    expect(document.querySelector('.ds-timeline__legend')).toBeNull();
  });

  it('shows the empty text with no rows or no bars', () => {
    const v = render(<Timeline {...props({ rows: [] })} />);
    expect(screen.getByText('No jobs yet')).toBeTruthy();
    v.unmount();
    render(<Timeline {...props({ bars: [] })} />);
    expect(screen.getByText('No jobs yet')).toBeTruthy();
    expect(screen.queryByRole('region')).toBeNull();
  });

  it('says it shows 24 hours when nothing older exists, and that it loads while loading', () => {
    const v = render(<Timeline {...props({ moreBefore: false })} />);
    expect(screen.getByText('Showing the last 24 hours')).toBeTruthy();
    v.unmount();
    render(<Timeline {...props({ loadingBefore: true })} />);
    expect(screen.getByText('Loading older jobs')).toBeTruthy();
    expect(screen.queryByText('Showing the last 24 hours')).toBeNull();
  });
});

describe('scroll box, follow and keys', () => {
  it('is a focusable region with a name and a key hint', () => {
    render(<Timeline {...props()} />);
    expect(region().getAttribute('tabindex')).toBe('0');
    const hint = document.getElementById(region().getAttribute('aria-describedby')!)!;
    expect(hint.textContent).toContain('Home');
  });

  it('pins now at 75 % of the viewport and keeps it there as now advances', () => {
    const v = mount();
    again(v);
    expect(region().scrollLeft).toBeCloseTo(PIN);
    again(v, { nowMs: NOW + 10 * MIN, toMs: TO + 10 * MIN });
    expect(region().scrollLeft).toBeCloseTo(PIN + 10 * TIMELINE_PX_PER_MIN);
    expect(screen.queryByText('Back to now')).toBeNull();
  });

  it('stops following when the reader scrolls away and shows Back to now', () => {
    const v = mount();
    again(v);
    region().scrollLeft = 1000;
    fireEvent.scroll(region());
    expect(screen.getByText('Back to now')).toBeTruthy();
    again(v, { nowMs: NOW + 10 * MIN, toMs: TO + 10 * MIN });
    expect(region().scrollLeft).toBe(1000);
  });

  it('keeps following when a scroll lands on the pinned place', () => {
    const v = mount();
    again(v);
    region().scrollLeft = PIN + 3;
    fireEvent.scroll(region());
    expect(screen.queryByText('Back to now')).toBeNull();
  });

  it('pins again on Back to now and the button goes away', () => {
    const v = mount();
    again(v);
    region().scrollLeft = 1000;
    fireEvent.scroll(region());
    fireEvent.click(screen.getByText('Back to now'));
    expect(region().scrollLeft).toBeCloseTo(PIN);
    expect(screen.queryByText('Back to now')).toBeNull();
  });

  it('Home re-pins and End goes to the end of the forecast', () => {
    const v = mount();
    again(v);
    region().scrollLeft = 500;
    fireEvent.scroll(region());
    fireEvent.keyDown(region(), { key: 'Home' });
    expect(region().scrollLeft).toBeCloseTo(PIN);
    fireEvent.keyDown(region(), { key: 'End' });
    expect(region().scrollLeft).toBe(SCROLL_W - CLIENT);
    expect(screen.getByText('Back to now')).toBeTruthy();
  });

  it('scrolls one step on the arrow keys and never below zero', () => {
    const v = mount();
    again(v);
    region().scrollLeft = 1000;
    fireEvent.keyDown(region(), { key: 'ArrowRight' });
    expect(region().scrollLeft).toBe(1000 + TIMELINE_STEP_PX);
    fireEvent.keyDown(region(), { key: 'ArrowLeft' });
    fireEvent.keyDown(region(), { key: 'ArrowLeft' });
    expect(region().scrollLeft).toBe(1000 - TIMELINE_STEP_PX);
    region().scrollLeft = 100;
    fireEvent.keyDown(region(), { key: 'ArrowLeft' });
    expect(region().scrollLeft).toBe(0);
  });
});

describe('paging', () => {
  it('asks for older jobs once at the left edge, not while loading', () => {
    const load = vi.fn();
    const v = mount({ onLoadBefore: load });
    region().scrollLeft = 0;
    fireEvent.scroll(region());
    fireEvent.scroll(region());
    expect(load).toHaveBeenCalledTimes(1);
    again(v, { onLoadBefore: load, loadingBefore: true });
    fireEvent.scroll(region());
    expect(load).toHaveBeenCalledTimes(1);
  });

  it('asks again after the load ended', () => {
    const load = vi.fn();
    const v = mount({ onLoadBefore: load });
    region().scrollLeft = 0;
    fireEvent.scroll(region());
    again(v, { onLoadBefore: load, loadingBefore: true });
    again(v, { onLoadBefore: load, loadingBefore: false });
    fireEvent.scroll(region());
    expect(load).toHaveBeenCalledTimes(2);
  });

  it('does not ask away from the edge or with nothing older', () => {
    const load = vi.fn();
    const v = mount({ onLoadBefore: load });
    region().scrollLeft = 300;
    fireEvent.scroll(region());
    again(v, { onLoadBefore: load, moreBefore: false });
    region().scrollLeft = 0;
    fireEvent.scroll(region());
    expect(load).not.toHaveBeenCalled();
  });

  it('keeps the view anchored when older time is added on the left', () => {
    const v = mount();
    region().scrollLeft = 1000;
    fireEvent.scroll(region());
    const added = 6 * 60 * MIN;
    again(v, { fromMs: FROM - added });
    expect(region().scrollLeft).toBe(1000 + (added / MIN) * TIMELINE_PX_PER_MIN);
  });
});
