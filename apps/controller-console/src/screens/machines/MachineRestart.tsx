// Restart the service of one machine (machines R5.81, operations R5.15): the Review step names the running jobs
// the restart waits for, then Restarting, then the console sees the agent connect again, then Done. No running job
// is cut. The shared wizard owns the steps, the locks and the busy state; this page passes data only. The words
// are in content.md ("Restart the service, machine page").
import {
  BulletList,
  Breadcrumbs,
  Facts,
  Message,
  Page,
  PageHeader,
  Stack,
  Text,
  Wizard,
  type WizardPhase,
} from '@crsp/ui';
import { useEffect, useRef, useState } from 'react';
import { useNavigate, useParams, useSearchParams } from 'react-router-dom';
import { unwrap, useClient, type Schemas } from '../../api/api';
import { useSession } from '../../auth/session';
import { TIMING } from '../../timing';
import { useLiveLog, type Note } from '../liveLog';

type Machine = Schemas['Machine'];

const STEPS = ['Review', 'Restarting', 'Done'] as const;

const restartOf = (m: Machine) =>
  m.pending_change?.kind === 'service_restart' ? m.pending_change : undefined;

// The weighted bar of the work step (UX-88): the drain, the restart, the reconnect.
const PHASES = [
  { name: 'Waiting for running jobs to end', weight: 2 },
  { name: 'Restarting the service', weight: 1 },
  { name: 'Connecting again', weight: 1 },
] as const;

const jobWords = (m: Machine): string[] =>
  (m.running_jobs ?? []).map((j) => `${j.repository} ${j.job_name}`);

/** The live line of the work step. */
export function restartLine(m: Machine | undefined): string {
  const c = m && restartOf(m);
  if (!m) return 'Starting';
  if (c?.state === 'waiting') {
    const n = c.waiting_for.length;
    return n > 0
      ? `Waiting for ${n} running ${n === 1 ? 'job' : 'jobs'} to end`
      : 'Waiting for running jobs to end';
  }
  return 'Restarting the service';
}

