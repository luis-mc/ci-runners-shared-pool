// Pool > Machines (LOOK56): what runs where. It only shows, and links to Setup > Machines, where machines change.
import { Empty, Message, Section, Stack, Table, TextLink, type Column } from '@crsp/ui';
import { unwrap, useClient, type Schemas } from '../../api/api';
import { useLoad } from '../../api/hooks';
import { useMachines } from '../../api/machines';
import { useLiveState } from '../../live/LiveProvider';
import { Load } from '../common';
import { MachineCard, withLiveLoad } from '../overview/Overview';

type Runner = Schemas['Runner'];

const columns: Column<Runner>[] = [
  { id: 'runner', header: 'Runner', cell: (r) => r.name },
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
            <Stack gap="section">
              {d.items.map((m) => (
                <Section key={m.id} title={m.name}>
                  <MachineCard m={withLiveLoad(m, live.overview)} />
                  <Load
                    result={runners.result}
                    what="the runners"
                    reload={runners.reload}
                  >
                    {(r) => {
                      const mine = r.items.filter((x) => x.machine_id === m.id);
                      return mine.length === 0 ? (
                        <Empty title={`No runner on ${m.name}.`} />
                      ) : (
                        <Table
                          caption={`Runners on ${m.name}`}
                          columns={columns}
                          rows={mine}
                          rowKey={(x) => x.id}
                        />
                      );
                    }}
                  </Load>
                  <TextLink to={`/setup/machines/${m.id}`}>
                    Change {m.name} in Setup &gt; Machines
                  </TextLink>
                </Section>
              ))}
            </Stack>
          )
        }
      </Load>
    </Stack>
  );
}
