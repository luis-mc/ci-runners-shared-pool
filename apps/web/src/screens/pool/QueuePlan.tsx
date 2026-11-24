// Pool > Queue Analysis (LOOK56, option C): every waiting job in start order, why it waits, and "Starts about HH:MM".
// A job that will not start by itself is marked "Needs you" and says what to do (UX-29, UX-58). LOOK70: as the approved
// prototype, no count tiles, no slot timeline, and a Reservation pane beside the running jobs.
import {
  Empty,
  FieldGrid,
  Message,
  Section,
  Segmented,
  Stack,
  Status,
  Strong,
  Text,
  TextLink,
} from '@crsp/ui';
import { useState } from 'react';
import type { Schemas } from '../../api/api';
import { RESERVATION_WORDS, ReservationCharts } from '../jobs/ChartsMachines';
import { isReserved, type Job } from '../jobs/words';
import { RunningTable } from './PoolOverview';
import { WithOverview } from './OverviewState';
import { needsYou, needsYouWords, placeWords, startsAbout, waitWords } from './poolWords';

type Filter = '' | 'room' | 'reserved' | 'none';
const FILTERS = [
  { value: 'room', label: 'Waiting for room' },
  { value: 'reserved', label: 'Reserved' },
  { value: 'none', label: 'No runner fits' },
] as const;

const matches = (f: Filter, j: Job): boolean => {
  if (f === '') return true;
  if (f === 'none') return j.wait_reason === 'no_runner_with_labels';
  if (f === 'reserved') return j.wait_reason === 'reserved';
  return j.wait_reason !== 'no_runner_with_labels' && j.wait_reason !== 'reserved';
};

export function QueuePlan() {
  const [filter, setFilter] = useState<Filter>('');
  return (
    <WithOverview>
      {(o: Schemas['Overview'], readAt) => {
        const queue = [...(o.queued ?? [])].sort(
          (a, b) => (a.queue_position ?? 1e9) - (b.queue_position ?? 1e9),
        );
        const shown = queue.filter((j) => matches(filter, j));
        return (
          <Stack gap="section">
            <Segmented<Filter>
              label="Show"
              options={FILTERS}
              value={filter}
              onChange={setFilter}
              allLabel={`All ${queue.length}`}
            />
            <Section title="Queue, in start order" count={shown.length}>
              {shown.length === 0 ? (
                <Empty title="Nothing is waiting. Every job has a slot." />
              ) : (
                <Stack>
                  {shown.map((j) => (
                    <QueueEntry
                      key={j.id}
                      j={j}
                      place={queue.indexOf(j) + 1}
                      readAt={readAt}
                    />
                  ))}
                </Stack>
              )}
            </Section>
            <FieldGrid>
              <ReservationPane readAt={readAt} queue={queue} />
              <RunningTable title="Where running jobs are going" running={o.running} />
            </FieldGrid>
          </Stack>
        );
      }}
    </WithOverview>
  );
}

const clock = (iso: string): string => {
  const d = new Date(iso);
  return `${String(d.getHours()).padStart(2, '0')}:${String(d.getMinutes()).padStart(2, '0')}`;
};

/**
 * The Reservation pane (LOOK70): the job its machine holds room for, one link to it, and the two free-CPU charts.
 * The API sends no free-CPU series, so the charts are the worked example (R5.166); the pane shows only when a job is
 * reserved.
 */
function ReservationPane({ queue, readAt }: { queue: readonly Job[]; readAt: number }) {
  const held = queue.find((j) => isReserved(j));
  if (!held) return null;
  return (
    <Section title="Reservation" hint={RESERVATION_WORDS.hint}>
      <Stack>
        <Text>
          <Strong>{held.job_name}</Strong>
          {held.reserved_since
            ? ` reserved since ${clock(held.reserved_since)}. `
            : ' is reserved. '}
          {waitWords(held)}. {startsAbout(held.queue_eta, readAt)}.
        </Text>
        <TextLink
          to={`/pool/jobs/${held.id}`}
        >{`Open ${held.job_name} in Pool > Jobs`}</TextLink>
        <ReservationCharts />
      </Stack>
    </Section>
  );
}

function stateWord(j: Job): string {
  if (isReserved(j)) return 'Reserved';
  if (j.wait_reason === 'no_runner_with_labels') return 'No runner fits';
  if (j.wait_reason === 'machine_drained') return 'Machine not taking jobs';
  return 'Waiting for room';
}

function QueueEntry({ j, place, readAt }: { j: Job; place: number; readAt: number }) {
  const risk = needsYou(j);
  return (
    <Section
      title={`${place}. ${j.job_name} ${j.repository} #${j.run_id}`}
      hint={waitWords(j)}
    >
      <Status kind={risk ? 'failed' : 'waiting'} word={stateWord(j)} />
      <Text>
        <Strong>
          {risk ? 'Does not start by itself' : startsAbout(j.queue_eta, readAt)}
        </Strong>
      </Text>
      <p>
        {placeWords(j) === 'none yet' ? 'No machine yet.' : `Plan: ${placeWords(j)}.`}{' '}
        {j.labels.length ? `Label ${j.labels.join(', ')}.` : ''}
      </p>
      {risk ? (
        <Message tone="problem">
          <Status kind="waiting" word="Needs you" /> {needsYouWords(j)}{' '}
          <TextLink to="/setup/machines">Open Setup &gt; Machines</TextLink>
        </Message>
      ) : null}
    </Section>
  );
}
