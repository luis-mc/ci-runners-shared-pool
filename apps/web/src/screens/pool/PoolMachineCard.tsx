// A machine on Pool > Machines (LOOK58c): its status and health badges, the key numbers, the job stats of the last 24
// hours or 7 days, and its runners each with their own stats. The title opens the machine page.
import { Card, Facts, Stack, Status, Text } from '@crsp/ui';
import { useState } from 'react';
import type { Schemas } from '../../api/api';
import { Load } from '../common';
import { MachineLoadBars, machineStatus } from '../overview/Overview';
import { runnerLine, runnerRowsOf, type StatsWindow } from './machineHealth';
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

/** The runners of a machine with their stats: the runners the list names, plus any runner that ran jobs on it. */
export function RunnerStatLines({
  runners,
  stats,
}: {
  runners: readonly Pick<Runner, 'id' | 'name'>[];
  stats: Schemas['MachineJobStats'];
}) {
  const rows = runnerRowsOf(runners, stats);
  if (rows.length === 0) return <Text tone="muted">No runner is on this machine.</Text>;
  return (
    <Facts
      items={rows.map((r) => ({
        name: r.name,
        value: runnerLine(r.stats),
      }))}
    />
  );
}

export function PoolMachineCard({
  m,
  runners,
}: {
  m: Machine;
  runners: readonly Runner[];
}) {
  const [window, setWindow] = useState<StatsWindow>('24h');
  const health = useMachineHealth(m.id);
  const stats = useMachineStats(m.id, window);
  const s = machineStatus(m);
  return (
    <Card title={m.name} to={`/pool/machines/${m.id}`}>
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
          {(d) => (
            <Stack gap="tight">
              <StatsSummary stats={d.machine} machineId={m.id} window={window} />
              <RunnerStatLines runners={runners} stats={d} />
            </Stack>
          )}
        </Load>
      </Stack>
    </Card>
  );
}
