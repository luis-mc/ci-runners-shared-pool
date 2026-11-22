// The machine page of Pool (LOOK58c, route /pool/machines/:machineId): every health metric of one machine, its disks,
// its job stats and its runners. The health API gives the last report and 24 hours of free space as days until full,
// not a series, so the numbers show as bars and tables and nothing is drawn that was not measured.
import {
  Breadcrumbs,
  Empty,
  Facts,
  LevelBar,
  Message,
  Page,
  PageHeader,
  Section,
  Stack,
  StatGrid,
  Status,
  StatTile,
  Table,
  Text,
  formatAgo,
  formatDateTime,
  formatNumber,
  formatPercent,
  formatSize,
  useNow,
  type Column,
} from '@crsp/ui';
import { useState } from 'react';
import { useParams } from 'react-router-dom';
import { unwrap, useClient, type Schemas } from '../../api/api';
import { useLoad } from '../../api/hooks';
import { Load } from '../common';
import { MachineLoadBars, machineStatus } from '../overview/Overview';
import {
  HealthBadges,
  StatsSummary,
  WindowSwitch,
  useMachineHealth,
  useMachineStats,
} from './MachineStats';
import { MachineTimeline } from './MachineTimeline';
import {
  VOLUME_WORDS,
  averageWords,
  dockerBytes,
  metricsOf,
  orUnknown,
  runnerRowsOf,
  skewWords,
  uptimeWords,
  type RunnerStatRow,
  type StatsWindow,
  WINDOW_WORDS,
  unknownOn,
} from './machineHealth';

type Machine = Schemas['Machine'];
type Health = Schemas['MachineHealth'];
type Volume = Schemas['HealthVolume'];

const yesNo = (v: boolean | undefined): string | undefined =>
  v === undefined ? undefined : v ? 'yes' : 'no';

function volumeColumns(os: string): Column<Volume>[] {
  return [
    { id: 'disk', header: 'Disk', cell: (v) => VOLUME_WORDS[v.role] },
    { id: 'path', header: 'Path', cell: (v) => v.path },
    {
      id: 'free',
      header: 'Free',
      cell: (v) =>
        v.free_bytes === undefined
          ? orUnknown(undefined, os, v.unknown_reason)
          : `${formatSize(v.free_bytes)} (${formatPercent(v.free_pct ?? 0)})`,
    },
    {
      id: 'size',
      header: 'Size',
      cell: (v) =>
        v.total_bytes === undefined ? unknownOn(os) : formatSize(v.total_bytes),
    },
    {
      id: 'days',
      header: 'Days until full',
      cell: (v) =>
        v.days_until_full !== undefined
          ? formatNumber(v.days_until_full, v.days_until_full < 10 ? 1 : 0)
          : (v.days_note ??
            (v.free_bytes === undefined ? unknownOn(os) : 'not enough samples yet')),
    },
  ];
}

function DiskSection({ h, os }: { h: Health | undefined; os: string }) {
  const volumes = h?.volumes ?? [];
  return (
    <Section title="Disks">
      {volumes.length === 0 ? (
        <Text>{unknownOn(os)}</Text>
      ) : (
        <Stack gap="tight">
          {volumes.map((v) =>
            v.total_bytes !== undefined && v.free_bytes !== undefined ? (
              <LevelBar
                key={`${v.role}${v.path}`}
                label={`${VOLUME_WORDS[v.role]} used`}
                value={v.total_bytes - v.free_bytes}
                max={v.total_bytes}
                text={`${formatSize(v.total_bytes - v.free_bytes)} of ${formatSize(v.total_bytes)} used`}
              />
            ) : null,
          )}
          <Table
            caption="Disks of this machine"
            columns={volumeColumns(os)}
            rows={volumes}
            rowKey={(v) => `${v.role}${v.path}`}
          />
        </Stack>
      )}
    </Section>
  );
}

