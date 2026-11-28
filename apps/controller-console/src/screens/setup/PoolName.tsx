// The pool name wizard (runners R5.141 to R5.145, web-console 4.1). The name is the runner label every crsp runner
// carries and crsp writes into each repository's CRSP_RUNS_ON_* variables. At the first setup it names the pool
// (mode `first`); later it renames it (mode `rename`): runners get the new label first, then the variables, then the
// old label goes. Steps: Name, Review, Renaming, Done. The stored rename id is in the address (?rename=), so Done
// after a reload reads the outcome from the server.
import {
  BulletList,
  Breadcrumbs,
  Facts,
  Message,
  Page,
  PageHeader,
  Pane,
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

type PoolRename = Schemas['PoolRename'];
type PoolInfo = Schemas['PoolName'];

const STEPS = ['Name', 'Review', 'Renaming', 'Done'] as const;
const FIRST_STEPS = ['Name', 'Review', 'Saving', 'Done'] as const;

const NAME_RE = /^crsp-[a-z0-9]+(-[a-z0-9]+)*$/;
export const MAX_NAME = 40;

/** The mistake in a pool name, in the words of the content file; undefined when the name is fine. */
export function nameProblem(name: string): string | undefined {
  if (!name.trim()) return 'Give the pool a name.';
  if (name.length > MAX_NAME) return `A pool name has at most ${MAX_NAME} characters.`;
  if (!NAME_RE.test(name))
    return 'A pool name starts with crsp- and then small letters, digits and hyphens, like crsp-home.';
  return undefined;
}

export function PoolNameWizard({
  mode,
  onDone,
}: {
  mode: 'first' | 'rename';
  onDone?: () => void;
}) {
  const client = useClient();
  const navigate = useNavigate();
  const [params, setParams] = useSearchParams();
  const stored = useRef(params.get('rename')).current;
  const { mounted, sleep } = useMounted();
  const first = mode === 'first';

  const [phase, setPhase] = useState<WizardPhase>('loading');
  const [step, setStep] = useState(1);
  const [info, setInfo] = useState<PoolInfo | undefined>(undefined);
  const [name, setName] = useState('crsp-home');
  const [error, setError] = useState<string | undefined>(undefined);
  const [job, setJob] = useState<PoolRename | undefined>(undefined);
  const [problem, setProblem] = useState<string | undefined>(undefined);
  const [stopping, setStopping] = useState(false);
  const [stopError, setStopError] = useState<string | undefined>(undefined);

  const settle = (j: PoolRename, afterReload: boolean) => {
    setJob(j);
    setStopping(false);
    setStep(4);
    if (j.state === 'done') setPhase(afterReload ? 'done-after-reload' : 'done');
    else if (j.state === 'stopped') setPhase('stopped');
    else {
      setProblem(j.steps.find((s) => s.state === 'failed')?.error ?? 'A step failed.');
      setPhase('failed');
    }
  };

  const follow = async (id: string) => {
    let j = await unwrap(
      client.GET('/api/v1/pool/renames/{renameId}', {
        params: { path: { renameId: id } },
      }),
    );
    setJob(j);
    while (j.state === 'applying') {
      if (!(await sleep(TIMING.planPollMs))) return;
      j = await unwrap(
        client.GET('/api/v1/pool/renames/{renameId}', {
          params: { path: { renameId: id } },
        }),
      );
      if (!mounted.current) return;
      setJob(j);
    }
    settle(j, false);
    onDone?.();
  };

  useEffect(() => {
    let on = true;
    void (async () => {
      try {
        const i = await unwrap(client.GET('/api/v1/pool/name'));
        if (!on) return;
        setInfo(i);
        setName(i.name);
        const id = stored ?? (!first ? i.pending?.id : undefined);
        if (id && !first) {
          const j = await unwrap(
            client.GET('/api/v1/pool/renames/{renameId}', {
              params: { path: { renameId: id } },
            }),
          );
          if (!on) return;
          setName(j.new);
          if (j.state === 'applying') {
            setJob(j);
            setStep(3);
            setPhase('paused');
          } else if (j.state === 'planned') {
            setJob(j);
            setStep(2);
            setPhase('ready-to-review');
          } else settle(j, true);
          return;
        }
        setStep(1);
        setPhase('first-run');
      } catch (e) {
        if (!on) return;
        setProblem(errorText(e));
        setStep(4);
        setPhase('failed');
      }
    })();
    return () => {
      on = false;
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [client]);

  const toReview = async () => {
    const mistake = nameProblem(name);
    setError(mistake);
    if (mistake) return;
    if (first) {
      setStep(2);
      setPhase('ready-to-review');
      return;
    }
    if (name === info?.name && !job) {
      setError(`The pool is called ${name} already.`);
      return;
    }
    setStep(2);
    setPhase('planning');
    try {
      const j = await unwrap(client.POST('/api/v1/pool/renames', { body: { name } }));
      setJob(j);
      setPhase('ready-to-review');
    } catch (e) {
      setStep(1);
      setPhase('first-run');
      setError(errorText(e));
    }
  };

  const run = async () => {
    setStep(3);
    setPhase('running');
    try {
      if (first) {
        const i = await unwrap(client.PUT('/api/v1/pool/name', { body: { name } }));
        setInfo(i);
        setStep(4);
        setPhase('done');
        onDone?.();
        return;
      }
      if (!job) return;
      const j = await unwrap(
        client.POST('/api/v1/pool/renames/{renameId}/apply', {
          params: { path: { renameId: job.id } },
        }),
      );
      const next = new URLSearchParams(params);
      next.set('rename', j.id);
      setParams(next, { replace: true });
      setJob(j);
      await follow(j.id);
    } catch (e) {
      setProblem(errorText(e));
      setStep(4);
      setPhase('failed');
    }
  };

  // Run again after a stop or a failure: the same name plans the same rename, and steps done are not repeated.
  const runAgain = async () => {
    const again = job?.new ?? name;
    setStopError(undefined);
    setStep(3);
    setPhase('running');
    try {
      const j = await unwrap(
        client.POST('/api/v1/pool/renames', { body: { name: again } }),
      );
      setJob(j);
      const a = await unwrap(
        client.POST('/api/v1/pool/renames/{renameId}/apply', {
          params: { path: { renameId: j.id } },
        }),
      );
      const next = new URLSearchParams(params);
      next.set('rename', a.id);
      setParams(next, { replace: true });
      setJob(a);
      await follow(a.id);
    } catch (e) {
      setProblem(errorText(e));
      setStep(4);
      setPhase('failed');
    }
  };

  const stop = async () => {
    if (!job) return;
    setStopping(true);
    setStopError(undefined);
    try {
      await unwrap(
        client.POST('/api/v1/pool/renames/{renameId}/stop', {
          params: { path: { renameId: job.id } },
        }),
      );
    } catch (e) {
      // 409: the rename has ended already; the next read shows how.
      if (!(e instanceof ApiError && e.status === 409)) {
        setStopError(errorText(e));
        setStopping(false);
      }
    }
  };

  const startAgain = () => {
    setParams(new URLSearchParams(), { replace: true });
    setJob(undefined);
    setProblem(undefined);
    setError(undefined);
    setStep(1);
    setPhase('first-run');
  };

  const steps = job?.steps ?? [];
  const done = steps.filter((s) => s.state === 'done');
  const failed = steps.filter((s) => s.state === 'failed');
  const left = steps.filter((s) => s.state !== 'done' && s.state !== 'failed');
  const doneCount = done.length;
  const total = Math.max(steps.length, 1);
  const running = steps.find((s) => s.state === 'running');
  const overridden = info?.env_override === true;

  let body;
  if (step <= 1) {
    body = (
      <Pane title={first ? 'Name the pool' : 'Rename the pool'}>
        <Stack>
          <Text>
            The pool name is the runner label every crsp runner carries. crsp writes it
            into each repository's CRSP_RUNS_ON variables.
          </Text>
          {overridden ? (
            <Message tone="needs-you">
              The service sets the pool name with CRSP_POOL_NAME, which wins over this
              page. Remove that value from the service, then name the pool here.
            </Message>
          ) : null}
          <TextField
            label="Pool name"
            help="Small letters, digits and hyphens, starting with crsp-, like crsp-home."
            value={name}
            error={error}
            onChange={(v) => {
              setName(v);
              setError(undefined);
            }}
          />
        </Stack>
      </Pane>
    );
  } else if (step === 2) {
    body = (
      <Pane title="Review">
        <Stack>
          {first ? (
            <Text>The pool will be called {name}. Nothing carries the label yet.</Text>
          ) : (
            <>
              <Facts
                items={[
                  { name: 'Old name', value: job?.old ?? info?.name ?? '' },
                  { name: 'New name', value: job?.new ?? name },
                  { name: 'Runners', value: String(job?.counts.runners ?? 0) },
                  { name: 'Repositories', value: String(job?.counts.repositories ?? 0) },
                  ...(job?.counts.scale_set
                    ? [{ name: 'Scale set', value: 'started again under the new name' }]
                    : []),
                ]}
              />
              <Message tone="note">
                A job queued with {job?.old ?? 'the old name'} still finds a runner until
                the last step takes that label off. Runners made for one job get the new
                label when they are made.
              </Message>
              <BulletList items={steps.map((s) => s.text)} />
            </>
          )}
        </Stack>
      </Pane>
    );
  } else if (step === 3) {
    body = (
      <Pane title={first ? 'Saving' : 'Renaming'}>
        <Stack>
          {phase === 'paused' ? (
            <Message tone="needs-you">
              Paused. The page closed while the rename ran. Run it again to go on where it
              ended.
            </Message>
          ) : null}
          {stopError ? (
            <Message tone="problem">Could not stop. {stopError}</Message>
          ) : null}
          <Facts items={steps.map((s) => ({ name: s.text, value: s.state }))} />
        </Stack>
      </Pane>
    );
  } else {
    body = (
      <Pane title="Result">
        <Stack>
          {phase === 'failed' && !job ? (
            <Message tone="problem">
              The pool was not named. {problem ?? ''} Try again.
            </Message>
          ) : null}
          {phase === 'stopped' ? (
            <Message tone="note">
              Stopped. {doneCount} {doneCount === 1 ? 'step was' : 'steps were'} done and{' '}
              {left.length} {left.length === 1 ? 'is' : 'are'} left. Jobs queued with{' '}
              {job?.old} still find a runner. Run again goes on where it ended.
            </Message>
          ) : null}
          {phase === 'failed' && job ? (
            <Message tone="problem">
              {failed.length} {failed.length === 1 ? 'step' : 'steps'} failed.{' '}
              {problem ?? ''} {doneCount} done, {left.length} left. Run again repeats the
              failed step and goes on.
            </Message>
          ) : null}
          {phase === 'done' || phase === 'done-after-reload' ? (
            <Message tone="done">The pool is called {job?.new ?? name}.</Message>
          ) : null}
          {job ? (
            <Facts
              items={[
                ...done.map((s) => ({ name: s.text, value: 'done' })),
                ...failed.map((s) => ({
                  name: s.text,
                  value: `failed: ${s.error ?? 'no reason given'}`,
                })),
                ...left.map((s) => ({ name: s.text, value: 'left' })),
              ]}
            />
          ) : null}
        </Stack>
      </Pane>
    );
  }

  return (
    <Wizard
      name={first ? 'Name the pool' : 'Rename the pool'}
      steps={first ? FIRST_STEPS : STEPS}
      current={step - 1}
      firstStep={0}
      phase={phase}
      reading="Reading the pool name"
      progress={
        phase === 'running' && !first
          ? {
              text: `Renaming ${Math.min(doneCount + 1, total)} of ${total}${running ? `: ${running.text}` : ''}`,
              value: doneCount,
              max: total,
            }
          : undefined
      }
      canStop={phase === 'running' && !first && Boolean(job)}
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
              disabled: overridden,
              reason: 'The service sets the name.',
            }
          : {
              label: first ? 'Name the pool' : 'Rename the pool',
              busyLabel: first ? 'Saving' : 'Renaming',
              onPress: () => void run(),
            }
      }
      review={step === 2}
      endPrimary={{ label: 'Run again', onPress: () => void runAgain() }}
      onStartAgain={startAgain}
      onTryAgain={job ? () => void runAgain() : startAgain}
      {...(first
        ? {}
        : {
            donePrimary: {
              label: 'Open Setup',
              onPress: () => void navigate('/setup/summary'),
            },
          })}
    >
      {body}
    </Wizard>
  );
}

export function PoolRenamePage() {
  return (
    <Page>
      <Breadcrumbs
        trail={[{ to: '/setup/summary', label: 'Setup' }, { label: 'Rename the pool' }]}
      />
      <PageHeader title="Rename the pool" />
      <PoolNameWizard mode="rename" />
    </Page>
  );
}
