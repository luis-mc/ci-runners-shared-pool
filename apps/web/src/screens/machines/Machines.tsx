// Machines (web-console 4.4, R5.22): list, detail, budget, slots, drain, resume, remove.
import { HelpTerm } from '../help/HelpTerm';
import {
  Button,
  Card,
  CardGrid,
  LevelBar,
  LinkButton,
  Chips,
  CheckRow,
  Empty,
  ErrorSummary,
  Facts,
  FieldGrid,
  Message,
  NumberField,
  Page,
  PageHeader,
  Pane,
  ListToolbar,
  SearchBar,
  Section,
  Stack,
  Status,
  Table,
  Breadcrumbs,
  Text,
  TextLink,
  formatPast,
  formatPercent,
  formatSize,
  type Column,
  ActionBar,
  EditBar,
} from '@crsp/ui';
import { lazy, Suspense, useMemo, useState } from 'react';
import { useNavigate, useParams } from 'react-router-dom';
import { unwrap, useClient, type Schemas } from '../../api/api';
import { useCommand, useLoad, useQuestion } from '../../api/hooks';
import { useMachines } from '../../api/machines';
import { useSession } from '../../auth/session';
import { useVersion } from '../../live/LiveProvider';
import { Ask, Load } from '../common';
import { machineStatus } from '../overview/Overview';
import { MachineDockerDiskSection } from './MachineDockerDisk';
import { MachinePolicySection, PolicyStatus, takingState } from './MachinePolicy';
import { usePoolSize } from './PoolSize';
import { RecoverySection } from './Recovery';

const MachineRemove = lazy(() => import('./MachineRemove'));

type Machine = Schemas['Machine'];

const STATES = [
  { value: 'active', label: 'Online' },
  { value: 'draining', label: 'Finishing jobs' },
  { value: 'drained', label: 'Not taking jobs' },
] as const;

/** The pool of a machine in one cell: "6 CPU / 9 GiB", or "not set" until the owner sizes it (machines R5.58). */
export function poolText(m: Machine): string {
  const cpus = m.settings.pool_cpus ?? 0;
  const gib = m.settings.pool_memory_gib ?? 0;
  return cpus > 0 && gib > 0 ? `${cpus} CPU / ${Number(gib.toFixed(1))} GiB` : 'not set';
}

/** One "pool size" card per machine (LOOK64): CPU and memory in the pool against what the machine has. */
export function PoolSizeCard({ m }: { m: Machine }) {
  const cpus = m.settings.pool_cpus ?? 0;
  const gib = m.settings.pool_memory_gib ?? 0;
  const cores = m.facts?.cores;
  const total = m.facts?.memory_bytes ? m.facts.memory_bytes / 2 ** 30 : undefined;
  return (
    <Card title={`${m.name}: pool size`}>
      <Stack>
        {cpus > 0 && cores ? (
          <LevelBar
            label="CPU in the pool"
            value={cpus}
            max={cores}
            text={`${cpus} of ${cores} CPU`}
          />
        ) : (
          <Text>{`CPU in the pool: ${cpus > 0 ? `${cpus} CPU` : 'not set'}`}</Text>
        )}
        {gib > 0 && total ? (
          <LevelBar
            label="Memory in the pool"
            value={gib}
            max={Number(total.toFixed(1))}
            text={`${Number(gib.toFixed(1))} of ${Number(total.toFixed(1))} GiB`}
          />
        ) : (
          <Text>{`Memory in the pool: ${gib > 0 ? `${Number(gib.toFixed(1))} GiB` : 'not set'}`}</Text>
        )}
        <LinkButton to={`/setup/machines/${m.id}`}>Change the pool size</LinkButton>
      </Stack>
    </Card>
  );
}

