// Setup > Size and backends (D24, LOOK31): one row per machine with its job slots and the backends it allows.
// The size wizard and the policy stay on the machine's own page; this page is the pool-wide view with a way in.
import {
  ClampText,
  Empty,
  LinkButton,
  Page,
  PageHeader,
  Section,
  Table,
  type Column,
} from '@crsp/ui';
import { useMachines } from '../../api/machines';
import type { Schemas } from '../../api/api';
import { DemandSection } from '../insights/RepositoryPanes';
import { Load } from '../common';

type Machine = Schemas['Machine'];

export function allowedBackends(m: Pick<Machine, 'backends'>): string {
  return (
    m.backends
      .filter((b) => b.allowed && b.usable)
      .map((b) => b.name)
      .join(', ') || 'none'
  );
}

export function SizePage() {
  const { result, reload } = useMachines();
  const columns: Column<Machine>[] = [
    {
      id: 'name',
      header: 'Machine',
      cell: (m) => <ClampText text={m.name} />,
      sort: (m) => m.name,
    },
    {
      id: 'slots',
      header: 'Job slots',
      cell: (m) => `${m.slots_used} of ${m.settings.job_slots} in use`,
    },
    { id: 'backends', header: 'Backends', cell: (m) => allowedBackends(m) },
  ];
  return (
    <Page>
      <PageHeader
        title="Size and backends"
        lead="How many jobs each machine runs at once, and how it isolates them."
      />
      <DemandSection />
      <Load result={result} what="the machines" reload={reload}>
        {(data) =>
          data.items.length === 0 ? (
            <Section title="Machines" count={0}>
              <Empty
                title="No machines yet. Add a machine, then set its size here."
                action={<LinkButton to="/setup/machines/add">Add a machine</LinkButton>}
              />
            </Section>
          ) : (
            <Section title="Machines" count={data.items.length}>
              <Table
                caption="Size and backends"
                columns={columns}
                rows={data.items}
                rowKey={(m) => m.id}
                action={{
                  label: 'Open',
                  to: (m) => `/setup/machines/${m.id}`,
                  name: (m) => m.name,
                }}
              />
            </Section>
          )
        }
      </Load>
    </Page>
  );
}
