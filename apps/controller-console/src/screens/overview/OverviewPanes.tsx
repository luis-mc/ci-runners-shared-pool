// The parts of the Overview that LOOK61 builds as the approved LOOK54 prototype shows them (design system UX-10,
// UX-52, UX-58): a machine card in cores and GiB, the timer alert, Next to start and Routing labels. The page passes
// data; every drawing is a shared part of @crsp/ui.
import {
  Card,
  Cluster,
  Code,
  LevelBar,
  Message,
  Stack,
  Status,
  Tag,
  Text,
  TextLink,
  formatPercent,
  type StatusKind,
} from '@crsp/ui';
import { useEffect, useState } from 'react';
import { unwrap, useClient, type Schemas } from '../../api/api';
import { machineStatus } from './Overview';
import { startsAbout } from '../pool/poolWords';

type Machine = Schemas['Machine'];
type Job = Schemas['Job'];

const GIB = 1024 ** 3;
const gib = (n: number): string => String(Math.round(n * 10) / 10);

/** "6 CPU / 9 GiB": what the machine has, from its facts; nothing when the agent has not reported them. */
export function sizeWords(m: Pick<Machine, 'facts'>): string | undefined {
  const cpus = m.facts?.threads ?? m.facts?.cores;
  const mem = m.facts?.memory_bytes;
  if (!cpus || !mem) return undefined;
  return `${cpus} CPU / ${gib(mem / GIB)} GiB`;
}

/** The names of the jobs that run on a machine, from the machine or else from the running list. */
export function jobNames(m: Machine, running: readonly Job[]): string[] {
  const jobs = m.running_jobs?.length
    ? m.running_jobs
    : running.filter((j) => j.machine_id === m.id);
  return jobs.map((j) => j.job_name);
}

/** CPU and Memory in cores and GiB: what the jobs use now, of what the machine has. Without its facts, as percent. */
export function MachineSizeBars({ m }: { m: Machine }) {
  const cpus = m.facts?.threads ?? m.facts?.cores;
  const mem = m.facts?.memory_bytes;
  const cpu = m.load?.cpu_pct;
  const memPct = m.load?.memory_pct;
  const totalGib = mem ? mem / GIB : undefined;
  return (
    <>
      {cpus && cpu !== undefined ? (
        <LevelBar
          label="CPU"
          value={(cpu / 100) * cpus}
          max={cpus}
          text={`${gib((cpu / 100) * cpus)} of ${cpus}`}
        />
      ) : (
        <LevelBar
          label="CPU"
          value={cpu ?? 0}
          max={100}
          text={cpu === undefined ? 'not known' : formatPercent(cpu)}
        />
      )}
      {totalGib && memPct !== undefined ? (
        <LevelBar
          label="Memory"
          value={(memPct / 100) * totalGib}
          max={totalGib}
          text={`${gib((memPct / 100) * totalGib)} of ${gib(totalGib)} GiB`}
        />
      ) : (
        <LevelBar
          label="Memory"
          value={memPct ?? 0}
          max={100}
          text={memPct === undefined ? 'not known' : formatPercent(memPct)}
        />
      )}
    </>
  );
}

/** A machine card of the Overview: pool size in the title, CPU and Memory in cores and GiB, the running job names and a link to Pool > Machines. */
export function OverviewMachineCard({
  m,
  running,
}: {
  m: Machine;
  running: readonly Job[];
}) {
  const s = machineStatus(m);
  const size = sizeWords(m);
  const names = jobNames(m, running);
  return (
    <Card
      title={size ? `${m.name} ${size}` : m.name}
      machine
      to={`/pool/machines/${m.id}`}
    >
      <Stack gap="tight">
        {s.kind === 'ok' ? null : <Status kind={s.kind} word={s.word} />}
        <MachineSizeBars m={m} />
        {names.length > 0 ? <Text tone="muted">{names.join(', ')}</Text> : null}
        <Text>
          <TextLink to={`/pool/machines/${m.id}`}>Open in Pool &gt; Machines</TextLink>
        </Text>
      </Stack>
    </Card>
  );
}

