// Setup > Size and backends (D24, LOOK31): one row per machine with its job slots and the backends it allows.
// The size wizard and the policy stay on the machine's own page; this page is the pool-wide view with a way in.
import {
  ClampText,
  Empty,
  LinkButton,
  Page,
  Section,
  Status,
  Table,
  Tag,
  Text,
  type Column,
} from '@crsp/ui';
import { useMachines } from '../../api/machines';
import { unwrap, useClient, type Schemas } from '../../api/api';
import { useLoad } from '../../api/hooks';
import { DemandSection } from '../insights/RepositoryPanes';
import { Load } from '../common';

type Machine = Schemas['Machine'];
type ClassSize = Schemas['RunnerClassSize'];

export interface BackendRow {
  name: string;
  allowed: number;
  usable: number;
  machines: number;
  version: string | undefined;
}

/** One row per backend any machine reports: on how many machines it is usable and allowed, and its version. */
export function backendRows(machines: readonly Machine[]): BackendRow[] {
  const by = new Map<string, BackendRow>();
  for (const m of machines) {
    for (const b of m.backends) {
      if (!b.present && !b.usable) continue;
      const row = by.get(b.name) ?? {
        name: b.name,
        allowed: 0,
        usable: 0,
        machines: 0,
        version: undefined,
      };
      row.machines += 1;
      if (b.usable) row.usable += 1;
      if (b.usable && b.allowed) row.allowed += 1;
      row.version ??= b.version;
      by.set(b.name, row);
    }
  }
  return [...by.values()].sort((a, b) => a.name.localeCompare(b.name));
}

/** The Size classes table (small to xl), read from the runner classes (runners R5.53). */
function SizeClasses() {
  const client = useClient();
  const { result } = useLoad(() => unwrap(client.GET('/api/v1/runner-classes')), []);
  if (result.state !== 'ready') return null;
  const columns: Column<ClassSize>[] = [
    { id: 'class', header: 'Class', cell: (c) => <Tag>{c.class}</Tag> },
    { id: 'cpus', header: 'CPU', cell: (c) => String(c.cpus) },
    { id: 'memory', header: 'Memory', cell: (c) => `${c.memory_gib} GiB` },
  ];
  return (
    <Section title="Size classes" count={result.data.classes.length}>
      <Text tone="muted">{`Preset: ${result.data.preset}.`}</Text>
      <Table
        caption="Size classes"
        columns={columns}
        rows={result.data.classes}
        rowKey={(c) => c.class}
      />
    </Section>
  );
}

export function allowedBackends(m: Pick<Machine, 'backends'>): string {
  return (
    m.backends
      .filter((b) => b.allowed && b.usable)
      .map((b) => b.name)
      .join(', ') || 'none'
  );
}

const backendColumns: Column<BackendRow>[] = [
  { id: 'name', header: 'Backend', cell: (b) => b.name, sort: (b) => b.name },
  {
    id: 'allowed',
    header: 'Allowed',
    cell: (b) =>
      b.allowed > 0 ? (
        <Status
          kind="ok"
          word={
            b.allowed === b.machines
              ? 'allowed'
              : `allowed on ${b.allowed} of ${b.machines}`
          }
        />
      ) : (
        <Status kind="off" word="off" />
      ),
  },
  { id: 'version', header: 'Version', cell: (b) => b.version ?? '—' },
];

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
      <Text tone="muted">
        How many jobs each machine runs at once, and how it isolates them.
      </Text>
      <SizeClasses />
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
            <>
              <Section title="Backends" count={backendRows(data.items).length}>
                <Table
                  caption="Backends"
                  columns={backendColumns}
                  rows={backendRows(data.items)}
                  rowKey={(b) => b.name}
                />
              </Section>
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
            </>
          )
        }
      </Load>
      <DemandSection />
    </Page>
  );
}
