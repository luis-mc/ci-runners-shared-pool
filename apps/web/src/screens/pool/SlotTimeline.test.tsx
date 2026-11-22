// LOOK58: the slot timeline loader: pages merge, bars map to the shared Timeline, the machine filter is sent.
import { cleanup, screen } from '@testing-library/react';
import { afterEach, describe, expect, it } from 'vitest';
import { machine, renderApp, signedIn } from '../../testing/mock';
import type { Schemas } from '../../api/api';
import { mergePages, toBars, toRows } from './SlotTimeline';

afterEach(cleanup);

const T0 = Date.parse('2026-11-22T10:00:00Z');
const at = (min: number) => new Date(T0 + min * 60_000).toISOString();
const row = (n: number): Schemas['TimelineRow'] => ({
  key: `m1:${n}`,
  machine_id: 'm1',
  machine_name: 'build-1',
  slot: n,
  label: `build-1 slot ${n}`,
});
const bar = (
  id: string,
  over: Partial<Schemas['TimelineBar']> = {},
): Schemas['TimelineBar'] => ({
  id,
  row_id: 'm1:1',
  state: 'done',
  label: id,
  start: at(-30),
  ...over,
});
const page = (over: Partial<Schemas['Timeline']> = {}): Schemas['Timeline'] => ({
  now: at(0),
  from: at(-360),
  to: at(0),
  rows: [row(1)],
  bars: [],
  more_before: false,
  ...over,
});

describe('mergePages', () => {
  it('keeps the rows once and the newest copy of a bar', () => {
    const newest = page({
      rows: [row(1), row(2)],
      bars: [bar('a', { state: 'running' })],
    });
    const older = [page({ rows: [row(1)], bars: [bar('a'), bar('b')] })];
    const m = mergePages(newest, older);
    expect(m.rows.map((r) => r.key)).toEqual(['m1:1', 'm1:2']);
    expect(m.bars.map((b) => `${b.id}:${b.state}`).sort()).toEqual([
      'a:running',
      'b:done',
    ]);
  });
});

describe('toBars and toRows', () => {
  it('turns times into milliseconds, groups rows by machine and opens a job', () => {
    const opened: number[] = [];
    const [b] = toBars(
      [bar('a', { job_id: 7, end: at(-10), wait_from: at(-40) })],
      (id) => opened.push(id),
    );
    expect(b?.startMs).toBe(T0 - 30 * 60_000);
    expect(b?.endMs).toBe(T0 - 10 * 60_000);
    expect(b?.waitFromMs).toBe(T0 - 40 * 60_000);
    b?.onOpen?.();
    expect(opened).toEqual([7]);
    expect(toRows([row(1)])[0]).toEqual({
      key: 'm1:1',
      label: 'build-1 slot 1',
      group: 'build-1',
    });
  });

  it('has no open action for a forecast with no job record', () => {
    expect(
      toBars([bar('f', { state: 'forecast' })], () => undefined)[0]?.onOpen,
    ).toBeUndefined();
  });
});

describe('SlotTimeline on the machine page', () => {
  it('asks for the slots of this machine only', async () => {
    const asked: string[] = [];
    const api = signedIn()
      .on('GET', '/api/v1/machines', { items: [machine()] })
      .on('GET', '/api/v1/machines/:id', machine())
      .on('GET', '/api/v1/timeline', (c) => {
        asked.push(c.query.get('machine_id') ?? '');
        return page({ bars: [bar('a', { label: 'unit tests' })] });
      });
    renderApp(api, '/pool/machines/m1');
    await screen.findAllByText('build-1 slot 1');
    expect(asked).toContain('m1');
  });
});