/** The timer alert: a running job whose job timer ran out and is stopping (scheduling R5.100). */
export function TimerAlert({ running }: { running: readonly Job[] }) {
  const client = useClient();
  const [timers, setTimers] = useState<Schemas['JobTimer'][]>([]);
  const key = running.map((j) => j.id).join(',');
  useEffect(() => {
    let on = true;
    const ids = key ? key.split(',').map(Number) : [];
    void Promise.all(
      ids.map((id) =>
        unwrap(
          client.GET('/api/v1/jobs/{jobId}/timer', { params: { path: { jobId: id } } }),
        ).catch(() => undefined),
      ),
    ).then((all) => {
      if (on)
        setTimers(all.filter((t): t is Schemas['JobTimer'] => t?.state === 'grace'));
    });
    return () => {
      on = false;
    };
  }, [client, key]);
  return (
    <>
      {timers.map((t) => (
        <Message
          key={t.job_id}
          tone="problem"
          action={
            <TextLink to={`/pool/jobs/${t.job_id}`}>
              {`Open ${running.find((j) => j.id === t.job_id)?.job_name ?? 'the job'} in Pool > Jobs`}
            </TextLink>
          }
        >
          {t.text}
        </Message>
      ))}
    </>
  );
}

const STARTS: Record<string, { kind: StatusKind; word: string }> = {
  repository_parallel_limit: { kind: 'waiting', word: 'Behind another job' },
  free_slot: { kind: 'waiting', word: 'Waiting for room' },
  load_over_budget: { kind: 'waiting', word: 'Waiting for room' },
  over_memory: { kind: 'waiting', word: 'Waiting for room' },
  no_runner_with_labels: { kind: 'failed', word: 'No runner fits' },
  machine_drained: { kind: 'failed', word: 'No runner fits' },
};

/** One word for why a queued job is not running yet. */
export function startWord(j: Pick<Job, 'wait_reason'>): {
  kind: StatusKind;
  word: string;
} {
  return (j.wait_reason && STARTS[j.wait_reason]) || { kind: 'waiting', word: 'Waiting' };
}

const NEXT_SHOWN = 3;

/** Next to start: the first jobs of the queue in the order crsp starts them, with the clock time of each. */
export function NextToStart({
  queued,
  count,
  readAt,
}: {
  queued: readonly Job[];
  count: number;
  readAt: number;
}) {
  const rows = [...queued]
    .sort((a, b) => (a.queue_position ?? 1e9) - (b.queue_position ?? 1e9))
    .slice(0, NEXT_SHOWN);
  return (
    <Card title="Next to start" count={count}>
      <Stack gap="tight">
        {rows.length === 0 ? <Text tone="muted">No job is waiting.</Text> : null}
        {rows.map((j) => {
          const w = startWord(j);
          return (
            <Cluster key={j.id}>
              <Text>
                <Code>{j.job_name}</Code> <Status kind={w.kind} word={w.word} />
              </Text>
              <Text tone="muted">{startsAbout(j.queue_eta, readAt).toLowerCase()}</Text>
            </Cluster>
          );
        })}
        <Text>
          <TextLink to="/pool/queue">
            {`See the whole plan in Pool > Queue Analysis (${count})`}
          </TextLink>
        </Text>
      </Stack>
    </Card>
  );
}

const LABELS_SHOWN = 5;

/** The routing labels that running and waiting jobs ask for, once each, sorted. */
export function labelsInUse(jobs: readonly Pick<Job, 'labels'>[]): string[] {
  return [...new Set(jobs.flatMap((j) => j.labels))].sort((a, b) => a.localeCompare(b));
}

/** Routing labels: the first labels in use, as tags, and the way to all of them. */
export function RoutingLabelsPane({ jobs }: { jobs: readonly Job[] }) {
  const all = labelsInUse(jobs);
  return (
    <Card title="Routing labels">
      <Stack gap="tight">
        {all.length === 0 ? (
          <Text tone="muted">No job carries a routing label right now.</Text>
        ) : (
          <Cluster gap="tight">
            {all.slice(0, LABELS_SHOWN).map((l) => (
              <Tag key={l}>{l}</Tag>
            ))}
          </Cluster>
        )}
        <Text>
          <TextLink to="/pool">{`See all ${all.length} labels in Pool > Overview`}</TextLink>
        </Text>
      </Stack>
    </Card>
  );
}