export function MachineRestartPage() {
  const { id = '' } = useParams();
  const client = useClient();
  const session = useSession();
  const navigate = useNavigate();
  const [params, setParams] = useSearchParams();
  const wasStarted = useRef(params.get('started') === '1').current;
  const startedHere = useRef(false);
  const polling = useRef(false);
  const mounted = useRef(true);
  const [phase, setPhase] = useState<WizardPhase>('loading');
  const [machine, setMachine] = useState<Machine | undefined>(undefined);
  const [problem, setProblem] = useState<string | undefined>(undefined);
  const [lost, setLost] = useState(false);
  const live = useLiveLog(PHASES);
  const on = (m: Machine | undefined) =>
    m
      ? {
          name: m.name,
          role: m.is_controller ? ('controller' as const) : ('agent' as const),
          os: m.os,
        }
      : undefined;
  /** One row per change of what the console sees on the machine; nothing is added when nothing changed. */
  const observe = (m: Machine) => {
    const c = restartOf(m);
    const machineSide = on(m);
    const note = (n: Note) =>
      live.say(
        n.machine ? n : { ...n, ...(machineSide ? { machine: machineSide } : {}) },
      );
    if (c?.state === 'needs_you')
      note({ at: 1, pct: null, line: 'The service could not restart.', error: true });
    else if (c?.state === 'waiting') {
      const n = c.waiting_for.length;
      note({
        at: 0,
        pct: null,
        reason:
          n > 0
            ? `${n} running ${n === 1 ? 'job' : 'jobs'} left; the time it takes is theirs.`
            : 'The running jobs are ending.',
        line: restartLine(m),
      });
    } else if (c)
      note({
        at: 1,
        pct: null,
        reason: 'The service reports no percent.',
        line: restartLine(m),
      });
    else if (m.online) note({ at: 2, pct: 100, line: 'The service is connected again.' });
    else
      note({
        at: 2,
        pct: null,
        reason: 'The service has not connected yet.',
        line: 'Waiting for the service to connect again',
      });
  };
  const admin = session.can('admin');
  const why = session.whyNot('admin');
  useEffect(() => {
    mounted.current = true;
    return () => {
      mounted.current = false;
    };
  }, []);

  const read = () =>
    unwrap(
      client.GET('/api/v1/machines/{machineId}', {
        params: { path: { machineId: id } },
      }),
    );

  const settle = (m: Machine, afterReload: boolean) => {
    setMachine(m);
    const c = restartOf(m);
    if (m.is_controller) setPhase('stopped');
    else if (c?.state === 'needs_you') {
      setProblem('The service could not restart. Resume the machine, then try again.');
      setPhase('failed');
    } else if (c) {
      if (afterReload || !startedHere.current) {
        setPhase('paused');
      } else setPhase('running');
    } else if (wasStarted && m.online)
      setPhase(afterReload ? 'done-after-reload' : 'done');
    else if (wasStarted) setPhase('paused');
    else setPhase(m.online ? 'ready-to-review' : 'first-run');
  };

  useEffect(() => {
    let on = true;
    read()
      .then((m) => on && settle(m, true))
      .catch((e: unknown) => {
        if (!on) return;
        setProblem(e instanceof Error ? e.message : 'The console did not answer.');
        setPhase('failed');
      });
    return () => {
      on = false;
    };
    // The stored run is read once, when the page opens.
  }, [client, id]);

  const follow = async () => {
    if (polling.current) return;
    polling.current = true;
    setPhase('running');
    let since: number | undefined; // when the service began to restart
    while (mounted.current) {
      await new Promise((r) => setTimeout(r, TIMING.planPollMs));
      if (!mounted.current) break;
      try {
        const m = await read();
        setMachine(m);
        observe(m);
        setLost(false);
        const c = restartOf(m);
        if (c?.state === 'needs_you') {
          setProblem(
            'The service could not restart. Resume the machine, then try again.',
          );
          setPhase('failed');
          break;
        }
        if (!c && m.online) {
          setPhase('done');
          break;
        }
        if (c?.state !== 'waiting') since ??= Date.now();
      } catch {
        setLost(true);
      }
      if (since !== undefined && Date.now() - since > TIMING.restartLimitMs) {
        setProblem(
          `The service did not come back within ${Math.round(TIMING.restartLimitMs / 60000)} minutes.`,
        );
        setPhase('failed');
        break;
      }
    }
    polling.current = false;
  };

  const start = async () => {
    setPhase('running');
    live.clear();
    try {
      live.say({
        at: 0,
        pct: null,
        reason: 'Asking the console to restart the service.',
        command: `Restart the service of ${machine?.name ?? 'the machine'}`,
      });
      await unwrap(
        client.POST('/api/v1/machines/{machineId}/restart', {
          params: { path: { machineId: id }, query: { confirmed: true } },
        }),
      );
      startedHere.current = true;
      live.say({ at: 0, pct: null, line: 'Accepted. No new jobs are taken.' });
      const next = new URLSearchParams(params);
      next.set('started', '1');
      setParams(next, { replace: true });
      void follow();
    } catch (e) {
      setProblem(e instanceof Error ? e.message : 'Could not start.');
      setPhase('failed');
    }
  };

  const startAgain = () => {
    startedHere.current = false;
    setParams(new URLSearchParams(), { replace: true });
    setProblem(undefined);
    setPhase('loading');
    read()
      .then((m) => settle(m, false))
      .catch((e: unknown) => {
        setProblem(e instanceof Error ? e.message : 'The console did not answer.');
        setPhase('failed');
      });
  };

  const name = machine?.name ?? 'this machine';
  const jobs = machine ? jobWords(machine) : [];
  const minutes = machine?.settings.drain_timeout_minutes;
  const toMachine = () => void navigate(`/setup/machines/${id}`);
  const done = phase === 'done' || phase === 'done-after-reload';

  let body;
  if (phase === 'first-run') {
    body = (
      <Message tone="needs-you">
        {`${name} is offline. Its service can restart only while it is connected. Try again when it is back.`}
      </Message>
    );
  } else if (phase === 'planning') {
    body = (
      <Message tone="note" dismissible={false}>
        {`Looking at ${name}.`}
      </Message>
    );
  } else if (phase === 'stopped') {
    body = (
      <Message tone="note" dismissible={false}>
        {`Stopped. Nothing changed: ${name} is the controller. Its service restarts from Settings, Updates.`}
      </Message>
    );
  } else if (phase === 'ready-to-review') {
    body = (
      <Stack>
        <Text>
          {`Restart the service of ${name}. This machine takes no new jobs, waits for the running jobs to end${minutes ? ` (up to ${minutes} minutes)` : ''}, then restarts its service. No running job is stopped.`}
        </Text>
        {jobs.length > 0 ? (
          <Stack>
            <Text>These jobs run now and are waited for:</Text>
            <BulletList items={jobs} />
          </Stack>
        ) : (
          <Message tone="note">No job runs on this machine now.</Message>
        )}
      </Stack>
    );
  } else if (phase === 'running' || phase === 'paused') {
    body = (
      <Stack>
        {phase === 'paused' ? (
          <Message tone="needs-you">
            Paused. The page closed while the service restarted. It goes on without this
            page. Start again to follow it.
          </Message>
        ) : null}
        {lost ? (
          <Message tone="note">
            The console is not answering. This page waits for it.
          </Message>
        ) : null}
        <Facts
          items={[
            { name: 'State', value: restartLine(machine) },
            { name: 'Jobs still running', value: String(jobs.length) },
            ...jobs.map((j) => ({ name: j, value: 'running' })),
          ]}
        />
      </Stack>
    );
  } else if (phase === 'failed') {
    body = (
      <Message tone="problem">
        {`The restart did not finish. ${problem ?? ''} The service keeps running. Try again.`}
      </Message>
    );
  } else if (done) {
    body = (
      <Stack>
        <Message tone="done">{`The service of ${name} restarted.`}</Message>
        <Facts
          items={[
            ...(machine?.facts?.agent_version
              ? [{ name: 'Version', value: machine.facts.agent_version }]
              : []),
            { name: 'Jobs stopped', value: '0' },
          ]}
        />
      </Stack>
    );
  } else {
    body = null;
  }

  const primary =
    phase === 'first-run'
      ? {
          label: 'Try again',
          busyLabel: 'Looking',
          onPress: () => {
            setPhase('planning');
            read()
              .then((m) => settle(m, false))
              .catch((e: unknown) => {
                setProblem(
                  e instanceof Error ? e.message : 'The console did not answer.',
                );
                setPhase('failed');
              });
          },
        }
      : {
          label: 'Restart the service',
          busyLabel: 'Restarting',
          onPress: () => void start(),
          disabled: !admin,
          reason: why,
        };

  return (
    <Page>
      <Breadcrumbs
        trail={[
          { to: '/setup/machines', label: 'Machines' },
          { to: `/setup/machines/${id}`, label: machine?.name ?? 'Machine' },
          { label: 'Restart the service' },
        ]}
      />
      <PageHeader title="Restart the service" />
      <Wizard
        name="Restart the service"
        steps={STEPS}
        current={
          phase === 'running' || phase === 'paused' || phase === 'failed'
            ? 1
            : done
              ? 2
              : 0
        }
        firstStep={0}
        phase={phase}
        reading="Reading the machine"
        live={live.events}
        progress={
          phase === 'running'
            ? {
                text: `${restartLine(machine)}${jobs.length ? `: ${jobs.length} ${jobs.length === 1 ? 'job' : 'jobs'} left` : ''}`,
              }
            : undefined
        }
        onBack={toMachine}
        primary={primary}
        review={phase === 'ready-to-review'}
        onStartAgain={startAgain}
        onTryAgain={startAgain}
        donePrimary={{ label: 'Open the machine', onPress: toMachine }}
      >
        {body}
      </Wizard>
    </Page>
  );
}
