// Run insights, Timeline (idea 3, 16): one lane per runner, crsp runners and other runners by name. Each lane is a
// table of its segments in time order (the text equivalent of the bars, UX-17, UX-41); idle time, "idle while a job
// waited", "after-merge work" and the runner wait of a follow-up run are named in words. The range is 1 to 24 hours.
import {
  Empty,
  Section,
  Segmented,
  Stack,
  Table,
  formatShortDateTime,
  type Column,
} from '@crsp/ui';
import { useState } from 'react';
import { Load } from '../common';
import { useTimeline } from './api';
import { useRepositoryFilter } from './InsightsLayout';
import type { Schemas } from '../../api/api';

type Segment = Schemas['TimelineSegment'];

const HOURS = [
  { value: '1', label: '1 hour' },
  { value: '6', label: '6 hours' },
  { value: '24', label: '24 hours' },
] as const;

const ORIGIN_WORDS: Record<Schemas['TimelineLane']['origin'], string> = {
  crsp: 'crsp runner',
  other: 'another runner',
  hosted: 'GitHub-hosted runner',
};

const MARKER_WORDS: Record<
  NonNullable<Segment['markers']>[number],
  (s: Segment) => string
> = {
  idle_while_waiting: (s) =>
    `idle while ${s.waiting_job_ids?.length ? `${s.waiting_job_ids.length} job(s)` : 'a job'} waited`,
  after_merge_work: () => 'after-merge work',
};

const minutes = (s: Segment): string =>
  `${Math.max(1, Math.round((Date.parse(s.end) - Date.parse(s.start)) / 60_000))} min`;

const columns: Column<Segment>[] = [
  {
    id: 'start',
    header: 'From',
    cell: (s) => formatShortDateTime(s.start),
    sort: (s) => s.start,
  },
  { id: 'length', header: 'Length', cell: (s) => minutes(s) },
  {
    id: 'what',
    header: 'What',
    cell: (s) => {
      if (s.kind === 'idle') return 'Idle';
      const job = `${s.repository ?? ''} ${s.job_ident ?? ''}`.trim();
      return s.kind === 'wait' ? `Waited for a runner: ${job}` : job;
    },
    kind: 'grow',
    grow: 2,
  },
  {
    id: 'note',
    header: 'Note',
    cell: (s) =>
      [
        ...(s.follow_up ? ['follow-up run'] : []),
        ...(s.markers ?? []).map((m) => MARKER_WORDS[m](s)),
      ].join(', ') || 'none',
    kind: 'grow',
  },
];

export function Timeline() {
  const [hours, setHours] = useState<'1' | '6' | '24'>('24');
  const repository = useRepositoryFilter();
  const { result, reload } = useTimeline(Number(hours));
  return (
    <Stack gap="section">
      <Segmented
        label="Range of the timeline"
        options={HOURS}
        value={hours}
        allLabel={null}
        onChange={(v) => setHours(v === '1' || v === '6' ? v : '24')}
      />
      <Load result={result} what="the timeline" reload={reload}>
        {(t) =>
          t.lanes.length === 0 ? (
            <Section title="Timeline">
              <Empty title="No runner took a job in this range." />
            </Section>
          ) : (
            <Stack>
              {t.lanes.map((lane) => {
                const rows = lane.segments.filter(
                  (s) => !repository || s.kind === 'idle' || s.repository === repository,
                );
                return (
                  <Section
                    key={lane.runner_name}
                    title={lane.runner_name}
                    hint={`${ORIGIN_WORDS[lane.origin]}${lane.machine_name ? ` on ${lane.machine_name}` : ''}${lane.labels?.length ? `, labels ${lane.labels.join(', ')}` : ''}`}
                  >
                    <Table
                      caption={`Segments of ${lane.runner_name}`}
                      columns={columns}
                      rows={rows}
                      rowKey={(s) => `${s.start}-${s.kind}`}
                    />
                  </Section>
                );
              })}
            </Stack>
          )
        }
      </Load>
    </Stack>
  );
}
