// Setup > Summary (LOOK64): three cards (Machines, Runners, Repositories), each with an Open link, and the Setup steps.
import {
  Button,
  Card,
  CardGrid,
  Facts,
  LinkButton,
  Page,
  Section,
  Stack,
  Status,
  SummaryLine,
  type SummaryItem,
  Text,
  TextLink,
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

/** The pool CPU and memory the machines give, as the Machines card says them (machines R5.58). */
export function poolGives(machines: readonly Machine[]): string {
  const cpus = machines.reduce((n, m) => n + (m.settings.pool_cpus ?? 0), 0);
  const gib = machines.reduce((n, m) => n + (m.settings.pool_memory_gib ?? 0), 0);
  const n = machines.length;
  const first = `${n} ${n === 1 ? 'machine' : 'machines'}`;
  return cpus > 0 && gib > 0
    ? `${first}, ${cpus} CPU and ${Number(gib.toFixed(1))} GiB in the pool.`
    : `${first}, pool size not set.`;
}

/** The Runners card line (UX-86): how many runners, how many persistent and how many ephemeral. */
export function runnerKinds(runners: readonly Runner[]): {
  total: SummaryItem[];
  states: SummaryItem[];
} {
  const p = runners.filter((r) => r.kind === 'persistent').length;
  return {
    total: [{ count: runners.length, word: 'runner', plural: 'runners' }],
    states: [
      { count: p, word: 'persistent' },
      { count: runners.length - p, word: 'ephemeral' },
    ],
  };
}

/** The "Setup steps" list (LOOK64): the three first-run steps, each done or with the action that does it. */
export function SetupSteps({ f, app }: { f: PoolFigures; app: boolean | undefined }) {
  const session = useSession();
  const admin = session.can('admin');
  const why = session.whyNot('admin');
  const done = <Status kind="ok" word="done" />;
  return (
    <Section title="Setup steps">
      <Facts
        items={[
          {
            name: 'Connect GitHub',
            value:
              app === undefined ? (
                <Text tone="muted">Not known yet.</Text>
              ) : app ? (
                done
              ) : (
                <Status kind="waiting" word="to do" />
              ),
          },
          {
            name: 'First machine',
            value:
              f.machines > 0 ? (
                done
              ) : admin ? (
                <LinkButton to="/setup/machines/add">Add a machine</LinkButton>
              ) : (
                <Button disabled reason={why}>
                  Add a machine
                </Button>
              ),
          },
          {
            name: 'First repository',
            value:
              f.repositories > 0 ? (
                done
              ) : f.machines === 0 ? (
                <Button disabled reason="Add a machine first.">
                  Choose repositories
                </Button>
              ) : (
                <LinkButton to="/setup/pool">Choose repositories</LinkButton>
              ),
          },
        ]}
      />
    </Section>
  );
}

/** The pool name and the way to rename it (runners R5.141). */
export function PoolNameSection() {
  const client = useClient();
  const session = useSession();
  const info = useLoad(() => unwrap(client.GET('/api/v1/pool/name')), []);
  const admin = session.can('admin');
  return (
    <Section title="Pool name">
      <Facts
        items={[
          {
            name: 'Name',
            value:
              info.result.state === 'ready' ? (
                info.result.data.name
              ) : (
                <Text tone="muted">Not known yet.</Text>
              ),
          },
          {
            name: 'Rename',
            value: admin ? (
              <LinkButton to="/setup/pool/rename">Rename the pool</LinkButton>
            ) : (
              <Button disabled reason={session.whyNot('admin')}>
                Rename the pool
              </Button>
            ),
          },
        ]}
      />
    </Section>
  );
}

export function SetupSummary() {
  const client = useClient();
  const machines = useMachines();
  const rv = useVersion('runners');
  const runners = useLoad(() => unwrap(client.GET('/api/v1/runners')), [rv]);
  const repos = useLoad(() => allRepositories(client, { pool: 'on' }), []);
  const setup = useLoad(
    () =>
      unwrap(client.GET('/api/v1/setup')).then(
        (s) => s.has_app,
        () => undefined,
      ),
    [],
  );

  return (
    <Page>
      <Load result={machines.result} what="the pool" reload={machines.reload}>
        {(m) => {
          // Each card shows as soon as its own data is in; a card still waiting says so alone (CRSP389).
          const rs =
            runners.result.state === 'ready' ? runners.result.data.items : undefined;
          const rp = repos.result.state === 'ready' ? repos.result.data.items : undefined;
          const f = poolFigures(m.items, rs ?? [], rp?.length ?? 0);
          const app = setup.result.state === 'ready' ? setup.result.data : undefined;
          const loading = <Text tone="muted">Loading the pool figures.</Text>;
          return (
            <Stack>
              <CardGrid>
                <Card title="Machines" count={f.machines}>
                  <Stack>
                    <Text>{poolGives(m.items)}</Text>
                    <TextLink to="/setup/machines">Open Machines</TextLink>
                  </Stack>
                </Card>
                <Card title="Runners" {...(rs ? { count: f.runners } : {})}>
                  <Stack>
                    {rs ? <SummaryLine {...runnerKinds(rs)} /> : loading}
                    <TextLink to="/setup/runners">Open Runners</TextLink>
                  </Stack>
                </Card>
                <Card title="Repositories" {...(rp ? { count: f.repositories } : {})}>
                  <Stack>
                    {rp ? (
                      <Text>
                        {f.repositories === 0
                          ? 'No repository uses the pool yet.'
                          : `${f.repositories} ${f.repositories === 1 ? 'repository uses' : 'repositories use'} the pool.`}
                      </Text>
                    ) : (
                      loading
                    )}
                    <TextLink to="/setup/pool">Open Pool setup</TextLink>
                  </Stack>
                </Card>
              </CardGrid>
              {rp ? <SetupSteps f={f} app={app} /> : null}
              <PoolNameSection />
            </Stack>
          );
        }}
      </Load>
    </Page>
  );
}
