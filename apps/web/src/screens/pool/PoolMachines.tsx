// Pool > Machines (LOOK56): what runs where. It only shows, and links to Setup > Machines, where machines change.
import {
  ActionBar,
  CardGrid,
  ClampText,
  Empty,
  LinkButton,
  Message,
  Section,
  Stack,
  Table,
  TextLink,
  type Column,
} from '@crsp/ui';
import { unwrap, useClient, type Schemas } from '../../api/api';
import { useLoad } from '../../api/hooks';
import { useMachines } from '../../api/machines';
import { useLiveState } from '../../live/LiveProvider';
import { Load } from '../common';
import { withLiveLoad } from '../overview/Overview';
import { PoolMachineCard } from './PoolMachineCard';

type Runner = Schemas['Runner'];

const columnsOf = (names: ReadonlyMap<string, string>): Column<Runner>[] => [
  { id: 'runner', header: 'Runner', cell: (r) => <ClampText text={r.name} /> },
  {
    id: 'machine',
    header: 'Machine',
    cell: (r) => {
      const name = r.machine_id ? names.get(r.machine_id) : undefined;
      return name ? <ClampText text={name} /> : 'none';
    },
  },
  { id: 'type', header: 'Type', cell: (r) => r.kind },
  { id: 'now', header: 'Doing now', cell: (r) => r.current_job?.job_name ?? 'idle' },
];

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
                <CardGrid>
                  {d.items.map((m) => (
                    <PoolMachineCard
                      key={m.id}
                      m={withLiveLoad(m, live.overview)}
                      runners={
                        runners.result.state === 'ready'
                          ? runners.result.data.items.filter((r) => r.machine_id === m.id)
                          : []
                      }
                    />
                  ))}
                </CardGrid>
              </Section>
              <Section title="Runners">
                <Load result={runners.result} what="the runners" reload={runners.reload}>
                  {(r) => {
                    const names = new Map(d.items.map((m) => [m.id, m.name]));
                    return r.items.length === 0 ? (
                      <Empty title="No runner is in the pool yet." />
                    ) : (
                      <Table
                        caption="Runners by machine"
                        columns={columnsOf(names)}
                        rows={r.items}
                        rowKey={(x) => x.id}
                      />
                    );
                  }}
                </Load>
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
