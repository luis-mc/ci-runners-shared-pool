// Notices (notifications section 4 "Notices list", R5.32; web-console): the last 30 days of notices, newest first.
// Each row has the time (UX-18), the event in words, the subject, the state and the tone as a word (UX-26), and what
// became of each delivery. The page only shows and links (UX-52); the settings for channels stay on Settings,
// Notifications. A delivery names its channel and a host or address, never a path or a secret.
import {
  Cluster,
  DateTime,
  Empty,
  LinkButton,
  Page,
  PageHeader,
  Section,
  Stack,
  Status,
  Table,
  Text,
  formatDateTime,
  type Column,
  type StatusKind,
} from '@crsp/ui';
import { unwrap, useClient, type Schemas } from '../../api/api';
import { useLoad } from '../../api/hooks';
import { Load } from '../common';
import { settingsPath } from '../settings/sections';

type Notice = Schemas['Notice'];
type Delivery = Schemas['NoticeDelivery'];

const EVENT_WORDS: Record<string, string> = {
  'machine.offline': 'Machine offline',
  'job.waiting': 'Job waiting',
  'disk.low': 'Disk low',
  'runner.outdated': 'Runner outdated',
  'doctor.finding': 'Doctor finding',
  'update.available': 'Update available',
  'notice.summary': 'More notices',
};

export const TONE_WORDS: Record<Schemas['NoticeTone'], string> = {
  done: 'Done',
  note: 'Note',
  needs_you: 'Needs you',
  problem: 'Problem',
};

const STATE_WORDS: Record<Notice['state'], string> = {
  firing: 'Open',
  resolved: 'Resolved',
};

const DELIVERY_WORDS: Record<Delivery['status'], { kind: StatusKind; word: string }> = {
  queued: { kind: 'waiting', word: 'Waiting to send' },
  waiting_tray: { kind: 'waiting', word: 'Waiting for the tray' },
  delivered: { kind: 'ok', word: 'Delivered' },
  failed: { kind: 'failed', word: 'Failed' },
  held: { kind: 'waiting', word: 'Held for the hourly summary' },
  grouped: { kind: 'info', word: 'Sent in a group' },
  dropped: { kind: 'off', word: 'Not sent' },
};

/** The event in words: a known type, or the type as sent. */
export function eventWord(type: string): string {
  return EVENT_WORDS[type] ?? type;
}

/** One line for a delivery: the channel, where it went, and what became of it. */
export function deliveryText(d: Delivery): string {
  const w = DELIVERY_WORDS[d.status].word;
  const where = d.target ? ` to ${d.target}` : '';
  const tries = d.attempt > 1 ? `, ${d.attempt} tries` : '';
  const next =
    d.status === 'queued' && d.next_try_at
      ? `, next try ${formatDateTime(d.next_try_at)}`
      : '';
  const why = d.last_error ? `. ${d.last_error}` : '';
  return `${d.channel}${where}: ${w}${tries}${next}${why}`;
}

/** The worst delivery of a notice decides the one status word of its row. */
function rowStatus(n: Notice): { kind: StatusKind; word: string } {
  const ds = n.deliveries;
  if (ds.length === 0) return { kind: 'info', word: 'Kept here only' };
  for (const s of ['failed', 'queued', 'waiting_tray', 'held'] as const) {
    if (ds.some((d) => d.status === s)) return DELIVERY_WORDS[s];
  }
  if (ds.every((d) => d.status === 'delivered' || d.status === 'grouped'))
    return DELIVERY_WORDS.delivered;
  return DELIVERY_WORDS.dropped;
}

const columns: Column<Notice>[] = [
  {
    id: 'at',
    header: 'When',
    cell: (n) => <DateTime value={n.at} />,
    title: (n) => formatDateTime(n.at),
    sort: (n) => n.at,
  },
  {
    id: 'event',
    header: 'Event',
    cell: (n) => eventWord(n.type),
    sort: (n) => eventWord(n.type),
  },
  {
    id: 'subject',
    header: 'About',
    cell: (n) => n.subject,
    sort: (n) => n.subject,
    kind: 'grow',
  },
  {
    id: 'state',
    header: 'State',
    cell: (n) => STATE_WORDS[n.state],
    sort: (n) => n.state,
  },
  {
    id: 'tone',
    header: 'Tone',
    cell: (n) => TONE_WORDS[n.tone],
    sort: (n) => TONE_WORDS[n.tone],
  },
  {
    id: 'summary',
    header: 'What happened',
    cell: (n) => n.summary + (n.flaps > 0 ? ` It came back ${n.flaps} times.` : ''),
    long: (n) => ({
      full: n.summary + (n.flaps > 0 ? ` It came back ${n.flaps} times.` : ''),
    }),
    secondary: true,
  },
  {
    id: 'delivery',
    header: 'Sent',
    cell: (n) => {
      const s = rowStatus(n);
      return <Status kind={s.kind} word={s.word} />;
    },
    long: (n) =>
      n.deliveries.length === 0
        ? null
        : { full: n.deliveries.map(deliveryText).join('\n'), short: rowStatus(n).word },
  },
];

export function NoticesScreen() {
  const client = useClient();
  const { result, reload } = useLoad(
    () => unwrap(client.GET('/api/v1/notices', { params: { query: { limit: 200 } } })),
    [client],
  );
  return (
    <Page>
      <PageHeader
        title="Notices"
        lead="Everything the pool told people in the last 30 days, and what became of each message."
      />
      <Stack>
        <Section title="Last 30 days">
          <Load result={result} what="the notices" reload={reload}>
            {(data) =>
              data.items.length === 0 ? (
                <Empty title="No notices yet. Notices appear here when a machine, job or disk needs you." />
              ) : (
                <Table
                  caption="Notices of the last 30 days"
                  columns={columns}
                  rows={data.items}
                  rowKey={(n) => n.id}
                />
              )
            }
          </Load>
        </Section>
        <Cluster>
          <Text>Channels and the events they carry are set on Settings.</Text>
          <LinkButton to={settingsPath('notifications')} kind="primary">
            Open notification settings
          </LinkButton>
        </Cluster>
      </Stack>
    </Page>
  );
}
