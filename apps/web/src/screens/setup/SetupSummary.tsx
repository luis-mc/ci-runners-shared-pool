// Setup > Summary (D24, LOOK31): what the pool is now (machines, runners per system, capacity, health) and,
// until it is set up, the first-run guide: 1 Add a machine, 2 Set runners, 3 Choose repositories.
import {
  ActionBar,
  Button,
  Facts,
  LevelBar,
  LinkButton,
  Page,
  PageHeader,
  Section,
  Stack,
  Status,
  Table,
  Text,
  type Column,
} from '@crsp/ui';
import { unwrap, useClient, type Schemas } from '../../api/api';
import { useMachines } from '../../api/machines';
import { useLoad } from '../../api/hooks';
import { useSession } from '../../auth/session';
import { useVersion } from '../../live/LiveProvider';
import { Load } from '../common';
import { allRepositories } from '../repositories/plans';

type Machine = Schemas['Machine'];
type Runner = Schemas['Runner'];

const SYSTEMS = [
  { os: 'linux', label: 'Linux' },
  { os: 'windows', label: 'Windows' },
  { os: 'macos', label: 'macOS' },
] as const;

export interface PoolFigures {
  machines: number;
  online: number;
  runners: number;
  busy: number;
  failed: number;
  slots: number;
  slotsUsed: number;
  repositories: number;
}

export function poolFigures(
  machines: readonly Machine[],
  runners: readonly Runner[],
  repositories: number,
): PoolFigures {
  return {
    machines: machines.length,
    online: machines.filter((m) => m.online).length,
    runners: runners.length,
    busy: runners.filter((r) => r.state === 'busy').length,
    failed: runners.filter((r) => r.state === 'failed' || r.state === 'offline').length,
    slots: machines.reduce((n, m) => n + m.settings.job_slots, 0),
    slotsUsed: machines.reduce((n, m) => n + m.slots_used, 0),
    repositories,
  };
}

/** The line at the top of Runners and the Summary: "2 machines, 6 runners, 2 busy". */
export function poolTotals(
  f: Pick<PoolFigures, 'machines' | 'runners' | 'busy'>,
): string {
  const n = (c: number, one: string, many: string) => `${c} ${c === 1 ? one : many}`;
  return `${n(f.machines, 'machine', 'machines')}, ${n(f.runners, 'runner', 'runners')}, ${f.busy} busy`;
}

export function FirstRunGuide({ f }: { f: PoolFigures }) {
  const session = useSession();
  const admin = session.can('admin');
  const why = session.whyNot('admin');
  return (
    <Section
      title="Set up the pool"
      hint="Three steps. A step opens when the one before it is done."
    >
      <Facts
        items={[
          {
            name: '1 Add a machine',
            value:
              f.machines > 0 ? (
                <Status
                  kind="ok"
                  word={`done, ${f.machines} ${f.machines === 1 ? 'machine' : 'machines'}`}
                />
              ) : (
                <Stack>
                  <Text>A computer that runs your jobs: Linux, Windows or macOS.</Text>
                  {admin ? (
                    <LinkButton to="/setup/machines/add">Add a machine</LinkButton>
                  ) : (
                    <Button disabled reason={why}>
                      Add a machine
                    </Button>
                  )}
                </Stack>
              ),
          },
          {
            name: '2 Set runners',
            value:
              f.runners > 0 ? (
                <Status
                  kind="ok"
                  word={`done, ${f.runners} ${f.runners === 1 ? 'runner' : 'runners'}`}
                />
              ) : (
                <Stack>
                  <Text>How many runners each machine has, per system.</Text>
                  {f.machines === 0 ? (
                    <Button disabled reason="Add a machine first.">
                      Add runners
                    </Button>
                  ) : admin ? (
                    <LinkButton to="/setup/runners/add">Add runners</LinkButton>
                  ) : (
                    <Button disabled reason={why}>
                      Add runners
                    </Button>
                  )}
                </Stack>
              ),
          },
          {
            name: '3 Choose repositories',
            value:
              f.repositories > 0 ? (
                <Status
                  kind="ok"
                  word={`done, ${f.repositories} ${f.repositories === 1 ? 'repository uses' : 'repositories use'} the pool`}
                />
              ) : (
                <Stack>
                  <Text>Pick the repositories that use the pool.</Text>
                  {f.machines === 0 ? (
                    <Button disabled reason="Add a machine first.">
                      Choose repositories
                    </Button>
                  ) : (
                    <LinkButton to="/setup/pool">Choose repositories</LinkButton>
                  )}
                </Stack>
              ),
          },
        ]}
      />
    </Section>
  );
}