function MemorySection({ m, h }: { m: Machine; h: Health | undefined }) {
  const swap = h?.swap;
  const swapped = swap?.total_bytes !== undefined && swap.used_bytes !== undefined;
  const oom = h?.oom;
  return (
    <Section title="Memory and swap">
      <Stack gap="tight">
        <MachineLoadBars m={m} />
        {swapped && swap.total_bytes ? (
          <LevelBar
            label="Swap"
            value={swap.used_bytes ?? 0}
            max={swap.total_bytes}
            text={`${formatSize(swap.used_bytes ?? 0)} of ${formatSize(swap.total_bytes)} used`}
          />
        ) : null}
        <Facts
          items={[
            {
              name: 'Swap',
              value: swapped
                ? swap.total_bytes === 0
                  ? 'none set up'
                  : `${formatSize(swap.used_bytes ?? 0)} of ${formatSize(swap.total_bytes ?? 0)} used`
                : orUnknown(undefined, m.os, swap?.unknown_reason),
            },
            {
              name: 'Memory kills',
              value:
                oom?.total_kills !== undefined
                  ? `${oom.total_kills}${oom.recent ? ', one recently' : ''}`
                  : orUnknown(undefined, m.os, oom?.unknown_reason),
            },
            {
              name: 'Last memory kill',
              value:
                oom?.last_kill_at !== undefined
                  ? formatDateTime(oom.last_kill_at)
                  : oom?.total_kills === 0
                    ? 'none'
                    : unknownOn(m.os),
            },
          ]}
        />
        {oom?.scopes && oom.scopes.length > 0 ? (
          <Table
            caption="Memory kills by scope"
            columns={[
              {
                id: 'scope',
                header: 'Scope',
                cell: (s) => (s.scope === 'pool' ? 'Pool' : 'Job'),
              },
              { id: 'name', header: 'Name', cell: (s) => s.name },
              { id: 'kills', header: 'Kills', cell: (s) => String(s.kills) },
            ]}
            rows={oom.scopes}
            rowKey={(s) => `${s.scope}${s.name}`}
          />
        ) : null}
      </Stack>
    </Section>
  );
}

function NetworkSection({ h, os }: { h: Health | undefined; os: string }) {
  const c = h?.connectivity;
  return (
    <Section title="Network and clock">
      <Facts
        items={[
          {
            name: 'Round trip to the controller',
            value: orUnknown(c?.rtt_ms === undefined ? undefined : `${c.rtt_ms} ms`, os),
          },
          {
            name: 'Last heartbeat',
            value: orUnknown(
              c?.last_heartbeat_at === undefined
                ? undefined
                : formatDateTime(c.last_heartbeat_at),
              os,
            ),
          },
          {
            name: 'Reconnects in 24 h',
            value: orUnknown(
              c?.reconnects_24h === undefined ? undefined : String(c.reconnects_24h),
              os,
            ),
          },
          {
            name: 'GitHub',
            value: orUnknown(
              c?.github?.reachable === undefined
                ? undefined
                : c.github.reachable
                  ? `reachable${c.github.latency_ms !== undefined ? `, ${c.github.latency_ms} ms` : ''}`
                  : `not reachable${c.github.error ? `: ${c.github.error}` : ''}`,
              os,
            ),
          },
          {
            name: 'DNS',
            value: orUnknown(
              c?.dns?.ok === undefined
                ? undefined
                : c.dns.ok
                  ? 'works'
                  : `fails${c.dns.error ? `: ${c.dns.error}` : ''}`,
              os,
            ),
          },
          {
            name: 'Clock skew',
            value: orUnknown(
              c?.clock_skew_seconds === undefined
                ? undefined
                : skewWords(c.clock_skew_seconds),
              os,
            ),
          },
        ]}
      />
    </Section>
  );
}

