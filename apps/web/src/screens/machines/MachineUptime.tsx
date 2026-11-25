// The uptime of one machine (machines R5.83 to R5.86): the share of time it was online over 24 hours, 7 days and
// 30 days, and why it took no job in each period. Shared parts only; the words are in content.md ("Machine uptime words").
import {
  Button,
  Facts,
  Failed,
  Loading,
  Section,
  Stack,
  StatGrid,
  StatTile,
  Status,
  Table,
  Text,
  formatDateTime,
  formatDuration,
  formatNumber,
  type Column,
} from '@crsp/ui';
import { unwrap, useClient, type Schemas } from '../../api/api';
import { useLoad } from '../../api/hooks';
import { useVersion } from '../../live/LiveProvider';

type Uptime = Schemas['MachineUptime'];
type Window = Schemas['UptimeWindow'];
type Period = Schemas['UptimePeriod'];

const WINDOW_WORDS: Record<string, { label: string; span: number }> = {
  '24h': { label: 'Last 24 hours', span: 24 * 3600 },
  '7d': { label: 'Last 7 days', span: 7 * 24 * 3600 },
  '30d': { label: 'Last 30 days', span: 30 * 24 * 3600 },
};

/** Why a machine took no job, in words (content.md, Machine uptime words). */
export function uptimeWhy(reason: string): string {
  switch (reason) {
    case 'heartbeat_lost':
      return 'No sign of life for 30 seconds';
    case 'clock_skew':
      return "Its clock was ahead of the controller's";
    case 'controller_stopped':
      return 'The controller was not running';
    case 'stopped':
      return 'Stopped from the console';
    case 'mode_paused':
      return 'The mode is Paused';
    case 'tray_paused':
      return 'Paused from the tray';
    case 'on_battery':
      return 'On battery';
    case 'busy':
      return 'Busy: the load was over the budget';
    case 'base_image_missing':
      return 'The base image is missing';
    case 'disk_low':
      return 'Free disk space is low';
    default:
      return 'Not known';
  }
}

const pct = (n: number) => formatNumber(n, 1);

function Tiles({ windows }: { windows: readonly Window[] }) {
  return (
    <StatGrid>
      {windows.map((w) => (
        <StatTile
          key={w.window}
          label={WINDOW_WORDS[w.window]?.label ?? w.window}
          value={w.uptime_pct === undefined ? undefined : pct(w.uptime_pct)}
          unit="% online"
          change="same"
          changeText={
            w.taking_pct === undefined ? '' : `took jobs ${pct(w.taking_pct)}% of the time`
          }
        />
      ))}
    </StatGrid>
  );
}

/** One line per window that was watched for less than its length. */
function watchedNote(windows: readonly Window[]): { name: string; value: string }[] {
  return windows
    .filter((w) => {
      const span = WINDOW_WORDS[w.window]?.span ?? 0;
      return w.covered_seconds > 0 && w.covered_seconds < span - 60;
    })
    .map((w) => ({
      name: `${WINDOW_WORDS[w.window]?.label ?? w.window}, watched for`,
      value: formatDuration(w.covered_seconds),
    }));
}

export function MachineUptimeSection({ machineId }: { machineId: string }) {
  const client = useClient();
  const v = useVersion('machines');
  const { result, reload } = useLoad(
    () =>
      unwrap(
        client.GET('/api/v1/machines/{machineId}/uptime', {
          params: { path: { machineId } },
        }),
      ),
    [machineId, v],
  );
  return (
    <Section title="Uptime">
      {result.state === 'loading' ? <Loading what="the uptime" /> : null}
      {result.state === 'failed' && !result.data ? (
        <Failed
          what="Could not load the uptime."
          why={result.error.message}
          action={<Button onPress={reload}>Try again</Button>}
        />
      ) : null}
      {result.state !== 'loading' && result.data ? <Body up={result.data} /> : null}
    </Section>
  );
}

function Body({ up }: { up: Uptime }) {
  if (!up.watched) {
    return (
      <Text>
        No uptime yet. The controller has not seen this machine. It counts from the
        first time it does.
      </Text>
    );
  }
  const notes = watchedNote(up.windows);
  const columns: Column<Period>[] = [
    {
      id: 'since',
      header: 'From',
      cell: (p) => formatDateTime(p.started_at),
      sort: (p) => p.started_at,
    },
    {
      id: 'length',
      header: 'Lasted',
      cell: (p) =>
        p.ended_at
          ? formatDuration(
              (Date.parse(p.ended_at) - Date.parse(p.started_at)) / 1000,
            )
          : 'Now',
    },
    {
      id: 'state',
      header: 'State',
      cell: (p) =>
        p.offline ? (
          <Status kind="failed" word="Offline" />
        ) : (
          <Status kind="waiting" word="Online, took no jobs" />
        ),
    },
    {
      id: 'why',
      header: 'Why',
      cell: (p) => uptimeWhy(p.reason),
      kind: 'grow',
    },
  ];
  return (
    <Stack>
      <Tiles windows={up.windows} />
      {notes.length > 0 ? <Facts items={notes} /> : null}
      {up.periods.length === 0 ? (
        <Text>This machine took jobs the whole time it was watched.</Text>
      ) : (
        <>
          <Text>
            While a machine takes no jobs, new jobs go to the fallback runner when one is
            set up. The newest 50 times of the last 30 days:
          </Text>
          <Table
            caption="Times this machine took no jobs"
            columns={columns}
            rows={up.periods}
            rowKey={(p) => p.started_at}
          />
        </>
      )}
    </Stack>
  );
}