export function MachinesList() {
  const session = useSession();
  const navigate = useNavigate();
  const { result, reload } = useMachines();
  const [q, setQ] = useState('');
  const [state, setState] = useState<'' | 'active' | 'draining' | 'drained'>('');

  const columns: Column<Machine>[] = [
    {
      id: 'name',
      header: 'Machine',
      cell: (m) => <TextLink to={`/setup/machines/${m.id}`}>{m.name}</TextLink>,
      sort: (m) => m.name,
    },
    { id: 'os', header: 'System', cell: (m) => systemText(m), sort: (m) => m.os },
    { id: 'pool', header: 'Pool', cell: (m) => poolText(m) },
    {
      id: 'budget',
      header: 'Load budget',
      cell: (m) =>
        `${m.settings.budget_memory_pct}% memory, ${m.settings.budget_cpu_pct}% CPU`,
    },
    {
      id: 'state',
      header: 'State',
      // One word: offline and the like first, else whether it takes jobs (UX-58).
      cell: (m) => {
        const s = machineStatus(m);
        return s.kind === 'ok' ? (
          <PolicyStatus m={m} />
        ) : (
          <Status kind={s.kind} word={s.word} />
        );
      },
      sort: (m) => machineStatus(m).word,
    },
  ];

  return (
    <Page>
      <PageHeader title="Machines" lead="What each machine gives the pool." />
      <Load result={result} what="machines" reload={reload}>
        {(data) => {
          const rows = data.items.filter(
            (m) =>
              (!state || m.state === state) &&
              m.name.toLowerCase().includes(q.trim().toLowerCase()),
          );
          if (data.items.length === 0)
            return <Empty title="No machines yet. Use Add a machine above to start." />;
          return (
            <Pane title="Machines" count={data.items.length}>
              <ListToolbar
                label="Machines"
                search={<SearchBar value={q} onChange={setQ} label="Search machines" />}
                filters={
                  <Chips
                    label="State"
                    options={STATES}
                    value={state}
                    onChange={setState}
                  />
                }
                count={`${rows.length} ${rows.length === 1 ? 'machine' : 'machines'}`}
              />
              {rows.length === 0 ? (
                <Message
                  tone="note"
                  action={
                    <Button
                      onPress={() => {
                        setQ('');
                        setState('');
                      }}
                    >
                      Clear search and filter
                    </Button>
                  }
                >
                  {data.items.length} machines are hidden by the search and the filter.
                  Clear them to see all.
                </Message>
              ) : (
                <Table
                  caption="Machines"
                  columns={columns}
                  rows={rows}
                  rowKey={(m) => m.id}
                />
              )}
            </Pane>
          );
        }}
      </Load>
      {result.state === 'ready' && result.data.items.length > 0 ? (
        <CardGrid>
          {result.data.items.map((m) => (
            <PoolSizeCard key={m.id} m={m} />
          ))}
        </CardGrid>
      ) : null}
      <ActionBar
        primary={
          <Button
            kind="primary"
            disabled={!session.can('admin')}
            reason={session.can('admin') ? undefined : session.whyNot('admin')}
            onPress={() => void navigate('/setup/machines/add')}
          >
            Add a machine
          </Button>
        }
      />
    </Page>
  );
}

export function MachineDetail() {
  const { id = '' } = useParams();
  const client = useClient();
  const v = useVersion('machines');
  const { result, reload } = useLoad(
    () =>
      unwrap(
        client.GET('/api/v1/machines/{machineId}', {
          params: { path: { machineId: id } },
        }),
      ),
    [id, v],
  );
  return (
    <Page>
      <Breadcrumbs
        trail={[
          { to: '/setup/machines', label: 'Machines' },
          { label: result.state === 'ready' ? result.data.name : 'Machine' },
        ]}
      />
      {result.state === 'ready' ||
      (result.state === 'failed' && result.data !== undefined) ? null : (
        <PageHeader title="Machine" />
      )}
      <Load result={result} what="the machine" reload={reload}>
        {(m) => <MachineBody m={m} onChanged={reload} />}
      </Load>
    </Page>
  );
}

