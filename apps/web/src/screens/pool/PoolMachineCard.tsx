// A machine on Pool > Machines (LOOK58c, LOOK66): its status and health badges, the key numbers, the job stats of
// the last 24 hours or 7 days, and the table "Runners on <machine>" (Runner, Type, Size, Doing now). The title opens
// the machine page. The runners come as the loaded list, never as an empty array while it loads (LOOK66).
import {
  Card,
  ClampText,
  Empty,
  Stack,
  Status,
  Strong,
  Table,
  Text,
  type Column,
} from '@crsp/ui';
import { useState } from 'react';
import type { Schemas } from '../../api/api';
import type { Loaded } from '../../api/hooks';
import { Load } from '../common';
import { MachineLoadBars, machineStatus } from '../overview/Overview';
import type { StatsWindow } from './machineHealth';
import {
  HealthBadges,
  MetricFacts,
  StatsSummary,
  WindowSwitch,
  useMachineHealth,
  useMachineStats,
} from './MachineStats';

type Machine = Schemas['Machine'];
type Runner = Schemas['Runner'];

const columns: Column<Runner>[] = [
  {
    id: 'runner',
    header: 'Runner',
    cell: (r) => <ClampText text={r.name} />,
    kind: 'grow',
  },
  { id: 'type', header: 'Type', cell: (r) => r.kind },
  // A runner with no declared class takes the default one (runners R5.71).
  { id: 'size', header: 'Size', cell: (r) => r.size_class ?? 'medium' },
  {
    id: 'now',
    header: 'Doing now',
    cell: (r) => <ClampText text={r.current_job?.job_name ?? 'idle'} />,
    kind: 'grow',
  },
];

/** "Runners on <machine>": the runners of one machine from the loaded runner list. */
export function MachineRunners({
  machine,
  runners,
  reload,
}: {
  machine: Pick<Machine, 'id' | 'name'>;
  runners: Loaded<Schemas['RunnerList']>;
  reload: () => void;
}) {
  return (
    <Stack gap="tight">
      <Text>
        <Strong>Runners on {machine.name}</Strong>
      </Text>
      <Load result={runners} what={`the runners on ${machine.name}`} reload={reload}>
        {(d) => {
          const rows = d.items.filter((r) => r.machine_id === machine.id);
          return rows.length === 0 ? (
            <Empty title="No runner is on this machine." />
          ) : (
            <Table
              caption={`Runners on ${machine.name}`}
              columns={columns}
              rows={rows}
              rowKey={(r) => r.id}
            />
          );
        }}
      </Load>
    </Stack>
  );
}

export function PoolMachineCard({
  m,
  runners,
  reloadRunners,
}: {
  m: Machine;
  runners: Loaded<Schemas['RunnerList']>;
  reloadRunners: () => void;
}) {
  const [window, setWindow] = useState<StatsWindow>('24h');
  const health = useMachineHealth(m.id);
  const stats = useMachineStats(m.id, window);
  const s = machineStatus(m);
  return (
    <Card title={m.name} machine to={`/pool/machines/${m.id}`}>
      <Stack gap="tight">
        <Status kind={s.kind} word={s.word} />
        <MachineLoadBars m={m} />
        <Load
          result={health.result}
          what={`the health of ${m.name}`}
          reload={health.reload}
        >
          {(d) => (
            <>
              <HealthBadges health={d} />
              <MetricFacts health={d} os={m.os} />
            </>
          )}
        </Load>
        <WindowSwitch value={window} onChange={setWindow} />
        <Load
          result={stats.result}
          what={`the job stats of ${m.name}`}
          reload={stats.reload}
        >
          {(d) => <StatsSummary stats={d.machine} machineId={m.id} window={window} />}
        </Load>
        <MachineRunners machine={m} runners={runners} reload={reloadRunners} />
      </Stack>
    </Card>
  );
}
