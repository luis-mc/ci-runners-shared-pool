// Pool > Queue Analysis (LOOK56, option C): every waiting job in start order, why it waits, and "Starts about HH:MM".
// A job that will not start by itself is marked "Needs you" and says what to do (UX-29, UX-58).
import { Empty, Message, Section, Segmented, Stack, Status, TextLink } from '@crsp/ui';
import { useState } from 'react';
import type { Schemas } from '../../api/api';
import { type Job } from '../jobs/words';
import { RunningTable } from './PoolOverview';
import { SlotTimeline } from './SlotTimeline';
import { WithOverview } from './OverviewState';
import { needsYou, needsYouWords, placeWords, startsAbout, waitWords } from './poolWords';

type Filter = '' | 'room' | 'none';
const FILTERS = [
  { value: 'room', label: 'Waiting for room' },
  { value: 'none', label: 'No runner fits' },
] as const;

const matches = (f: Filter, j: Job): boolean =>
  f === '' ||
  (f === 'none'
    ? j.wait_reason === 'no_runner_with_labels'
    : j.wait_reason !== 'no_runner_with_labels');

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
            <Section
              title="Where the queue lands on the slots"
              hint="Waiting jobs are placed on the slots the scheduler gives them. A forecast is not a promise."
            >
              <SlotTimeline compact />
            </Section>
            <RunningTable title="Where running jobs are going" running={o.running} />
          </Stack>
        );
      }}
    </WithOverview>
  );
}

function QueueEntry({ j, place, readAt }: { j: Job; place: number; readAt: number }) {
  const risk = needsYou(j);
  return (
    <Section
      title={`${place}. ${j.job_name} ${j.repository} #${j.run_id}`}
      hint={waitWords(j)}
    >
      <Status
        kind={risk ? 'failed' : 'waiting'}
        word={risk ? 'Does not start by itself' : startsAbout(j.queue_eta, readAt)}
      />
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
