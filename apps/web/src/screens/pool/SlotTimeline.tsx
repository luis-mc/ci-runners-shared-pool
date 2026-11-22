// The one "Jobs over time" chart of the console (LOOK58): machine runner slots as rows, past, running and forecast
// jobs as bars. Pool > Live, Queue Analysis, Pool Overview, the Jobs why-waiting inspector and the machine page all
// mount this one part; the drawing is the shared Timeline of @crsp/ui, and this file only loads and pages the data.
import { Timeline, useNow, type TimelineBarData, type TimelineRowData } from '@crsp/ui';
import { useCallback, useEffect, useMemo, useState } from 'react';
import { useNavigate } from 'react-router-dom';
import { unwrap, useClient, type Schemas } from '../../api/api';
import { useLoad } from '../../api/hooks';
import { useRefreshTick } from '../../api/machines';
import { Load } from '../common';

type Page = Schemas['Timeline'];

const ms = (t: string | undefined): number | undefined => (t ? Date.parse(t) : undefined);

/** Rows of every page, in the order of the newest page first; bars by id, the newest page wins. */
export function mergePages(newest: Page, older: readonly Page[]) {
  const rows = new Map<string, Schemas['TimelineRow']>();
  const bars = new Map<string, Schemas['TimelineBar']>();
  for (const p of [...older].reverse().concat(newest)) {
    for (const r of p.rows) rows.set(r.key, r);
    for (const b of p.bars) bars.set(b.id, b);
  }
  const order = [...newest.rows, ...older.flatMap((p) => p.rows)];
  const seen = new Set<string>();
  const list: Schemas['TimelineRow'][] = [];
  for (const r of order) {
    if (seen.has(r.key)) continue;
    seen.add(r.key);
    list.push(rows.get(r.key) ?? r);
  }
  return { rows: list, bars: [...bars.values()] };
}

export function toRows(rows: readonly Schemas['TimelineRow'][]): TimelineRowData[] {
  return rows.map((r) => ({ key: r.key, label: r.label, group: r.machine_name }));
}

export function toBars(
  bars: readonly Schemas['TimelineBar'][],
  open: (jobId: number) => void,
): TimelineBarData[] {
  return bars.map((b) => ({
    id: b.id,
    rowKey: b.row_id,
    state: b.state,
    label: b.label,
    sublabel: b.repository,
    startMs: Date.parse(b.start),
    endMs: ms(b.end),
    endLoMs: ms(b.end_lo),
    endHiMs: ms(b.end_hi),
    waitFromMs: ms(b.wait_from),
    detail: b.detail ?? b.state,
    onOpen: b.job_id === undefined ? undefined : () => open(b.job_id as number),
  }));
}

export interface SlotTimelineProps {
  /** Only the slots and jobs of this machine. */
  machineId?: string | undefined;
  compact?: boolean | undefined;
  label?: string | undefined;
}

export function SlotTimeline({
  machineId,
  compact,
  label = 'Jobs of the pool',
}: SlotTimelineProps) {
  const client = useClient();
  const navigate = useNavigate();
  const nowMs = useNow();
  const tick = useRefreshTick();
  const head = useLoad(
    () =>
      unwrap(
        client.GET('/api/v1/timeline', {
          params: { query: machineId ? { machine_id: machineId } : {} },
        }),
      ),
    [client, machineId, tick],
  );
  const [older, setOlder] = useState<Page[]>([]);
  const [loading, setLoading] = useState(false);
  useEffect(() => setOlder([]), [machineId]);

  const headData = head.result.state === 'loading' ? undefined : head.result.data;
  const earliest = older.length ? older[older.length - 1]! : headData;
  const loadBefore = useCallback(() => {
    if (!earliest || loading) return;
    setLoading(true);
    unwrap(
      client.GET('/api/v1/timeline', {
        params: {
          query: {
            before: earliest.from,
            ...(machineId ? { machine_id: machineId } : {}),
          },
        },
      }),
    )
      .then((p) => setOlder((o) => [...o, p]))
      .catch(() => undefined)
      .finally(() => setLoading(false));
  }, [client, earliest, loading, machineId]);

  const open = useCallback((id: number) => void navigate(`/pool/jobs/${id}`), [navigate]);
  const merged = useMemo(
    () => (headData ? mergePages(headData, older) : undefined),
    [headData, older],
  );
  return (
    <Load result={head.result} what="the jobs over time" reload={head.reload}>
      {(d) => {
        const m = merged ?? mergePages(d, []);
        const last = older.length ? older[older.length - 1]! : d;
        return (
          <Timeline
            label={label}
            rows={toRows(m.rows)}
            bars={toBars(m.bars, open)}
            nowMs={nowMs}
            fromMs={Date.parse(last.from)}
            toMs={Math.max(Date.parse(d.to), nowMs)}
            moreBefore={last.more_before}
            onLoadBefore={loadBefore}
            loadingBefore={loading}
            compact={compact}
            emptyText="No job ran, runs or waits in the last 24 hours."
          />
        );
      }}
    </Load>
  );
}