function UpdatesSection({ h, os }: { h: Health | undefined; os: string }) {
  const u = h?.updates;
  const p = h?.power;
  const b = h?.boot;
  const hours =
    u?.active_hours_start !== undefined && u.active_hours_end !== undefined
      ? `${String(u.active_hours_start).padStart(2, '0')}:00 to ${String(u.active_hours_end).padStart(2, '0')}:00`
      : undefined;
  return (
    <Section title="Restarts, sleep and power">
      <Facts
        items={[
          {
            name: 'Uptime',
            value: orUnknown(
              b?.uptime_seconds === undefined ? undefined : uptimeWords(b.uptime_seconds),
              os,
              b?.unknown_reason,
            ),
          },
          {
            name: 'Last boot',
            value: orUnknown(
              b?.last_boot_at === undefined ? undefined : formatDateTime(b.last_boot_at),
              os,
              b?.unknown_reason,
            ),
          },
          {
            name: 'Restarted without being asked',
            value: orUnknown(yesNo(b?.unexpected_restart), os, b?.unknown_reason),
          },
          {
            name: 'Restart pending',
            value: orUnknown(yesNo(u?.restart_pending), os, u?.unknown_reason),
          },
          {
            name: 'Why it is pending',
            value:
              u?.restart_reasons && u.restart_reasons.length > 0
                ? u.restart_reasons.join(', ')
                : u?.restart_pending === false
                  ? 'not pending'
                  : orUnknown(undefined, os),
          },
          {
            name: 'May restart now',
            value: orUnknown(yesNo(u?.restart_may_happen_now), os),
          },
          {
            name: 'May stop running jobs',
            value: orUnknown(yesNo(u?.restart_may_hit_jobs), os),
          },
          {
            name: 'Updates waiting',
            value: orUnknown(
              u?.pending_update_count === undefined
                ? undefined
                : String(u.pending_update_count),
              os,
              u?.unknown_reason,
            ),
          },
          { name: 'Update active hours', value: orUnknown(hours, os) },
          {
            name: 'Automatic updates',
            value: orUnknown(u?.auto_update_state || undefined, os),
          },
          {
            name: 'Power',
            value: orUnknown(
              p?.on_battery === undefined
                ? undefined
                : p.on_battery
                  ? 'on battery'
                  : 'on mains power',
              os,
              p?.source_unknown_reason,
            ),
          },
          {
            name: 'Sleep',
            value: orUnknown(
              p?.sleep_enabled === undefined
                ? undefined
                : p.sleep_enabled
                  ? `can go to sleep${p.sleep_modes && p.sleep_modes.length ? ` (${p.sleep_modes.join(', ')})` : ''}`
                  : 'never goes to sleep',
              os,
              p?.sleep_unknown_reason,
            ),
          },
          {
            name: 'Last wake',
            value: orUnknown(
              p?.last_wake_at === undefined ? undefined : formatDateTime(p.last_wake_at),
              os,
            ),
          },
        ]}
      />
    </Section>
  );
}

function SoftwareSection({ h, os }: { h: Health | undefined; os: string }) {
  const v = h?.versions;
  const d = h?.docker_use;
  const flag = (out: boolean | undefined) => (out ? ' (out of date)' : '');
  const docker = dockerBytes(h);
  return (
    <Section title="Versions and Docker">
      <Facts
        items={[
          {
            name: 'Agent',
            value: orUnknown(
              v?.agent ? `${v.agent}${flag(v.agent_out_of_date)}` : undefined,
              os,
            ),
          },
          {
            name: 'Controller',
            value: orUnknown(v?.controller || undefined, os),
          },
          {
            name: 'Runner app',
            value: orUnknown(
              v?.runner_app ? `${v.runner_app}${flag(v.runner_out_of_date)}` : undefined,
              os,
            ),
          },
          {
            name: 'Docker',
            value: orUnknown(
              v?.docker
                ? `${v.docker_kind ? `${v.docker_kind} ` : ''}${v.docker}`
                : undefined,
              os,
            ),
          },
          { name: 'WSL', value: orUnknown(v?.wsl || undefined, os) },
          {
            name: 'Docker disk',
            value: orUnknown(
              docker === undefined ? undefined : `${formatSize(docker)} in use`,
              os,
              d?.unknown_reason,
            ),
          },
        ]}
      />
      {docker !== undefined ? (
        <Table
          caption="What Docker uses on disk"
          columns={[
            {
              id: 'what',
              header: 'What',
              cell: (r: { what: string; bytes: number | undefined }) => r.what,
            },
            {
              id: 'size',
              header: 'Size',
              cell: (r) => (r.bytes === undefined ? unknownOn(os) : formatSize(r.bytes)),
            },
          ]}
          rows={[
            { what: 'Images', bytes: d?.images_bytes },
            { what: 'Build cache', bytes: d?.build_cache_bytes },
            { what: 'Volumes', bytes: d?.volumes_bytes },
            { what: 'Containers', bytes: d?.containers_bytes },
          ]}
          rowKey={(r) => r.what}
        />
      ) : null}
    </Section>
  );
}

