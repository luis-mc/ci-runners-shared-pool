// The "Add runners" wizard (web-console 4.5, R5.23, R5.19): Runners, Review, Registering,
// Done. Only machines whose system matches the chosen one can be picked; the others are greyed with the reason
// (UX-38). The stored plan id is in the address (?plan=).
import {
  BulletList,
  Breadcrumbs,
  CheckRow,
  ChoiceField,
  Facts,
  LinkButton,
  Message,
  NumberField,
  Page,
  PageHeader,
  Section,
  Stack,
  Text,
  TextField,
  Wizard,
  type WizardPhase,
} from '@crsp/ui';
import { useEffect, useRef, useState } from 'react';
import { useNavigate, useSearchParams } from 'react-router-dom';
import { TIMING } from '../../timing';
import { ApiError, errorText, unwrap, useClient, type Schemas } from '../../api/api';
import { useMounted } from '../../api/mounted';
import {
  allRepositories,
  needsYes,
  planProgress,
  type Plan,
  type Repository,
} from '../repositories/plans';
import { PullingStep, ReadyMadeLine } from '../registry/PullingStep';
import { chooseKind, KIND_HELP, KIND_NAMES, type Kind } from './kinds';

const STEPS = ['Runners', 'Review', 'Registering', 'Done'] as const;
type OS = Schemas['OS'];

/** A machine takes runners of its own system, and Linux runners too when its
 * container engine runs Linux containers (Docker Desktop on WSL2; runners R5.47). */
export function machineSupports(m: Schemas['Machine'], os: OS): boolean {
  return (
    m.os === os || (os === 'linux' && m.os === 'windows' && m.container_os === 'linux')
  );
}

/** A Linux runner on a Windows machine runs in the container backend, which a machine allows by itself (R5.27). */
export function containerBackendAllowed(m: Schemas['Machine']): boolean {
  return m.backends.some((b) => b.name === 'docker' && b.usable && b.allowed);
}

/** A reason ends with a full stop, so words can follow it. */
export function sentence(text: string): string {
  const t = text.trim();
  return /[.!?]$/.test(t) ? t : `${t}.`;
}

const NOT_ALLOWED =
  'The container backend is not allowed on this machine. Linux runners here need it.';
const NO_STEPS = 'Nothing would be added with these choices. Change them and try again.';

function crossesTo(m: Schemas['Machine'], os: OS): boolean {
  return m.os !== os && machineSupports(m, os);
}

const RUNS_ON: Record<OS, string> = {
  linux: 'RUNS_ON_LINUX',
  windows: 'RUNS_ON_WINDOWS',
  macos: 'RUNS_ON_MACOS',
};

/**
 * The labels a repository's workflows ask for on this system: the RUNS_ON_* variable the pool wrote (it holds
 * the pool label too, such as crsp-home), else the labels of its policy, else the plain self-hosted label.
 */
export function defaultLabels(
  repo: Pick<Repository, 'variables' | 'policy'> | undefined,
  os: OS,
): string {
  const v = repo?.variables.find((x) => x.name === RUNS_ON[os] && x.value);
  if (v?.value) {
    try {
      const list: unknown = JSON.parse(v.value);
      if (Array.isArray(list)) {
        const labels = list.filter((x): x is string => typeof x === 'string');
        if (labels.length > 0) return labels.join(', ');
      }
    } catch {
      // A value that is not a list falls through to the policy.
    }
  }
  const policy = repo?.policy.labels ?? [];
  return policy.length > 0 ? policy.join(', ') : 'self-hosted';
}

