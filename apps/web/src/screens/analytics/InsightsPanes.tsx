// Analytics > Insights, the two panes at the foot of the page (LOOK65): Waits, a small table of the waits by job, and the
// Runner timeline, one row for each runner, drawn by the shared timeline part. The screens pass data only (UX-41).
import {
  Card,
  Empty,
  Table,
  Timeline,
  useNow,
  type Column,
  type TimelineBarData,
  type TimelineRowData,
} from '@crsp/ui';
import type { Schemas } from '../../api/api';
import { Load } from '../common';
import { useTimeline, useWaits } from '../insights/api';
import { useRepositoryFilter } from '../insights/InsightsLayout';
import { secondsWords } from '../insights/words';

type Row = Schemas['WaitSplitRow'];
type Lane = Schemas['TimelineLane'];
type Segment = Schemas['TimelineSegment'];

export const WAITS_WORDS = {
  title: 'Waits',
  empty: 'No finished jobs in the last 7 days.',
  label: 'Label',
  median: 'Median',
  slowest: 'Slowest 5%',
} as const;

const waitsColumns: Column<Row>[] = [
  {
    id: 'label',
    header: WAITS_WORDS.label,
    cell: (r) => r.key,
    sort: (r) => r.key,
    kind: 'grow',
  },
  {
    id: 'median',
    header: WAITS_WORDS.median,
    cell: (r) => secondsWords(r.wait.p50_seconds),
    sort: (r) => r.wait.p50_seconds ?? 0,
    kind: 'fit',
  },
  {
    id: 'slowest',
    header: WAITS_WORDS.slowest,
    cell: (r) => secondsWords(r.wait.p95_seconds),
    sort: (r) => r.wait.p95_seconds ?? 0,
    kind: 'fit',
  },
];

export function WaitsPane() {
  const repository = useRepositoryFilter();
  const { result, reload } = useWaits(7, 'job_key');
  return (
    <Card title={WAITS_WORDS.title}>
      <Load result={result} what="the waits" reload={reload}>
        {(d) => {
          const rows = d.rows.filter((r) => !repository || r.key.startsWith(repository));
          return rows.length === 0 ? (
            <Empty title={WAITS_WORDS.empty} />
          ) : (
            <Table
              caption={WAITS_WORDS.title}
              oneLine
              columns={waitsColumns}
              rows={rows}
              rowKey={(r) => r.key}
            />
          );
        }}
      </Load>
    </Card>
  );
}

export const RUNNER_TIMELINE_WORDS = {
  title: 'Runner timeline',
  chart: 'Jobs of each runner',
  empty: 'No runner took a job in the last 6 hours.',
  hours: 6,
  waited: (job: string) => `Waited for a runner: ${job}`,
  followUp: 'follow-up run',
  afterMerge: 'after-merge work',
} as const;

const jobName = (s: Segment): string =>
  `${s.repository ?? ''} ${s.job_ident ?? ''}`.trim() || 'a job';

/** A segment in words: what it is and its notes. */
export function segmentWords(s: Segment): string {
  const notes = [
    ...(s.follow_up ? [RUNNER_TIMELINE_WORDS.followUp] : []),
    ...(s.markers?.includes('after_merge_work')
      ? [RUNNER_TIMELINE_WORDS.afterMerge]
      : []),
  ];
  const what = s.kind === 'wait' ? RUNNER_TIMELINE_WORDS.waited(jobName(s)) : jobName(s);
  return notes.length ? `${what}, ${notes.join(', ')}` : what;
}

/** The rows and bars of the lanes. Idle time is the gap between bars, so it is not drawn. */
export function lanesToTimeline(
  lanes: readonly Lane[],
  nowMs: number,
  repository?: string,
): { rows: TimelineRowData[]; bars: TimelineBarData[] } {
  const rows: TimelineRowData[] = [];
  const bars: TimelineBarData[] = [];
  for (const lane of lanes) {
    rows.push({
      key: lane.runner_name,
      label: lane.runner_name,
      group: lane.machine_name,
    });
    lane.segments.forEach((s, i) => {
      if (s.kind === 'idle' || (repository && s.repository !== repository)) return;
      const end = Date.parse(s.end);
      bars.push({
        id: `${lane.runner_name}-${i}`,
        rowKey: lane.runner_name,
        state: s.kind === 'wait' ? 'waiting_runner' : end >= nowMs ? 'running' : 'done',
        label: s.job_ident ?? jobName(s),
        sublabel: s.repository,
        startMs: Date.parse(s.start),
        ...(end >= nowMs && s.kind !== 'wait' ? {} : { endMs: end }),
        detail: segmentWords(s),
      });
    });
  }
  return { rows, bars };
}

export function RunnerTimelinePane() {
  const repository = useRepositoryFilter();
  const nowMs = useNow();
  const { result, reload } = useTimeline(RUNNER_TIMELINE_WORDS.hours);
  return (
    <Card title={RUNNER_TIMELINE_WORDS.title}>
      <Load result={result} what="the runner timeline" reload={reload}>
        {(t) => {
          const { rows, bars } = lanesToTimeline(t.lanes, nowMs, repository);
          return (
            <Timeline
              label={RUNNER_TIMELINE_WORDS.chart}
              rows={rows}
              bars={bars}
              nowMs={nowMs}
              fromMs={nowMs - RUNNER_TIMELINE_WORDS.hours * 3_600_000}
              toMs={nowMs}
              moreBefore={false}
              compact
              emptyText={RUNNER_TIMELINE_WORDS.empty}
            />
          );
        }}
      </Load>
    </Card>
  );
}