function MachineBody({ m, onChanged }: { m: Machine; onChanged: () => void }) {
  const client = useClient();
  const session = useSession();
  const navigate = useNavigate();
  const admin = session.can('admin');
  const why = session.whyNot('admin');
  const s = machineStatus(m);
  const finishing = m.pending_change?.waiting_for?.length ?? m.running_jobs?.length ?? 0;
  // One state (OPS29): the State fact, the Now line, its note and the header button all read `taking`.
  const t = takingState(m);
  const stopped = t.reason === 'stopped';
  const special = m.recovery?.state === 'needs_recovery' || m.state === 'removing';
  const state = special
    ? { kind: s.kind, word: s.word }
    : stopped && m.state === 'draining'
      ? {
          kind: 'waiting' as const,
          word: `Finishing ${finishing} ${finishing === 1 ? 'job' : 'jobs'} (up to ${m.settings.drain_timeout_minutes} min)`,
        }
      : { kind: t.kind, word: t.word };
  const jobs = (m.running_jobs ?? []).map((j) => `${j.repository} ${j.job_name}`);

  const [mem, setMem] = useState(m.settings.budget_memory_pct);
  const [cpu, setCpu] = useState(m.settings.budget_cpu_pct);
  const [slots, setSlots] = useState(m.settings.job_slots);
  const [drain, setDrain] = useState(m.settings.drain_timeout_minutes);
  const [allowed, setAllowed] = useState<string[]>(m.settings.allowed_backends);
  // The floor in force: the console value, else what the agent started with (runners R5.83, OPS86).
  const floorOff = m.policy?.disk_floor_off === true;
  const floorSaved = m.settings.disk_floor_gib || m.policy?.disk_floor_gib || 10;
  const [floor, setFloor] = useState(floorSaved);
  const [errors, setErrors] = useState<string[]>([]);
  const [saved, setSaved] = useState(false);

  const poolSize = usePoolSize(m, onChanged);
  const settingsChanges =
    (mem !== m.settings.budget_memory_pct ? 1 : 0) +
    (cpu !== m.settings.budget_cpu_pct ? 1 : 0) +
    (slots !== m.settings.job_slots ? 1 : 0) +
    (drain !== m.settings.drain_timeout_minutes ? 1 : 0) +
    (!floorOff && floor !== floorSaved ? 1 : 0) +
    (allowed.slice().sort().join() !== m.settings.allowed_backends.slice().sort().join()
      ? 1
      : 0);
  const changeCount = settingsChanges + poolSize.changes;
  const body = useMemo(
    () => ({
      budget_memory_pct: mem,
      budget_cpu_pct: cpu,
      job_slots: slots,
      drain_timeout_minutes: drain,
      allowed_backends: allowed as Schemas['BackendName'][],
      // Sent only when changed: saving other fields must not pin the value of the start.
      ...(!floorOff && floor !== floorSaved ? { disk_floor_gib: floor } : {}),
    }),
    [mem, cpu, slots, drain, allowed, floor, floorOff, floorSaved],
  );

  const saveAsk = useQuestion(
    () =>
      unwrap(
        client.PATCH('/api/v1/machines/{machineId}', {
          params: { path: { machineId: m.id } },
          body: { ...body, confirm: true },
        }),
      ),
    () => {
      setSaved(true);
      onChanged();
    },
  );
  const save = useCommand(async () => {
    const problems: string[] = [];
    if (mem < 1 || mem > 100) problems.push('Memory budget must be from 1% to 100%.');
    if (cpu < 1 || cpu > 100) problems.push('CPU budget must be from 1% to 100%.');
    if (slots < 1) problems.push('Job slots must be at least 1.');
    if (!floorOff && (floor < 1 || floor > 2000))
      problems.push('Free disk to keep must be from 1 GB to 2000 GB.');
    setErrors(problems);
    if (problems.length) return;
    if (!(await poolSize.save())) return;
    if (settingsChanges === 0) {
      setSaved(true);
      onChanged();
      return;
    }
    try {
      await unwrap(
        client.PATCH('/api/v1/machines/{machineId}', {
          params: { path: { machineId: m.id } },
          body,
        }),
      );
      setSaved(true);
      onChanged();
    } catch (e) {
      if (
        e instanceof Error &&
        'code' in e &&
        (e as { code: string }).code === 'needs_confirmation'
      ) {
        saveAsk.ask(
          [`Settings of ${m.name} change and its runners restart.`],
          ((e as { details?: { text?: string }[] }).details ?? []).map(
            (d) => d.text ?? '',
          ),
        );
        return;
      }
      throw e;
    }
  });

  const drainQ = useQuestion(
    () =>
      unwrap(
        client.POST('/api/v1/machines/{machineId}/drain', {
          params: { path: { machineId: m.id } },
          body: {},
        }),
      ),
    onChanged,
  );
  const resume = useCommand(async () => {
    await unwrap(
      client.POST('/api/v1/machines/{machineId}/resume', {
        params: { path: { machineId: m.id }, query: { confirm: true } },
      }),
    );
    onChanged();
  });
  const untickedUsable = m.backends.filter((b) => b.usable && !allowed.includes(b.name));
  const f = m.facts;
  return (
    <>
      <PageHeader
        title={m.name}
        lead={`${m.os} ${m.arch}`}
        hint={
          stopped
            ? 'New jobs are stopped. Resume takes jobs again.'
            : 'Running jobs finish first. Resume takes jobs again.'
        }
        action={
          stopped ? (
            <Button
              onPress={() => void resume.run()}
              busy={resume.busy}
              busyLabel="Resuming"
              disabled={!admin}
              reason={why}
            >
              Resume
            </Button>
          ) : (
            <Button
              onPress={() =>
                drainQ.ask(
                  [
                    'Running jobs finish first, for up to ' +
                      `${m.settings.drain_timeout_minutes} min.`,
                    'Nothing is removed. Resume undoes it.',
                  ],
                  jobs,
                )
              }
              disabled={!admin}
              reason={why}
            >
              Stop new jobs
            </Button>
          )
        }
      />
      {resume.error ? (
        <Message tone="problem">Could not resume. {resume.error}</Message>
      ) : null}
      {m.pending_change ? <DrainHolder change={m.pending_change} /> : null}
      <Section title="Facts">
        <Facts
          items={[
            { name: 'State', value: <Status kind={state.kind} word={state.word} /> },
            { name: 'Last seen', value: formatPast(m.last_seen_at) },
            {
              name: 'Slots in use',
              value: `${m.slots_used} of ${m.settings.job_slots}`,
              help: <HelpTerm term="slot" />,
            },
            {
              name: 'Load budget',
              value: `memory ${formatPercent(m.settings.budget_memory_pct)}, CPU ${formatPercent(m.settings.budget_cpu_pct)}`,
              help: <HelpTerm term="budget" />,
            },
            ...(f?.cpu_model
              ? [{ name: 'CPU', value: `${f.cpu_model}, ${f.cores ?? '?'} cores` }]
              : []),
            ...(f?.memory_bytes
              ? [{ name: 'Memory', value: formatSize(f.memory_bytes) }]
              : []),
            ...(f?.agent_version ? [{ name: 'Agent', value: f.agent_version }] : []),
          ]}
        />
      </Section>
      <RecoverySection m={m} onChanged={onChanged} />
      <MachinePolicySection m={m} onChanged={onChanged} />
      <MachineDockerDiskSection m={m} onChanged={onChanged} />
      <form
        onSubmit={(e) => {
          e.preventDefault();
          void save.run();
        }}
      >
        <Stack>
          <ErrorSummary errors={errors} />
          <Section title="Backends">
            <Stack gap="tight">
              {untickedUsable.length > 0 ? (
                <Message
                  tone="needs-you"
                  action={
                    <Button
                      onPress={() =>
                        setAllowed((a) => [...a, ...untickedUsable.map((b) => b.name)])
                      }
                    >
                      {`Tick ${untickedUsable.map((b) => b.name).join(', ')}`}
                    </Button>
                  }
                >
                  {`${untickedUsable.map((b) => b.name).join(', ')} ${untickedUsable.length === 1 ? 'is' : 'are'} installed and works on this machine, but is not turned on. Tick it and press Save so this machine can run its runners.`}
                </Message>
              ) : null}
              {m.backends.map((b) => (
                <CheckRow
                  key={b.name}
                  label={b.name}
                  checked={allowed.includes(b.name)}
                  changed={
                    allowed.includes(b.name) !==
                    m.settings.allowed_backends.includes(b.name)
                  }
                  onChange={(c) =>
                    setAllowed((a) =>
                      c ? [...a, b.name] : a.filter((x) => x !== b.name),
                    )
                  }
                  // The reason is written once (UX-1): the lock text, or for a viewer the admin-only one.
                  help={b.usable ? (b.version ?? '') : ''}
                  {...(!admin || !b.usable
                    ? { disabledReason: !admin ? why : (b.reason ?? 'Not usable here.') }
                    : {})}
                />
              ))}
            </Stack>
          </Section>
          <Section title="Budget and slots">
            <FieldGrid>
              <NumberField
                label="Memory budget"
                value={mem}
                unit="%"
                min={1}
                max={100}
                changed={mem !== m.settings.budget_memory_pct}
                onChange={setMem}
              />
              <NumberField
                label="CPU budget"
                value={cpu}
                unit="%"
                min={1}
                max={100}
                changed={cpu !== m.settings.budget_cpu_pct}
                onChange={setCpu}
              />
              <NumberField
                label="Job slots"
                value={slots}
                min={1}
                max={64}
                changed={slots !== m.settings.job_slots}
                onChange={setSlots}
              />
              <NumberField
                label="Time to finish running jobs"
                help="How long running jobs may take to finish when new jobs are stopped. crsp never cuts a job."
                value={drain}
                unit=" min"
                min={1}
                max={1440}
                changed={drain !== m.settings.drain_timeout_minutes}
                onChange={setDrain}
              />
            </FieldGrid>
          </Section>
          <Section title="Disk">
            <Stack>
              {floorOff ? (
                <Facts
                  items={[
                    {
                      name: 'Free disk to keep',
                      value: 'Off',
                    },
                  ]}
                />
              ) : (
                <NumberField
                  label="Free disk to keep"
                  help="Under this much free space new jobs wait (disk_low). Running jobs finish."
                  value={floor}
                  unit=" GB"
                  min={1}
                  max={2000}
                  changed={floor !== floorSaved}
                  onChange={setFloor}
                />
              )}
              {floorOff ? (
                <Text>
                  The floor is off in config.toml on this machine. It cannot be changed
                  here.
                </Text>
              ) : null}
              {f?.disk_free_bytes !== undefined ? (
                <Text>{`${Math.round((f.disk_free_bytes / 2 ** 30) * 10) / 10} GB free now.`}</Text>
              ) : null}
            </Stack>
          </Section>
          {poolSize.section}
        </Stack>
      </form>
      <Suspense fallback={null}>
        <MachineRemove
          machineId={m.id}
          disabled={!admin}
          onRemoved={() => void navigate('/setup/machines')}
        />
      </Suspense>
      {saved ? <Message tone="done">Settings saved.</Message> : null}
      {save.error ? (
        <Message tone="problem">Could not save. {save.error} Try again.</Message>
      ) : null}
      <EditBar
        changes={changeCount}
        onSave={() => void save.run()}
        onCancel={() => {
          setMem(m.settings.budget_memory_pct);
          setCpu(m.settings.budget_cpu_pct);
          setSlots(m.settings.job_slots);
          setDrain(m.settings.drain_timeout_minutes);
          setFloor(floorSaved);
          setAllowed(m.settings.allowed_backends);
          setErrors([]);
          poolSize.reset();
        }}
        submit={false}
        busy={save.busy}
        blocked={!admin}
        why={why}
        holdReason={poolSize.hold}
      />
      <Ask
        title={`Stop new jobs on ${m.name}?`}
        action="Stop new jobs"
        busyLabel="Stopping new jobs"
        q={drainQ.question}
        busy={drainQ.busy}
        error={drainQ.error}
        onCancel={drainQ.cancel}
        onConfirm={() => void drainQ.confirm()}
      />
      <Ask
        title="Save these settings?"
        action="Save"
        busyLabel="Saving"
        q={saveAsk.question}
        busy={saveAsk.busy}
        error={saveAsk.error}
        onCancel={saveAsk.cancel}
        onConfirm={() => void saveAsk.confirm()}
      />
    </>
  );
}