export function AddRunnersWizard() {
  const client = useClient();
  const navigate = useNavigate();
  const [params, setParams] = useSearchParams();
  const storedPlan = useRef(params.get('plan')).current;

  const [phase, setPhase] = useState<WizardPhase>('loading');
  const [step, setStep] = useState(1);
  const [machines, setMachines] = useState<Schemas['Machine'][]>([]);
  const [repos, setRepos] = useState<Repository[]>([]);
  const [os, setOs] = useState<OS>('linux');
  const [machineId, setMachineId] = useState('');
  const [count, setCount] = useState(1);
  // The kind the user picked; until then the D94(b) default stands. Keep dependencies is for a persistent runner.
  const [kindPick, setKindPick] = useState<Kind | undefined>(undefined);
  const [keepDeps, setKeepDeps] = useState(true);
  const [labels, setLabelsText] = useState('self-hosted');
  // Labels follow the repository's own variables until the user writes their own (finding 12).
  const [labelsEdited, setLabelsEdited] = useState(false);
  const setLabels = (v: string) => {
    setLabelsEdited(true);
    setLabelsText(v);
  };
  const [plan, setPlan] = useState<Plan | undefined>(undefined);
  const [problem, setProblem] = useState<string | undefined>(undefined);
  // The step that failed (index), and the reason a plan was refused, shown on step 1.
  const [failedAt, setFailedAt] = useState<number | undefined>(undefined);
  const [refusal, setRefusal] = useState<string | undefined>(undefined);
  const [stopping, setStopping] = useState(false);
  const [stopError, setStopError] = useState<string | undefined>(undefined);
  const [errors, setErrors] = useState<Record<string, string>>({});

  const settle = (p: Plan, afterReload: boolean) => {
    setPlan(p);
    setStopping(false);
    setStep(4);
    if (p.state === 'done') setPhase(afterReload ? 'done-after-reload' : 'done');
    else if (p.state === 'stopped') setPhase('stopped');
    else {
      setProblem(p.steps.find((s) => s.state === 'failed')?.error ?? 'A step failed.');
      setFailedAt(2);
      setPhase('failed');
    }
  };

  useEffect(() => {
    let on = true;
    void (async () => {
      try {
        const [m, r] = await Promise.all([
          unwrap(client.GET('/api/v1/machines')),
          allRepositories(client, { pool: 'on' }),
        ]);
        if (!on) return;
        setMachines(m.items);
        setRepos(r.items);
        const first = m.items.find((x) => machineSupports(x, 'linux')) ?? m.items[0];
        if (first) {
          setOs(machineSupports(first, 'linux') ? 'linux' : first.os);
          setMachineId(first.id);
        }
        if (storedPlan) {
          const p = await unwrap(
            client.GET('/api/v1/runner-plans/{planId}', {
              params: { path: { planId: storedPlan } },
            }),
          );
          if (!on) return;
          if (p.state === 'applying') {
            setPlan(p);
            setStep(3);
            setPhase('paused');
          } else settle(p, true);
          return;
        }
        setStep(1);
        setPhase('first-run');
      } catch (e) {
        if (!on) return;
        setProblem(e instanceof Error ? e.message : 'The controller did not answer.');
        setFailedAt(0);
        setStep(4);
        setPhase('failed');
      }
    })();
    return () => {
      on = false;
    };
  }, [client, storedPlan]);

  // No repository is asked for (D31): the server serves the pool, so the labels follow the repository it will
  // take, the first with the pool on, or the plain self-hosted label while none has it.
  const wanted = defaultLabels(repos[0], os);
  useEffect(() => {
    if (!labelsEdited) setLabelsText(wanted);
  }, [wanted, labelsEdited]);
  const supports = (m: Schemas['Machine']) => machineSupports(m, os);
  const picked = machines.find((m) => m.id === machineId);
  const choice = chooseKind(picked, os, repos, kindPick);
  const labelList = labels
    .split(',')
    .map((x) => x.trim())
    .filter(Boolean);

  const toReview = async () => {
    const e: Record<string, string> = {};
    if (!machineId || !machines.some((m) => m.id === machineId && supports(m)))
      e.machine = 'Pick a machine that runs this system.';
    if (count < 1) e.count = 'Add at least 1 runner.';
    setErrors(e);
    setRefusal(undefined);
    if (Object.keys(e).length) return;
    if (containerOff) {
      setRefusal(NOT_ALLOWED);
      return;
    }
    setStep(2);
    setPhase('planning');
    try {
      const p = await unwrap(
        client.POST('/api/v1/runner-plans', {
          body: {
            os,
            kind: choice.kind,
            machine_id: machineId,
            count,
            labels: labelList,
            ...(choice.kind === 'persistent' ? { keep_dependencies: keepDeps } : {}),
          },
        }),
      );
      if (p.steps.length === 0) {
        // Review never lists nothing: with no step there is nothing to review.
        setRefusal(NO_STEPS);
        setStep(1);
        setPhase('first-run');
        return;
      }
      setPlan(p);
      setPhase('ready-to-review');
    } catch (err) {
      // A refusal of the controller is shown on step 1 with its reason; the user changes the choices there.
      setRefusal(sentence(errorText(err)));
      setStep(1);
      setPhase('first-run');
    }
  };

  const { mounted, sleep } = useMounted();
  const apply = async () => {
    if (!plan) return;
    setStep(3);
    setPhase('running');
    try {
      let p = await unwrap(
        client.POST('/api/v1/runner-plans/{planId}/apply', {
          params: { path: { planId: plan.id } },
          body: { approved_steps: needsYes(plan) },
        }),
      );
      const next = new URLSearchParams(params);
      next.set('plan', p.id);
      setParams(next, { replace: true });
      setPlan(p);
      while (p.state === 'applying' || p.state === 'planned') {
        if (!(await sleep(TIMING.planPollMs))) {
          return;
        }
        p = await unwrap(
          client.GET('/api/v1/runner-plans/{planId}', {
            params: { path: { planId: p.id } },
          }),
        );
        if (!mounted.current) {
          return;
        }
        setPlan(p);
      }
      settle(p, false);
    } catch (err) {
      setProblem(err instanceof Error ? err.message : 'Could not register the runners.');
      setFailedAt(2);
      setStep(4);
      setPhase('failed');
    }
  };

  // Stop (web-console 11, UX-30): the step that runs finishes, no later step starts, and the wizard ends
  // at Done saying what was done and what is left. The plan's own state tells when it has ended.
  const stop = async () => {
    if (!plan) return;
    setStopping(true);
    setStopError(undefined);
    try {
      await unwrap(
        client.POST('/api/v1/runner-plans/{planId}/stop', {
          params: { path: { planId: plan.id } },
        }),
      );
    } catch (e) {
      // 409: the plan has already ended; the next read shows how.
      if (!(e instanceof ApiError && e.status === 409)) {
        setStopError(errorText(e));
        setStopping(false);
      }
    }
  };

  // Try again never sends the same request: the user goes back to step 1 with the reason, to change a choice.
  const tryAgain = () => {
    const why = problem;
    startAgain();
    if (why) setRefusal(sentence(why));
  };

  const startAgain = () => {
    setParams(new URLSearchParams(), { replace: true });
    setPlan(undefined);
    setProblem(undefined);
    setFailedAt(undefined);
    setRefusal(undefined);
    setStep(1);
    setPhase('first-run');
  };

  const containerRunner = picked !== undefined && crossesTo(picked, os);
  const containerOff = picked !== undefined && choice.none;
  const ephemeral = choice.kind === 'ephemeral';
  const backendReason =
    picked !== undefined &&
    Object.values(choice.reasons).some((r) => r?.includes('container backend'));

  const prog = plan ? planProgress(plan) : { done: 0, total: 0 };

  let body;
  if (step <= 1) {
    body = (
      <Stack>
        {machines.length === 0 ? (
          <Message
            tone="needs-you"
            action={<LinkButton to="/pool/machines/add">Add a machine</LinkButton>}
          >
            Add a machine first. Runners live on a machine.
          </Message>
        ) : null}
        <ChoiceField
          label="System"
          value={os}
          onChange={(v) => {
            setOs(v as OS);
            const m = machines.find((x) => machineSupports(x, v as OS));
            setMachineId(m?.id ?? '');
          }}
          choices={[
            { value: 'linux', label: 'Linux' },
            { value: 'windows', label: 'Windows' },
            { value: 'macos', label: 'macOS' },
          ]}
        />
        <ChoiceField
          label="Machine"
          value={machineId}
          error={errors.machine}
          onChange={setMachineId}
          choices={machines.map((m) => ({
            value: m.id,
            label: m.name,
            ...(supports(m)
              ? {}
              : { disabledReason: `This machine runs ${m.os}, not ${os}.` }),
          }))}
        />
        {picked && containerOff ? (
          <Message
            tone="needs-you"
            action={
              <LinkButton to={`/pool/machines/${picked.id}`}>
                Allow it on the machine
              </LinkButton>
            }
          >
            {NOT_ALLOWED}
          </Message>
        ) : null}
        {refusal ? <Message tone="problem">{refusal}</Message> : null}
        <ChoiceField
          label="Type"
          value={choice.kind}
          onChange={(v) => setKindPick(v as Kind)}
          choices={(['persistent', 'ephemeral'] as const).map((k) => ({
            value: k,
            label: KIND_NAMES[k],
            description: KIND_HELP[k],
            ...(choice.reasons[k] ? { disabledReason: choice.reasons[k] } : {}),
          }))}
        />
        {choice.line ? <Text tone="muted">{choice.line}</Text> : null}
        {picked && backendReason && !containerOff ? (
          <Message
            tone="note"
            action={
              <LinkButton to={`/pool/machines/${picked.id}`}>
                Allow it on the machine
              </LinkButton>
            }
          >
            The container backend decides which runner types this machine offers.
          </Message>
        ) : null}
        {containerRunner && !containerOff && !ephemeral ? (
          <Message tone="note">
            Linux runners on this Windows machine run in containers, through Docker. The
            container backend must be allowed on the machine.
          </Message>
        ) : null}
        <NumberField
          label={ephemeral ? 'Most ephemeral runners at once' : 'Number of runners'}
          {...(ephemeral
            ? {
                help: 'The pool makes a fresh container for each job, up to this many at the same time.',
              }
            : {})}
          value={count}
          min={1}
          max={32}
          onChange={setCount}
          {...(errors.count ? { error: errors.count } : {})}
        />
        {ephemeral ? null : (
          <CheckRow
            label="Keep downloaded dependencies"
            checked={keepDeps}
            onChange={setKeepDeps}
            help="Temporary files of each job are always removed."
          />
        )}
        <TextField
          label="Labels"
          help="Separate labels with commas."
          value={labels}
          onChange={setLabels}
          width="full"
        />
        <Text tone="muted">Runners serve every repository that uses the pool.</Text>
      </Stack>
    );
  } else if (step === 2) {
    body = (
      <Stack>
        <Text>
          {ephemeral
            ? `Up to ${count} ephemeral ${count === 1 ? 'runner' : 'runners'} at once`
            : `${count} persistent ${count === 1 ? 'runner' : 'runners'}`}{' '}
          on {picked?.name ?? 'the machine'}.{' '}
          {ephemeral
            ? 'Each job gets a fresh container.'
            : keepDeps
              ? 'Downloaded dependencies are kept between jobs. Temporary files of each job are always removed.'
              : 'Downloaded dependencies are removed after each job too. Temporary files of each job are always removed.'}{' '}
          These steps run:
        </Text>
        <BulletList items={(plan?.steps ?? []).map((s) => s.text)} />
        {picked ? <ReadyMadeLine arch={picked.arch} /> : null}
      </Stack>
    );
  } else if (step === 3) {
    body = (
      <Stack>
        {phase === 'paused' ? (
          <Message tone="needs-you">
            Paused. The page closed while runners were registering. Start again to plan
            what is left.
          </Message>
        ) : null}
        {stopError ? <Message tone="problem">Could not stop. {stopError}</Message> : null}
        {picked ? (
          <PullingStep
            machineId={picked.id}
            machineName={picked.name}
            active={phase === 'running'}
          />
        ) : null}
        <Facts
          items={(plan?.steps ?? []).map((s) => ({ name: s.text, value: s.state }))}
        />
      </Stack>
    );
  } else {
    const done = plan?.steps.filter((s) => s.state === 'done') ?? [];
    const failed = plan?.steps.filter((s) => s.state === 'failed') ?? [];
    const left =
      plan?.steps.filter((s) => s.state !== 'done' && s.state !== 'failed') ?? [];
    body = (
      <Stack>
        {phase === 'failed' ? (
          <Message tone="problem">
            {failed.length} failed, {done.length} done.{' '}
            {sentence(problem ?? 'A step failed.')} A failed runner stays in Runners with
            its reason. Open Runners, remove it, fix the cause, then add it again.
          </Message>
        ) : null}
        {phase === 'stopped' ? (
          <Message tone="note">
            Stopped. {done.length} steps were done and {left.length} are left.
          </Message>
        ) : null}
        {phase === 'done' || phase === 'done-after-reload' ? (
          <Message tone="done">
            {done.length} {done.length === 1 ? 'step' : 'steps'} done. The runners are
            added.
          </Message>
        ) : null}
        <Facts
          items={[
            ...done.map((s) => ({ name: s.text, value: 'done' })),
            ...failed.map((s) => ({
              name: s.text,
              value: `failed: ${s.error ?? 'no reason given'}`,
            })),
            ...left.map((s) => ({ name: s.text, value: 'left' })),
            ...(plan ? [] : [{ name: 'Runners added', value: '0' }]),
          ]}
        />
      </Stack>
    );
  }

  return (
    <Wizard
      name="Add runners"
      steps={STEPS}
      current={step - 1}
      firstStep={0}
      phase={phase}
      reading="Reading machines"
      progress={
        phase === 'running'
          ? {
              text: `Registering ${Math.min(prog.done + 1, Math.max(prog.total, 1))} of ${prog.total}${prog.current ? `: ${prog.current}` : ''}`,
              value: prog.done,
              max: Math.max(prog.total, 1),
            }
          : undefined
      }
      canStop={phase === 'running' && Boolean(plan)}
      stopping={stopping}
      onStop={() => void stop()}
      onBack={() => {
        setStep(1);
        setPhase('first-run');
      }}
      onStep={(i) => {
        if (i === 0) {
          setStep(1);
          setPhase('first-run');
        }
      }}
      primary={
        step <= 1
          ? {
              label: 'Next',
              onPress: () => void toReview(),
              disabled: machines.length === 0 || containerOff,
              reason: machines.length === 0 ? 'Add a machine first.' : NOT_ALLOWED,
            }
          : {
              label: ephemeral
                ? `Allow ${count} ephemeral ${count === 1 ? 'runner' : 'runners'}`
                : `Add ${count} ${count === 1 ? 'runner' : 'runners'}`,
              busyLabel: 'Registering',
              onPress: () => void apply(),
            }
      }
      review={step === 2}
      onStartAgain={startAgain}
      onTryAgain={tryAgain}
      failedAt={failedAt}
      donePrimary={{
        label: 'Open Runners',
        onPress: () => void navigate('/pool/runners'),
      }}
    >
      <Section
        title={
          step <= 1
            ? 'Runners to add'
            : step === 2
              ? 'Review'
              : step === 3
                ? 'Registering'
                : 'Result'
        }
      >
        {body}
      </Section>
    </Wizard>
  );
}

export function AddRunnersPage() {
  return (
    <Page>
      <Breadcrumbs
        trail={[{ to: '/pool/runners', label: 'Runners' }, { label: 'Add runners' }]}
      />
      <PageHeader title="Add runners" />
      <AddRunnersWizard />
    </Page>
  );
}
