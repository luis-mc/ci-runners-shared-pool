// The Turn on, Turn off and Repair wizard of Pool setup (CRSP340, the owner's pick of option 5). Review, the work, Done.
// Review names the repositories in plain groups; the work shows one card per repository and the Terminal with each
// step as a command and its result (UX-88). The raw steps stay in a fold. The route is
// /setup/pool/change?to=on|off|repair&repo=<id>; the plan id is kept in the address (?plan=) so a reload reads the outcome.
import {
  BulletList,
  Card,
  CardGrid,
  Disclosure,
  Facts,
  Message,
  Page,
  Pane,
  Progress,
  Stack,
  Status,
  Text,
  Wizard,
  type StatusKind,
  type WizardPhase,
} from '@crsp/ui';
import { useEffect, useRef, useState, type ReactNode } from 'react';
import { useNavigate, useSearchParams } from 'react-router-dom';
import { TIMING } from '../../timing';
import { ApiError, errorText, unwrap, useClient } from '../../api/api';
import { useMounted } from '../../api/mounted';
import { planNotes, planPhases, useLiveLog } from '../liveLog';
import { allRepositories, needsYes, type Plan, type Repository } from './plans';

export type Dir = 'on' | 'off' | 'repair';

const count = (n: number, one: string, many: string) => `${n} ${n === 1 ? one : many}`;
const repos = (n: number) => count(n, 'repository', 'repositories');

const stepsOf = (plan: Plan | undefined, full: string) =>
  (plan?.steps ?? []).filter((s) => s.target === full);

/** One repository's state in the work: waiting, working, done or failed. */
export function repoState(
  plan: Plan | undefined,
  full: string,
): { kind: StatusKind; word: string } {
  const st = stepsOf(plan, full);
  // A repair that found nothing to change has no step for the repository: when the plan is done
  // it shows the end state, never "waiting".
  if (st.length === 0 && plan?.state === 'done') return { kind: 'ok', word: 'done' };
  if (st.length > 0 && st.every((s) => s.state === 'done' || s.state === 'skipped'))
    return { kind: 'ok', word: 'done' };
  if (st.some((s) => s.state === 'failed')) return { kind: 'failed', word: 'failed' };
  if (st.some((s) => s.state === 'running' || s.state === 'done'))
    return { kind: 'running', word: 'working' };
  return { kind: 'waiting', word: 'waiting' };
}

const WORDS: Record<Dir, { work: string; name: string; button: string }> = {
  on: { work: 'Turning on', name: 'Turn on the pool', button: 'Turn on' },
  off: { work: 'Turning off', name: 'Turn off the pool', button: 'Turn off' },
  repair: { work: 'Repairing', name: 'Repair the pool', button: 'Repair' },
};

function Review({ dir, list, plan }: { dir: Dir; list: Repository[]; plan?: Plan }) {
  const title =
    dir === 'on'
      ? 'These will start using the pool'
      : dir === 'off'
        ? 'These will stop using the pool'
        : 'These will be repaired';
  return (
    <Stack>
      <Pane title={title} count={list.length}>
        <Stack>
          <BulletList items={list.map((r) => r.full_name)} label={title} />
          {dir === 'off' ? (
            <Text tone="muted">
              Their runners are removed and their settings are kept.
            </Text>
          ) : null}
          {dir === 'repair' ? (
            <Text tone="muted">The missing variable is written again.</Text>
          ) : null}
        </Stack>
      </Pane>
      <Disclosure summary={`Technical steps (${plan?.steps.length ?? 0})`}>
        <BulletList
          items={(plan?.steps ?? []).map((s) => s.text)}
          label="Technical steps"
        />
      </Disclosure>
    </Stack>
  );
}