interface SystemRow {
  label: string;
  machines: number;
  runners: number;
  busy: number;
}

export function SetupSummary() {
  const client = useClient();
  const machines = useMachines();
  const rv = useVersion('runners');
  const runners = useLoad(() => unwrap(client.GET('/api/v1/runners')), [rv]);
  const repos = useLoad(() => allRepositories(client, { pool: 'on' }), []);
  const session = useSession();

  return (
    <Page>
      <PageHeader
        title="Setup"
        lead="One pool across your machines. Set it up here, then choose the repositories that use it."
      />
      <Load result={machines.result} what="the pool" reload={machines.reload}>
        {(m) => {
          const rs = runners.result.state === 'loading' ? undefined : runners.result.data;
          const rp = repos.result.state === 'loading' ? undefined : repos.result.data;
          if (!rs || !rp) return <Text tone="muted">Loading the pool figures.</Text>;
          const f = poolFigures(m.items, rs.items, rp.items.length);
          const ready = f.machines > 0 && f.runners > 0 && f.repositories > 0;
          const rows: SystemRow[] = SYSTEMS.map((s) => ({
            label: s.label,
            machines: m.items.filter((x) => x.os === s.os).length,
            runners: rs.items.filter((r) => r.os === s.os).length,
            busy: rs.items.filter((r) => r.os === s.os && r.state === 'busy').length,
          })).filter((r) => r.machines > 0 || r.runners > 0);
          const cols: Column<SystemRow>[] = [
            { id: 'system', header: 'System', cell: (r) => r.label },
            { id: 'machines', header: 'Machines', cell: (r) => String(r.machines) },
            { id: 'runners', header: 'Runners', cell: (r) => String(r.runners) },
            { id: 'busy', header: 'Busy', cell: (r) => String(r.busy) },
          ];
          return (
            <Stack>
              {ready ? null : <FirstRunGuide f={f} />}
              {f.machines > 0 ? (
                <>
                  <Section title="The pool">
                    <Stack>
                      <Facts
                        items={[
                          {
                            name: 'Machines',
                            value: `${f.online} of ${f.machines} online`,
                          },
                          { name: 'Runners', value: `${f.runners}, ${f.busy} busy` },
                          {
                            name: 'Repositories',
                            value:
                              f.repositories === 0
                                ? 'none use the pool yet'
                                : `${f.repositories} use the pool`,
                          },
                          {
                            name: 'Health',
                            value:
                              f.online < f.machines || f.failed > 0 ? (
                                <Status
                                  kind="failed"
                                  word={`${f.machines - f.online} offline, ${f.failed} not ready`}
                                />
                              ) : (
                                <Status kind="ok" word="all ready" />
                              ),
                          },
                        ]}
                      />
                      <LevelBar
                        label="Capacity"
                        value={f.slotsUsed}
                        max={f.slots}
                        text={`${f.slotsUsed} of ${f.slots} job slots in use`}
                      />
                    </Stack>
                  </Section>
                  <Section title="Runners per system">
                    <Table
                      caption="Runners per system"
                      columns={cols}
                      rows={rows}
                      rowKey={(r) => r.label}
                    />
                  </Section>
                </>
              ) : null}
            </Stack>
          );
        }}
      </Load>
      {machines.result.state === 'ready' &&
      machines.result.data.items.length > 0 &&
      session.can('admin') ? (
        <ActionBar
          primary={
            <LinkButton to="/setup/runners/add" kind="primary">
              Add runners
            </LinkButton>
          }
        />
      ) : null}
    </Page>
  );
}
