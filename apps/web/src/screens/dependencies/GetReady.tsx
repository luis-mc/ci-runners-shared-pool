// "Get this machine ready" (dependency-setup spec 4, DEP1 to DEP13): one wizard that finds what a runner choice
// needs on a machine, offers the ways to get it, asks, installs, verifies and says what changed. The shared wizard
// owns the steps, the locks, Stop and the busy primary; this page passes data only. The words are in content.md
// ("Dependency setup words"). Doors: Add runners, Add machine, Setup and the doctor open it (DEP13, DEP14).
import {
  Breadcrumbs,
  Button,
  Card,
  CardGrid,
  CheckRow,
  ChoiceField,
  CodeBlock,
  Facts,
  Loading,
  Message,
  Page,
  PageHeader,
  Section,
  Stack,
  Text,
  Wizard,
  type WizardPhase,
} from '@crsp/ui';
import { useEffect, useRef, useState, type ComponentProps } from 'react';
import { useNavigate, useParams, useSearchParams } from 'react-router-dom';
import { ApiError, errorText, unwrap, useClient } from '../../api/api';
import { useQuestion } from '../../api/hooks';
import { useMounted } from '../../api/mounted';
import { useSession } from '../../auth/session';
import { TIMING } from '../../timing';
import { Ask } from '../common';
import {
  STEP,
  STEPS,
  doneRows,
  factRows,
  failedWords,
  headline,
  installLabel,
  installNote,
  itemFacts,
  itemWords,
  jobActive,
  overallValue,
  progressLine,
  recommended,
  reviewLabel,
  reviewSummary,
  toProgressEvent,
  undoItems,
  type DepCheck,
  type DepJob,
  type RunnerKind,
  type RunnerOS,
} from './plan';

export interface GetReadyProps {
  machineId: string;
  /** The machine's name, for the words ("crsp cannot reach {machine}."). */
  machineName?: string | undefined;
  runnerOs: RunnerOS;
  runnerKind: RunnerKind;
  /** A stored job (the address keeps it): the wizard reads it and shows where it ended (done after reload). */
  jobId?: string | undefined;
  /** The original task goes on: nothing was missing, or the work is done (CORE-4, DEP13). */
  onDone: () => void;
  /** The words of the Done primary: "Continue", "Back to Add runners". */
  doneLabel?: string | undefined;
  onUseAnother?: (() => void) | undefined;
  /** The user leaves with the plan kept ("Later"). */
  onLeave?: (() => void) | undefined;
  /** The job that now belongs to this run, for the host to keep in its address. */
  onJob?: ((id: string | undefined) => void) | undefined;
}

type Doing = 'install' | 'start' | 'undo';