function minutes(seconds: number): string {
  return `${Math.max(1, Math.round(seconds / 60))} min`;
}

/** The job that holds a drain, how long it runs and its usual time (machines R5.55, R5.56). */
function DrainHolder({ change }: { change: Schemas['MachineChange'] }) {
  const h = change.holder;
  const waiting = change.waiting_for ?? [];
  if (waiting.length === 0) return null;
  const j = h?.job ?? waiting[0]!;
  const runner = j.runner_name ? ` on ${j.runner_name}` : '';
  return (
    <Section title="Waiting for jobs to finish">
      <Stack gap="tight">
        <Facts
          items={[
            { name: 'Holding it', value: `${j.repository} ${j.job_name}${runner}` },
            ...(h ? [{ name: 'Running for', value: minutes(h.running_seconds) }] : []),
            ...(h?.usual_seconds
              ? [{ name: 'Usually', value: minutes(h.usual_seconds) }]
              : []),
            { name: 'Jobs it waits for', value: String(waiting.length) },
          ]}
        />
        {h?.over_usual ? (
          <Message tone="problem">
            This job runs far past its usual time. crsp never stops a job. Wait longer, or
            cancel the change with Resume.
          </Message>
        ) : null}
        {change.choices?.length && !h?.over_usual ? (
          <Message tone="note">
            The time to finish running jobs passed. crsp never stops a job. Wait longer,
            or cancel the change with Resume.
          </Message>
        ) : null}
      </Stack>
    </Section>
  );
}

const OS_WORD: Record<string, string> = {
  linux: 'Linux',
  windows: 'Windows',
  macos: 'macOS',
};

/** The System column: the system and arch, or, when the engine runs another
 * system's containers (Docker Desktop on WSL2), "Windows, runs Linux containers". */
export function systemText(m: Schemas['Machine']): string {
  if (m.container_os && m.container_os !== m.os) {
    return `${OS_WORD[m.os] ?? m.os}, runs ${OS_WORD[m.container_os] ?? m.container_os} containers`;
  }
  return `${m.os} ${m.arch}`;
}