function Work({
  list,
  plan,
  verb,
}: {
  list: Repository[];
  plan: Plan | undefined;
  verb: string;
}) {
  return (
    <CardGrid>
      {list.map((r) => {
        const st = stepsOf(plan, r.full_name);
        const done = st.filter((s) => s.state === 'done' || s.state === 'skipped').length;
        const s = repoState(plan, r.full_name);
        return (
          <Card key={r.id} title={r.full_name}>
            <Stack gap="tight">
              <Status kind={s.kind} word={s.word} />
              <Progress
                label={`${verb} ${r.full_name}`}
                value={done}
                max={Math.max(st.length, 1)}
              />
            </Stack>
          </Card>
        );
      })}
    </CardGrid>
  );
}

export function ChangePoolWizard() {
  const client = useClient();
  const navigate = useNavigate();
  const [params, setParams] = useSearchParams();
  const raw = params.get('to');
  const dir: Dir = raw === 'off' || raw === 'repair' ? raw : 'on';
  const ids = params.getAll('repo').map(Number);
  const storedPlan = useRef(params.get('plan')).current;
  const { mounted, sleep } = useMounted();
  const word = WORDS[dir];

  const [phase, setPhase] = useState<WizardPhase>('loading');
  const [step, setStep] = useState(0);
  const [list, setList] = useState<Repository[]>([]);
  const [plan, setPlan] = useState<Plan | undefined>(undefined);
  const [problem, setProblem] = useState<string | undefined>(undefined);
  const [stopping, setStopping] = useState(false);
  const seen = useRef(new Map<string, string>());
  const log = useLiveLog(planPhases(plan));
  const record = (p: Plan) => {
    for (const n of planNotes(p, seen.current)) log.say(n, planPhases(p));
  };

  const settle = (p: Plan, afterReload: boolean) => {
    setPlan(p);
    record(p);
    setStopping(false);
    setStep(2);
    if (p.state === 'done') setPhase(afterReload ? 'done-after-reload' : 'done');
    else if (p.state === 'stopped') setPhase('stopped');
    else {
      setProblem(p.steps.find((s) => s.state === 'failed')?.error ?? 'A step failed.');
      setPhase('failed');
    }
  };

  const toReview = async (chosen: Repository[]) => {
    setStep(0);
    setPhase('planning');
    try {
      const p = await unwrap(
        client.POST('/api/v1/repository-plans', {
          body: {
            changes: chosen.map((r) => ({
              repository_id: r.id,
              pool:
                dir === 'off'
                  ? ('off' as const)
                  : dir === 'repair'
                    ? ('repair' as const)
                    : ('on' as const),
            })),
          },
        }),
      );
      setPlan(p);
      if (p.state === 'done') {
        // Nothing differs: the plan is already done, so there is nothing to review or apply.
        setStep(2);
        setPhase('done');
      } else setPhase('ready-to-review');
    } catch (e) {
      setProblem(errorText(e));
      setStep(2);
      setPhase('failed');
    }
  };

  useEffect(() => {
    let on = true;
    void (async () => {
      try {
        const all = await allRepositories(client);
        if (!on) return;
        const chosen = all.items.filter((r) => ids.includes(r.id));
        setList(chosen);
        if (storedPlan) {
          const p = await unwrap(
            client.GET('/api/v1/repository-plans/{planId}', {
              params: { path: { planId: storedPlan } },
            }),
          );
          if (!on) return;
          if (p.state === 'applying') {
            setPlan(p);
            setStep(1);
            setPhase('paused');
          } else settle(p, true);
          return;
        }
        if (chosen.length === 0) {
          setStep(0);
          setPhase('first-run');
          return;
        }
        await toReview(chosen);
      } catch (e) {
        if (!on) return;
        setProblem(errorText(e));
        setStep(2);
        setPhase('failed');
      }
    })();
    return () => {
      on = false;
    };
    // The stored run is read once, when the page opens.
  }, [client]);

  const apply = async () => {
    if (!plan) return;
    setStep(1);
    setPhase('running');
    try {
      let p = await unwrap(
        client.POST('/api/v1/repository-plans/{planId}/apply', {
          params: { path: { planId: plan.id } },
          body: { approved_steps: needsYes(plan) },
        }),
      );
      const next = new URLSearchParams(params);
      next.set('plan', p.id);
      setParams(next, { replace: true });
      setPlan(p);
      record(p);
      while (p.state === 'applying' || p.state === 'planned') {
        if (!(await sleep(TIMING.planPollMs))) return;
        p = await unwrap(
          client.GET('/api/v1/repository-plans/{planId}', {
            params: { path: { planId: p.id } },
          }),
        );
        if (!mounted.current) return;
        setPlan(p);
        record(p);
      }
      settle(p, false);
    } catch (e) {
      setProblem(errorText(e));
      setStep(2);
      setPhase('failed');
    }
  };

  const stop = async () => {
    if (!plan) return;
    setStopping(true);
    try {
      await unwrap(
        client.POST('/api/v1/repository-plans/{planId}/stop', {
          params: { path: { planId: plan.id } },
        }),
      );
    } catch (e) {
      if (!(e instanceof ApiError && e.status === 409)) setStopping(false);
    }
  };

  const back = () => void navigate('/setup/pool');
  const reposDone = list.filter(
    (r) => repoState(plan, r.full_name).word === 'done',
  ).length;
  const current = list.find((r) => repoState(plan, r.full_name).word === 'working');
  const total = plan?.steps.length ?? 0;
  const stepsDone = plan?.steps.filter((s) => s.state === 'done').length ?? 0;

  let body: ReactNode;
  if (phase === 'first-run')
    body = (
      <Message tone="needs-you">
        No repository was chosen. Go back to Pool setup and tick the repositories first.
      </Message>
    );
  else if (step === 0)
    body = <Review dir={dir} list={list} {...(plan ? { plan } : {})} />;
  else if (step === 1) body = <Work list={list} plan={plan} verb={word.work} />;
  else {
    const done =
      dir === 'on'
        ? `${repos(list.length)} now ${list.length === 1 ? 'uses' : 'use'} the pool.`
        : dir === 'off'
          ? `${repos(list.length)} no longer ${list.length === 1 ? 'uses' : 'use'} the pool.`
          : plan?.steps.length === 0
            ? 'Nothing to repair: the variables are already right.'
            : `${repos(list.length)} repaired.`;
    body = (
      <Pane title="Result">
        <Stack>
          {phase === 'failed' ? (
            <Message tone="problem">{`Nothing more was changed. ${problem ?? ''}`}</Message>
          ) : phase === 'stopped' ? (
            <Message tone="note">{`Stopped. ${reposDone} of ${list.length} done.`}</Message>
          ) : (
            <Message tone="done">{done}</Message>
          )}
          <Facts
            items={list.map((r) => {
              const s = repoState(plan, r.full_name);
              // A finished repository says where it stands now, in the words of Pool setup.
              const now =
                s.word !== 'done'
                  ? s
                  : dir === 'off'
                    ? { kind: 'off' as StatusKind, word: 'not using the pool' }
                    : { kind: 'ok' as StatusKind, word: 'using the pool' };
              return {
                name: r.full_name,
                value: <Status kind={now.kind} word={now.word} />,
              };
            })}
          />
        </Stack>
      </Pane>
    );
  }

  return (
    <Page>
      <Wizard
        name={word.name}
        steps={['Review', word.work, 'Done']}
        current={step}
        firstStep={0}
        phase={phase}
        reading="Reading the repositories"
        live={log.events}
        progress={
          phase === 'running'
            ? {
                text: `${word.work} ${Math.min(reposDone + 1, Math.max(list.length, 1))} of ${list.length}${current ? `: ${current.full_name}` : ''}`,
                value: stepsDone,
                max: Math.max(total, 1),
              }
            : undefined
        }
        canStop={phase === 'running' && Boolean(plan)}
        stopping={stopping}
        onStop={() => void stop()}
        onBack={back}
        primary={{
          label: `${word.button} ${repos(list.length)}`,
          busyLabel: word.work,
          onPress: () => void apply(),
        }}
        review={step === 0 && phase === 'ready-to-review'}
        onStartAgain={back}
        onTryAgain={back}
        donePrimary={{ label: 'Back to the repositories', onPress: back }}
      >
        {body}
      </Wizard>
    </Page>
  );
}