export function GetReadyWizard(p: GetReadyProps) {
  const client = useClient();
  const session = useSession();
  const { mounted, sleep } = useMounted();
  const polling = useRef(false);
  const lastStep = useRef<number>(STEP.install);
  const onDoneRef = useRef(p.onDone);
  onDoneRef.current = p.onDone;
  const admin = session.can('admin');
  const why = session.whyNot('admin');

  const [phase, setPhase] = useState<WizardPhase>('loading');
  const [step, setStep] = useState<number>(STEP.check);
  const [check, setCheck] = useState<DepCheck | undefined>(undefined);
  const [job, setJob] = useState<DepJob | undefined>(undefined);
  const [pick, setPick] = useState<string | undefined>(undefined);
  const [licence, setLicence] = useState(false);
  const [manual, setManual] = useState(false);
  const [offline, setOffline] = useState(false);
  const [problem, setProblem] = useState<string | undefined>(undefined);
  const [failedAt, setFailedAt] = useState<number | undefined>(undefined);
  const [stopping, setStopping] = useState(false);
  const [stopError, setStopError] = useState<string | undefined>(undefined);
  const [lost, setLost] = useState(false);
  const [doing, setDoing] = useState<Doing>('install');
  // The stored job is read once; the address changing to the job this page starts is not a reload.
  const stored = useRef(p.jobId).current;

  const path = { machineId: p.machineId };
  const readJob = (id: string) =>
    unwrap(
      client.GET('/api/v1/machines/{machineId}/dependencies/jobs/{jobId}', {
        params: { path: { ...path, jobId: id } },
      }),
    );

  /** Put a job on the screen. It never starts a read; `follow` does. */
  const show = (j: DepJob, afterReload: boolean) => {
    setJob(j);
    setLost(false);
    switch (j.state) {
      case 'consented':
      case 'installing':
      case 'undoing':
        lastStep.current = STEP.install;
        setStep(STEP.install);
        setPhase('running');
        break;
      case 'verifying':
      case 'preparing':
        lastStep.current = STEP.verify;
        setStep(STEP.verify);
        setPhase('running');
        break;
      case 'paused_restart':
        setStep(STEP.install);
        setPhase('paused');
        break;
      case 'done':
        setStep(STEP.done);
        setPhase(afterReload ? 'done-after-reload' : 'done');
        break;
      case 'failed':
        setProblem(undefined);
        setFailedAt(lastStep.current);
        setStep(lastStep.current);
        setPhase('failed');
        break;
      case 'stopped':
      case 'undone':
        setStep(STEP.done);
        setPhase('stopped');
        break;
    }
    if (j.state !== 'undoing') setDoing((d) => (d === 'undo' ? 'install' : d));
    if (j.state !== 'failed') setFailedAt(undefined);
    if (j.state !== 'installing' && j.state !== 'consented') setStopping(false);
  };

  /** Read the job once a second while it is not over; a paused job is read too, so the page resumes on its own. */
  const follow = async (id: string) => {
    if (polling.current) return;
    polling.current = true;
    try {
      while (mounted.current) {
        if (!(await sleep(TIMING.dependencyPollMs))) break;
        try {
          const j = await readJob(id);
          if (!mounted.current) break;
          show(j, false);
          if (!jobActive(j)) break;
        } catch {
          setLost(true);
        }
      }
    } finally {
      polling.current = false;
    }
  };

  const runCheck = async (first: boolean) => {
    setPhase(first ? 'loading' : 'planning');
    setStep(STEP.check);
    setProblem(undefined);
    setFailedAt(undefined);
    setManual(false);
    try {
      const c = await unwrap(
        client.POST('/api/v1/machines/{machineId}/dependencies/check', {
          params: { path },
          body: { runner_os: p.runnerOs, runner_kind: p.runnerKind },
        }),
      );
      if (!mounted.current) return;
      if (c.ready) {
        // Nothing is missing: no wizard, the original task goes on (CORE-4).
        setCheck(c);
        onDoneRef.current();
        return;
      }
      setOffline(false);
      setCheck(c);
      setPick(recommended(c)?.id);
      setLicence(false);
      setStep(STEP.choose);
      setPhase('first-run');
    } catch (e) {
      if (!mounted.current) return;
      if (e instanceof ApiError && e.status === 409) {
        // An offline machine is not offered an install (DEP12).
        setOffline(true);
        setStep(STEP.choose);
        setPhase('first-run');
        return;
      }
      setProblem(errorText(e));
      setFailedAt(STEP.check);
      setStep(STEP.check);
      setPhase('failed');
    }
  };

  useEffect(() => {
    void (async () => {
      try {
        if (stored) {
          const j = await readJob(stored);
          if (!mounted.current) return;
          show(j, true);
          if (jobActive(j)) void follow(j.id);
          return;
        }
        // A job still going on this machine is resumed (DEP11); a finished one is not shown again.
        const list = await unwrap(
          client.GET('/api/v1/machines/{machineId}/dependencies/jobs', {
            params: { path },
          }),
        );
        if (!mounted.current) return;
        const newest = [...list].sort((a, b) =>
          b.updated_at.localeCompare(a.updated_at),
        )[0];
        if (newest && jobActive(newest)) {
          show(newest, true);
          p.onJob?.(newest.id);
          void follow(newest.id);
          return;
        }
      } catch (e) {
        if (!mounted.current) return;
        if (stored) {
          setProblem(errorText(e));
          setFailedAt(STEP.check);
          setPhase('failed');
          return;
        }
      }
      await runCheck(true);
    })();
    // The stored run is read once, when the page opens.
  }, [client, p.machineId]);

  const choice =
    check?.choices.find((c) => c.id === pick) ?? (check ? recommended(check) : undefined);
  const items = check?.items ?? [];

  const begin = async (kind: Doing, accept: boolean) => {
    if (!choice) return;
    setDoing(kind);
    setStep(STEP.install);
    setPhase('running');
    setJob(undefined);
    lastStep.current = STEP.install;
    try {
      const j = await unwrap(
        client.POST('/api/v1/machines/{machineId}/dependencies/jobs', {
          params: { path },
          body: {
            runner_os: p.runnerOs,
            runner_kind: p.runnerKind,
            choice_id: choice.id,
            confirmed: true,
            ...(accept ? { accept_licence: true } : {}),
          },
        }),
      );
      p.onJob?.(j.id);
      show(j, false);
      void follow(j.id);
    } catch (e) {
      setProblem(errorText(e));
      setFailedAt(STEP.install);
      setStep(STEP.install);
      setPhase('failed');
    }
  };

  const stop = async () => {
    if (!job) return;
    setStopping(true);
    setStopError(undefined);
    try {
      await unwrap(
        client.POST('/api/v1/machines/{machineId}/dependencies/jobs/{jobId}/stop', {
          params: { path: { ...path, jobId: job.id } },
        }),
      );
    } catch (e) {
      // 409: the job has already ended; the next read shows how.
      if (!(e instanceof ApiError && e.status === 409)) {
        setStopError(errorText(e));
        setStopping(false);
      }
    }
  };

  // Asks first, then sends (UX-9, UX-62): the restart of the PC, and the undo that names each item.
  const restart = useQuestion(
    async () => {
      if (!job) return;
      await unwrap(
        client.POST(
          '/api/v1/machines/{machineId}/dependencies/jobs/{jobId}/restart-host',
          {
            params: { path: { ...path, jobId: job.id }, query: { confirmed: true } },
          },
        ),
      );
    },
    () => undefined,
  );
  const undo = useQuestion(
    async () => {
      if (!job) return;
      const j = await unwrap(
        client.POST('/api/v1/machines/{machineId}/dependencies/jobs/{jobId}/undo', {
          params: { path: { ...path, jobId: job.id }, query: { confirmed: true } },
        }),
      );
      setDoing('undo');
      show(j, false);
      void follow(j.id);
    },
    () => undefined,
  );

  const again = () => {
    p.onJob?.(undefined);
    setJob(undefined);
    void runCheck(false);
  };

  // ------------------------------------------------------------------------------------------------- the words
  const machine = p.machineName ?? 'this machine';
  const names = choice && check ? itemWords(choice, items) : '';
  const needsTick = Boolean(choice?.requires_licence_tick);
  const blocked = check?.blocked_reason;
  const event = job?.last ? toProgressEvent(job.last) : undefined;
  const line =
    doing === 'undo'
      ? 'Removing what crsp added'
      : job
        ? progressLine(event)
        : doing === 'start'
          ? 'Starting Docker'
          : 'Starting';
  const value = job ? overallValue(job.log) : undefined;
  const added = job?.added ?? [];

  const manualBlock = (steps: readonly string[]) => (
    <Stack>
      <Text>Run these steps on {machine}, then press Try again.</Text>
      <CodeBlock text={steps.join('\n')} label="Steps to run yourself" />
    </Stack>
  );

  const undoButton = (
    <Button
      onPress={() => undo.ask(job ? undoItems(job, items) : [])}
      busy={undo.busy}
      busyLabel="Undoing"
      disabled={!admin}
      reason={why}
    >
      Undo what was installed
    </Button>
  );

  let body;
  let primary: ComponentProps<typeof Wizard>['primary'];
  let endPrimary: ComponentProps<typeof Wizard>['endPrimary'];
  let title = 'Looking';
  if (phase === 'loading' || phase === 'planning') {
    title = 'Checking';
    body =
      phase === 'planning' ? (
        <Message tone="note" dismissible={false}>
          {`Checking ${machine} again.`}
        </Message>
      ) : null;
    primary = {
      label: 'Try again',
      onPress: () => undefined,
      disabled: true,
      reason: 'Checking',
    };
  } else if (phase === 'first-run' && offline) {
    title = 'This machine';
    body = (
      <Stack>
        <Message tone="needs-you">{`crsp cannot reach ${machine}.`}</Message>
        <Text>Nothing can be installed on a machine that is not connected.</Text>
        <Button onPress={p.onUseAnother}>Use another machine</Button>
      </Stack>
    );
    primary = { label: 'Try again', onPress: () => void runCheck(false) };
  } else if (phase === 'first-run' && check) {
    title = headline(check);
    const startOnly = check.start_only === true;
    body = (
      <Stack>
        <Facts items={itemFacts(items)} />
        {blocked ? <Message tone="needs-you">{blocked}</Message> : null}
        {choice && !startOnly && installNote(choice) ? (
          <Text>{installNote(choice)}</Text>
        ) : null}
        {check.choices.length > 1 && !startOnly ? (
          <ChoiceField
            label="Way to get it"
            value={choice?.id ?? ''}
            onChange={setPick}
            choices={check.choices.map((c) => ({
              value: c.id,
              label: c.recommended ? `${c.title} (recommended)` : c.title,
            }))}
          />
        ) : null}
        {startOnly ? null : (
          <Stack gap="tight">
            <Button onPress={() => setManual((m) => !m)} expanded={manual}>
              Do it myself
            </Button>
            {manual && choice ? (
              <Stack>
                {manualBlock(choice.manual_steps)}
                <Button onPress={() => void runCheck(false)}>Try again</Button>
              </Stack>
            ) : null}
            <Button onPress={p.onUseAnother}>Use another machine</Button>
          </Stack>
        )}
      </Stack>
    );
    primary = startOnly
      ? {
          label: 'Start Docker',
          busyLabel: 'Starting Docker',
          onPress: () => void begin('start', false),
          disabled: !admin || !choice,
          reason: !admin ? why : 'No way to start it was offered.',
        }
      : {
          label: choice ? installLabel(choice, items) : 'Install',
          onPress: () => {
            setStep(STEP.review);
            setPhase('ready-to-review');
          },
          disabled: !choice || Boolean(blocked) || !admin,
          reason: !choice
            ? 'No way to install it was offered.'
            : (blocked ?? (!admin ? why : undefined)),
        };
  } else if (phase === 'ready-to-review' && choice && check) {
    title = 'Review';
    const lacksTick = needsTick && !licence;
    body = (
      <Stack>
        <Text>{`crsp will install ${names} on ${machine}.`}</Text>
        <CardGrid>
          {choice.facts.map((f) => (
            <Card key={f.name} title={f.name}>
              <Facts items={factRows(f)} />
            </Card>
          ))}
        </CardGrid>
        <Text>{reviewSummary(choice, check.disk_free_bytes)}</Text>
        {needsTick ? (
          <CheckRow
            label="I have the right to use Docker Desktop"
            checked={licence}
            onChange={setLicence}
            help="crsp does not judge licences. Docker Desktop is paid for larger companies."
          />
        ) : null}
        {blocked ? <Message tone="needs-you">{blocked}</Message> : null}
      </Stack>
    );
    primary = {
      label: reviewLabel(choice),
      busyLabel: 'Installing',
      onPress: () => void begin('install', needsTick && licence),
      disabled: !admin || lacksTick || Boolean(blocked),
      reason: !admin
        ? why
        : blocked
          ? blocked
          : lacksTick
            ? 'Tick the licence box first.'
            : undefined,
    };
  } else if (phase === 'running') {
    title =
      step === STEP.verify ? 'Verifying' : doing === 'undo' ? 'Undoing' : 'Installing';
    body = (
      <Stack>
        {lost ? (
          <Message tone="note">
            The console is not answering. This page waits for it.
          </Message>
        ) : null}
        {stopError ? (
          <Message tone="problem">{`Could not stop. ${stopError}`}</Message>
        ) : null}
        <Text>{`On ${machine}. You can leave this page; the work goes on.`}</Text>
      </Stack>
    );
    primary = {
      label:
        doing === 'undo'
          ? 'Undo what was installed'
          : doing === 'start'
            ? 'Start Docker'
            : choice
              ? reviewLabel(choice)
              : 'Install',
      busyLabel:
        doing === 'undo'
          ? 'Undoing'
          : doing === 'start'
            ? 'Starting Docker'
            : 'Installing',
      onPress: () => undefined,
    };
  } else if (phase === 'paused') {
    title = 'Restart needed';
    body = (
      <Stack>
        {lost ? (
          <Message tone="note">
            The console is not answering. This page waits for it.
          </Message>
        ) : null}
        <Message tone="needs-you">
          {`Restart needed to finish. crsp keeps what it did and goes on with Verify when ${machine} is back.`}
        </Message>
        <Ask
          title={`Restart ${machine} now?`}
          intro="The machine restarts at once. crsp goes on when it is connected again."
          action="Restart this PC now"
          busyLabel="Restarting"
          q={restart.question}
          busy={restart.busy}
          error={restart.error}
          onCancel={restart.cancel}
          onConfirm={() => void restart.confirm()}
        />
      </Stack>
    );
    primary = { label: 'Restart this PC now', onPress: () => undefined };
    endPrimary = {
      label: 'Restart this PC now',
      onPress: () =>
        restart.ask([
          `${machine} restarts`,
          'No running job is cut: the machine holds jobs first',
        ]),
    };
  } else if (phase === 'stopped') {
    const undone = job?.state === 'undone';
    title = undone ? 'Undone' : 'Stopped';
    body = (
      <Stack>
        <Message tone="note" dismissible={false}>
          {undone
            ? 'Undone. What this run added was removed. Software that was there before was not touched.'
            : `Stopped. ${added.length === 0 ? 'Nothing was installed yet.' : `${added.length} ${added.length === 1 ? 'item was' : 'items were'} installed.`} The rest was not started.`}
        </Message>
        <Facts
          items={doneRows(job ?? emptyJob, items).filter(
            (r) => !undone || r.name !== 'Engine version',
          )}
        />
        {!undone && added.length > 0 ? undoButton : null}
        <Ask
          title={`Undo what was installed on ${machine}?`}
          intro="Only what crsp added in this run goes. Software that was there before stays."
          action="Undo"
          busyLabel="Undoing"
          q={undo.question}
          busy={undo.busy}
          error={undo.error}
          onCancel={undo.cancel}
          onConfirm={() => void undo.confirm()}
        />
      </Stack>
    );
    primary = { label: 'Continue', onPress: again };
    endPrimary = { label: 'Continue', onPress: again };
  } else if (phase === 'failed') {
    title = 'It did not finish';
    body = (
      <Stack>
        <Message tone="problem">
          {job
            ? failedWords(job, items)
            : `${problem ?? 'The console did not answer.'} Try again.`}
        </Message>
        {job?.manual_steps && job.manual_steps.length > 0 ? (
          <Button onPress={() => setManual((m) => !m)} expanded={manual}>
            Show the steps for myself
          </Button>
        ) : null}
        {manual && job?.manual_steps ? manualBlock(job.manual_steps) : null}
        {job && added.length > 0 ? undoButton : null}
        <Ask
          title={`Undo what was installed on ${machine}?`}
          intro="Only what crsp added in this run goes. Software that was there before stays."
          action="Undo"
          busyLabel="Undoing"
          q={undo.question}
          busy={undo.busy}
          error={undo.error}
          onCancel={undo.cancel}
          onConfirm={() => void undo.confirm()}
        />
      </Stack>
    );
    primary = { label: 'Try again', onPress: again };
  } else if (phase === 'done' || phase === 'done-after-reload') {
    title = 'Result';
    body = (
      <Stack>
        <Message tone="done">{`${machine} is ready. Docker answers and a test container ran.`}</Message>
        <Facts items={job ? doneRows(job, items) : []} />
        {added.length > 0 ? (
          <Text>
            To undo, remove only what is listed above. crsp can do it for you: Undo what
            was installed.
          </Text>
        ) : null}
        {added.length > 0 ? undoButton : null}
        <Ask
          title={`Undo what was installed on ${machine}?`}
          intro="Only what crsp added in this run goes. Software that was there before stays."
          action="Undo"
          busyLabel="Undoing"
          q={undo.question}
          busy={undo.busy}
          error={undo.error}
          onCancel={undo.cancel}
          onConfirm={() => void undo.confirm()}
        />
      </Stack>
    );
    primary = { label: p.doneLabel ?? 'Continue', onPress: p.onDone };
  } else {
    body = <Loading what="Reading the machine" />;
    primary = {
      label: 'Try again',
      onPress: () => undefined,
      disabled: true,
      reason: 'Checking',
    };
  }

  return (
    <Wizard
      name="Get this machine ready"
      steps={STEPS}
      current={step}
      firstStep={STEP.choose}
      phase={phase}
      reading="Reading the machine"
      live={job ? job.log.map(toProgressEvent) : undefined}
      progress={
        phase === 'running'
          ? { text: line, ...(value !== undefined ? { value, max: 100 } : {}) }
          : undefined
      }
      canStop={
        phase === 'running' &&
        doing === 'install' &&
        (job?.state === 'installing' || job?.state === 'consented')
      }
      stopping={stopping}
      onStop={() => void stop()}
      backLabel={
        phase === 'ready-to-review' ? 'Cancel' : phase === 'paused' ? 'Later' : undefined
      }
      onBack={() => {
        if (phase === 'ready-to-review') {
          setStep(STEP.choose);
          setPhase('first-run');
        } else p.onLeave?.();
      }}
      onStep={(i) => {
        if (i === STEP.check) again();
        else if (i === STEP.choose && check) {
          setStep(STEP.choose);
          setPhase('first-run');
        }
      }}
      primary={primary}
      endPrimary={endPrimary}
      review={phase === 'ready-to-review'}
      failedAt={failedAt}
      onTryAgain={again}
      donePrimary={{ label: p.doneLabel ?? 'Continue', onPress: p.onDone }}
    >
      <Section title={title}>{body}</Section>
    </Wizard>
  );
}

