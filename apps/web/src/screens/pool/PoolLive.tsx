// Pool > Live (LOOK56, option A; LOOK58): the jobs of the pool on the slots of the machines over time, and the load of
// each machine. The chart is the one shared slot timeline; the machines change as events arrive.
import { CardGrid, Section, Stack } from '@crsp/ui';
import { MachineCard, withLiveLoad } from '../overview/Overview';
import { useMachines } from '../../api/machines';
import { useLiveState } from '../../live/LiveProvider';
import { Load } from '../common';
import { SlotTimeline } from './SlotTimeline';

export function PoolLive() {
  const live = useLiveState();
  const machines = useMachines();
  return (
    <Stack gap="section">
      <Section
        title="Jobs over time"
        hint="Each row is a slot of a machine. Scroll left for the past 24 hours, right for the forecast."
      >
        <SlotTimeline />
      </Section>
      <Section title="Machines">
        <Load result={machines.result} what="machines" reload={machines.reload}>
          {(d) => (
            <CardGrid>
              {d.items.map((m) => (
                <MachineCard key={m.id} m={withLiveLoad(m, live.overview)} />
              ))}
            </CardGrid>
          )}
        </Load>
      </Section>
    </Stack>
  );
}
