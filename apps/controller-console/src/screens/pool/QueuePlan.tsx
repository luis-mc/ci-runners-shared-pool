// Pool > Queue Analysis (LOOK56, option C): every waiting job in start order, why it waits, and "Starts about HH:MM".
// A job that will not start by itself is marked "Needs you" and says what to do (UX-29, UX-58). LOOK70: as the approved
// prototype, no count tiles, no slot timeline, and a Reservation pane beside the running jobs.
import {
  Code,
  Empty,
  ExternalButton,
  Message,
  Section,
  Segmented,
  Stack,
  Status,
  Strong,
  Table,
  Text,
  TextLink,
  useNow,
  type Column,
  type StatusKind,
} from '@crsp/ui';
import { useState } from 'react';
import type { Schemas } from '../../api/api';
import { RESERVATION_WORDS, ReservationCharts } from '../jobs/ChartsMachines';
import { isReserved, type Job } from '../jobs/words';
import { RunningTable } from './PoolOverview';
import { WithOverview } from './OverviewState';
import { placeWords, startsAbout, waitWords } from './poolWords';
import {
  QUEUE_WORDS,
  missingOf,
  queueGroups,
  queueReason,
  waitedWords,
  type Reason,
} from './queueWords';

type Filter = '' | Reason;

const FILTERS: readonly { value: Reason; label: string }[] = [
  { value: 'room', label: QUEUE_WORDS.room },
  { value: 'reserved', label: QUEUE_WORDS.reserved },
  { value: 'none', label: QUEUE_WORDS.none },
];

const KIND: Record<Reason, StatusKind> = {
  room: 'waiting',
  reserved: 'info',
  none: 'failed',
  drained: 'failed',
};

export function QueuePlan() {
  const [filter, setFilter] = useState<Filter>('');
  return (
    <WithOverview>
      {(o: Schemas['Overview'], readAt) => {
        const queue = [...(o.queued ?? [])].sort(
          (a, b) => (a.queue_position ?? 1e9) - (b.queue_position ?? 1e9),
        );
        const count = (r: Reason) => queue.filter((j) => queueReason(j) === r).length;
        const shown = queue.filter((j) => filter === '' || queueReason(j) === filter);
        const groups = queueGroups(queue);
        return (
          <Stack gap="section">
            <Segmented<Reason>
              label="Show"
              options={FILTERS.map((f) => ({
                ...f,
                label: `${f.label} ${count(f.value) + (f.value === 'room' ? count('drained') : 0)}`,
              }))}
              value={filter}
              onChange={setFilter}
              allLabel={`${QUEUE_WORDS.all} ${queue.length}`}
            />
            <Section title="Queue, in start order" count={shown.length}>
              {groups.map((g) => (
                <Message key={g.key} tone="problem">
                  {g.words} <TextLink to={g.to}>{g.link}</TextLink>
                </Message>
              ))}
              {shown.length === 0 ? (
                <Empty title="Nothing is waiting. Every job has a slot." />
              ) : (
                <QueueTable shown={shown} queue={queue} readAt={readAt} />
              )}
            </Section>
            <ReservationPane readAt={readAt} queue={queue} />
            <RunningTable title="Where running jobs are going" running={o.running} />
          </Stack>
        );
      }}
    </WithOverview>
  );
}

/** One row per waiting job: the word that says why, its labels (the ones no runner has stand out) and how long it waited. */
function QueueTable({
  shown,
  queue,
  readAt,
}: {
  shown: readonly Job[];
  queue: readonly Job[];
  readAt: number;
}) {
  const now = useNow();
  const columns: Column<Job>[] = [
    {
      id: 'job',
      header: 'Job',
      grow: 3,
      cell: (j) => (
        <>
          {queue.indexOf(j) + 1}. <Strong>{j.job_name}</Strong>
        </>
      ),
      title: (j) => `${queue.indexOf(j) + 1}. ${j.job_name} ${j.repository} #${j.run_id}`,
      line: 1,
    },
    {
      id: 'repo',
      header: 'Repository',
      line: 2,
      cell: (j) => shortRepo(j.repository),
      title: (j) => `${j.repository} #${j.run_id}`,
    },
    {
      id: 'why',
      header: 'Why it waits',
      line: 1,
      cell: (j) => <Status kind={KIND[queueReason(j)]} word={stateWord(j)} />,
      title: (j) => stateWord(j),
    },
    {
      id: 'labels',
      header: 'Labels',
      grow: 3,
      secondary: true,
      cell: (j) => <LabelLine j={j} />,
      title: (j) => j.labels.join(', '),
    },
    {
      id: 'waited',
      header: 'Waiting',
      line: 2,
      cell: (j) => waitedWords(j, now),
      title: (j) => startsAbout(j.queue_eta, readAt),
    },
  ];
  return (
    <Table
      caption="Queue, in start order"
      columns={columns}
      rows={shown}
      rowKey={(j) => String(j.id)}
      oneLine
      compact
      fold="lines"
      detail={{
        label: 'Details',
        rowOpen: true,
        render: (j) => <QueueDetail j={j} readAt={readAt} />,
      }}
      rowLabel={(j) => j.job_name}
    />
  );
}

/** The runs-on labels as one line; the labels no runner has are in bold. */
function LabelLine({ j }: { j: Job }) {
  const missing = queueReason(j) === 'none' ? missingOf(j) : [];
  return (
    <>
      {[
        ...j.labels.filter((l) => missing.includes(l)),
        ...j.labels.filter((l) => !missing.includes(l)),
      ].map((l, i) => (
        <span key={l}>
          {i > 0 ? ', ' : ''}
          {missing.includes(l) ? (
            <Strong>
              <Code>{l}</Code>
            </Strong>
          ) : (
            <Code>{l}</Code>
          )}
        </span>
      ))}
    </>
  );
}

/** What opens under a row: the start estimate, where it will go, the whole reason, and the job's own page. */
function QueueDetail({ j, readAt }: { j: Job; readAt: number }) {
  return (
    <Stack gap="tight">
      <Text>{waitWords(j)}</Text>
      <Text>
        {startsAbout(j.queue_eta, readAt)}.{' '}
        {placeWords(j) === 'none yet' ? 'No machine yet.' : `Plan: ${placeWords(j)}.`}
      </Text>
      <Text mono>{j.labels.join(', ')}</Text>
      <TextLink to={`/pool/jobs/${j.id}`}>{`Open ${j.job_name} in Pool > Jobs`}</TextLink>
      {j.html_url ? (
        <ExternalButton href={j.html_url}>Open on GitHub</ExternalButton>
      ) : null}
    </Stack>
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
  return QUEUE_WORDS[queueReason(j)];
}

/** The repository without its owner: the owner is the same for every job of the pool. The full name is the cell's title. */
function shortRepo(repository: string): string {
  return repository.slice(repository.indexOf('/') + 1);
}