const emptyJob: DepJob = {
  id: '',
  machine_id: '',
  state: 'stopped',
  choice_id: '',
  item_ids: [],
  runner_os: '',
  runner_kind: '',
  created_at: '',
  updated_at: '',
  added: [],
  log: [],
};

/** The page: /setup/machines/:id/ready?os=linux&kind=persistent&job=...&return=/setup/runners/add */
export function GetReadyPage() {
  const { id = '' } = useParams();
  const [params, setParams] = useSearchParams();
  const navigate = useNavigate();
  const client = useClient();
  const [name, setName] = useState<string | undefined>(undefined);
  useEffect(() => {
    let on = true;
    unwrap(
      client.GET('/api/v1/machines/{machineId}', { params: { path: { machineId: id } } }),
    )
      .then((m) => on && setName(m.name))
      .catch(() => undefined);
    return () => {
      on = false;
    };
  }, [client, id]);
  const os =
    (['linux', 'windows', 'macos'] as const).find((x) => x === params.get('os')) ??
    'linux';
  const kind = params.get('kind') === 'ephemeral' ? 'ephemeral' : 'persistent';
  // Only an address inside the console is a way back (never another site).
  const back = params.get('return');
  const target =
    back && back.startsWith('/') && !back.startsWith('//') ? back : undefined;
  const leave = () => void navigate(target ?? `/setup/machines/${id}`);
  return (
    <Page>
      <Breadcrumbs
        trail={[
          { to: '/setup/machines', label: 'Machines' },
          { to: `/setup/machines/${id}`, label: name ?? 'Machine' },
          { label: 'Get this machine ready' },
        ]}
      />
      <PageHeader title="Get this machine ready" />
      <GetReadyWizard
        machineId={id}
        machineName={name}
        runnerOs={os}
        runnerKind={kind}
        jobId={params.get('job') ?? undefined}
        onDone={leave}
        doneLabel={target ? 'Continue where you were' : 'Open the machine'}
        onUseAnother={() => void navigate('/setup/machines')}
        onLeave={leave}
        onJob={(j) => {
          const next = new URLSearchParams(params);
          if (j) next.set('job', j);
          else next.delete('job');
          setParams(next, { replace: true });
        }}
      />
    </Page>
  );
}