function HeatSection({ h, os }: { h: Health | undefined; os: string }) {
  const t = h?.thermal;
  const l = h?.load;
  const n = (x: number | undefined) => (x === undefined ? undefined : formatNumber(x, 1));
  return (
    <Section title="Heat and load">
      <Facts
        items={[
          {
            name: 'Temperature',
            value: orUnknown(
              t?.max_temp_celsius === undefined
                ? undefined
                : `${formatNumber(t.max_temp_celsius, 1)} °C`,
              os,
              t?.unknown_reason,
            ),
          },
          {
            name: 'Slowed by heat',
            value: orUnknown(
              t?.throttled === undefined
                ? undefined
                : t.throttled
                  ? `yes${t.throttle_detail ? `: ${t.throttle_detail}` : ''}`
                  : 'no',
              os,
              t?.unknown_reason,
            ),
          },
          {
            name: 'Load, 1 min',
            value: orUnknown(n(l?.load1), os, l?.load_unknown_reason),
          },
          {
            name: 'Load, 5 min',
            value: orUnknown(n(l?.load5), os, l?.load_unknown_reason),
          },
          {
            name: 'Load, 15 min',
            value: orUnknown(n(l?.load15), os, l?.load_unknown_reason),
          },
          {
            name: 'CPU taken by the host',
            value: orUnknown(
              l?.steal_pct === undefined ? undefined : formatPercent(l.steal_pct),
              os,
              l?.steal_unknown_reason,
            ),
          },
        ]}
      />
    </Section>
  );
}

const OUTCOMES = [
  ['succeeded', 'Succeeded'],
  ['failed', 'Failed'],
  ['cancelled', 'Cancelled'],
  ['timed_out', 'Timed out'],
  ['over_memory', 'Over memory'],
  ['stopped', 'Stopped by crsp'],
] as const;

function runnerColumns(): Column<RunnerStatRow>[] {
  return [
    { id: 'runner', header: 'Runner', cell: (r) => r.name },
    { id: 'jobs', header: 'Jobs', cell: (r) => String(r.stats?.counts.total ?? 0) },
    {
      id: 'ok',
      header: 'Succeeded',
      cell: (r) => String(r.stats?.counts.succeeded ?? 0),
    },
    {
      id: 'bad',
      header: 'Failed or cut short',
      cell: (r) =>
        String(
          (r.stats?.counts.failed ?? 0) +
            (r.stats?.counts.timed_out ?? 0) +
            (r.stats?.counts.over_memory ?? 0),
        ),
    },
    {
      id: 'run',
      header: 'Average run time',
      cell: (r) => averageWords(r.stats?.avg_run_seconds),
    },
    {
      id: 'busy',
      header: 'Busy',
      cell: (r) =>
        r.stats?.busy_pct === undefined ? 'no jobs yet' : formatPercent(r.stats.busy_pct),
    },
  ];
}

