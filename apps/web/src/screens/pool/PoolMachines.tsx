// Pool > Machines (LOOK56, LOOK66): what runs where; each card holds the table of its own runners. It only shows,
// and links to Setup > Machines, where machines change.
import {
  ActionBar,
  Empty,
  LinkButton,
  Message,
  Section,
  Stack,
  TextLink,
} from '@crsp/ui';
import { unwrap, useClient } from '../../api/api';
import { useLoad } from '../../api/hooks';
import { useMachines } from '../../api/machines';
import { useLiveState } from '../../live/LiveProvider';
import { Load } from '../common';
import { withLiveLoad } from '../overview/Overview';
import { PoolMachineCard } from './PoolMachineCard';

export function PoolMachines() {
  const machines = useMachines();
  const client = useClient();
  const runners = useLoad(() => unwrap(client.GET('/api/v1/runners')), [client]);
  const live = useLiveState();
  return (
    <Stack gap="section">
      <Message tone="note">
        This page shows what runs where. To change a machine, its pool size or its load
        budget, go to <TextLink to="/setup/machines">Setup &gt; Machines</TextLink>.
      </Message>
      <Load result={machines.result} what="machines" reload={machines.reload}>
        {(d) =>
          d.items.length === 0 ? (
            <Empty title="No machine is in the pool yet." />
          ) : (
            <>
              <Section title="Machines" count={d.items.length}>
                <Stack gap="section">
                  {d.items.map((m) => (
                    <PoolMachineCard
                      key={m.id}
                      m={withLiveLoad(m, live.overview)}
                      runners={runners.result}
                      reloadRunners={runners.reload}
                    />
                  ))}
                </Stack>
              </Section>
            </>
          )
        }
      </Load>
      <ActionBar
        primary={
          <LinkButton to="/setup/machines" kind="primary">
            Open Setup &gt; Machines
          </LinkButton>
        }
      />
    </Stack>
  );
}