function StatsSection({
  m,
  runners,
}: {
  m: Machine;
  runners: readonly Schemas['Runner'][];
}) {
  const [window, setWindow] = useState<StatsWindow>('24h');
  const stats = useMachineStats(m.id, window);
  return (
    <Section title="Job stats" hint={`Jobs that finished in ${WINDOW_WORDS[window]}.`}>
      <Stack gap="tight">
        <WindowSwitch value={window} onChange={setWindow} />
        <Load result={stats.result} what="the job stats" reload={stats.reload}>
          {(d) => (
            <Stack gap="tight">
              {OUTCOMES.map(([key, label]) => (
                <LevelBar
                  key={key}
                  label={label}
                  value={d.machine.counts[key]}
                  max={Math.max(1, d.machine.counts.total)}
                  text={`${d.machine.counts[key]} of ${d.machine.counts.total} jobs`}
                />
              ))}
              <StatsSummary stats={d.machine} machineId={m.id} window={window} />
              {runnerRowsOf(runners, d).length === 0 ? (
                <Empty title="No runner is on this machine." />
              ) : (
                <Table
                  caption="Runners of this machine"
                  columns={runnerColumns()}
                  rows={runnerRowsOf(runners, d)}
                  rowKey={(r) => r.id}
                />
              )}
            </Stack>
          )}
        </Load>
      </Stack>
    </Section>
  );
}

function MachineBody({ m }: { m: Machine }) {
  const client = useClient();
  const now = useNow();
  const health = useMachineHealth(m.id);
  const runners = useLoad(() => unwrap(client.GET('/api/v1/runners')), [client]);
  const s = machineStatus(m);
  const own =
    runners.result.state === 'ready'
      ? runners.result.data.items.filter((r) => r.machine_id === m.id)
      : [];
  return (
    <Stack gap="section">
      <Load result={health.result} what="the health" reload={health.reload}>
        {(h) => (
          <>
            <Stack gap="tight">
              <Status kind={s.kind} word={s.word} />
              <HealthBadges health={h} />
              {!h.reported ? (
                <Message tone="note">
                  This machine has not reported its health yet. Every value reads unknown
                  until it does.
                </Message>
              ) : h.stale && h.received_at ? (
                <Message tone="note">
                  The last report is from {formatAgo(h.received_at, now)}. These values
                  may be out of date.
                </Message>
              ) : null}
            </Stack>
            <Section title="Key numbers">
              <StatGrid>
                {metricsOf(h, m.os).map((x) => (
                  <StatTile
                    key={x.id}
                    label={x.label}
                    value={x.value}
                    {...(x.why ? { changeText: x.why } : {})}
                  />
                ))}
              </StatGrid>
            </Section>
            <DiskSection h={h} os={m.os} />
            <MemorySection m={m} h={h} />
            <NetworkSection h={h} os={m.os} />
            <UpdatesSection h={h} os={m.os} />
            <SoftwareSection h={h} os={m.os} />
            <HeatSection h={h} os={m.os} />
          </>
        )}
      </Load>
      <StatsSection m={m} runners={own} />
      {/* LOOK58: the shared Timeline of this machine's slots is mounted inside MachineTimeline. */}
      <Section
        title="Jobs over time"
        hint="The jobs of each slot over time will be shown here."
      >
        <MachineTimeline machineId={m.id} />
      </Section>
    </Stack>
  );
}

export function PoolMachineDetail() {
  const { machineId = '' } = useParams();
  const client = useClient();
  const { result, reload } = useLoad(
    () =>
      unwrap(
        client.GET('/api/v1/machines/{machineId}', { params: { path: { machineId } } }),
      ),
    [client, machineId],
  );
  const loaded =
    result.state === 'ready' || (result.state === 'failed' && result.data !== undefined);
  const name = result.state === 'ready' ? result.data.name : 'Machine';
  return (
    <Page>
      <Breadcrumbs
        trail={[{ to: '/pool/machines', label: 'Machines' }, { label: name }]}
      />
      {loaded ? null : <PageHeader title="Machine" />}
      <Load result={result} what="the machine" reload={reload}>
        {(m) => (
          <>
            <PageHeader
              title={m.name}
              lead="Health, job stats and runners of this machine. To change it, go to Setup > Machines."
            />
            <MachineBody m={m} />
          </>
        )}
      </Load>
    </Page>
  );
}
