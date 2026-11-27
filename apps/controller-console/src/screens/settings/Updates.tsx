// Settings, Updates (operations R5.11 to R5.18, tray R5.15 to R5.18): the version, the newest stable release,
// the Automatic updates switch (saved at once, UX-22), and the Review step that asks the yes before an update
// or a restart. Nothing here stops a running job: the machine drains first.
import {
  Breadcrumbs,
  BulletList,
  Facts,
  Message,
  Page,
  PageHeader,
  Stack,
  Text,
  Wizard,
  formatDateTime,
  type WizardPhase,
} from '@crsp/ui';
import { useEffect, useRef, useState } from 'react';
import { useNavigate, useSearchParams } from 'react-router-dom';
import { unwrap, useClient, type Schemas } from '../../api/api';
import { TIMING } from '../../timing';
import { useLiveLog } from '../liveLog';
import { settingsPath } from './sections';

type UpdateStatus = Schemas['UpdateStatus'];
type UpdateStage = NonNullable<UpdateStatus['stage']>;
type UpdateJob = Schemas['UpdateJob'];

const IN_PROGRESS: readonly UpdateStatus['state'][] = [
  'waiting_for_jobs',
  'downloading',
  'installing',
  'restarting',
];

// The compact update page (TRAY4): one step per stage of the update, in the order the service goes through them.
const COMPACT_STEPS = [
  'Review',
  'Downloading',
  'Waiting for jobs',
  'Installing',
  'Restarting',
  'Done',
] as const;
const STAGE_STEP: Record<UpdateStage['name'], number> = {
  downloading: 1,
  waiting_for_jobs: 2,
  installing: 3,
  waiting_installer: 3,
  restarting: 4,
  done: 5,
  failed: 1,
};
const STATE_STEP: Partial<Record<UpdateStatus['state'], number>> = {
  downloading: 1,
  waiting_for_jobs: 2,
  installing: 3,
  restarting: 4,
};
/** A stage this old is a leftover, not a running update. */
const STAGE_FRESH_MS = 45 * 60 * 1000;
const stageLive = (u: UpdateStatus): boolean =>
  u.stage !== undefined &&
  u.stage.name !== 'done' &&
  u.stage.name !== 'failed' &&
  Date.now() - new Date(u.stage.at).getTime() < STAGE_FRESH_MS;
const stageRecentDone = (u: UpdateStatus): boolean =>
  u.stage?.name === 'done' &&
  u.stage.version === u.current_version &&
  Date.now() - new Date(u.stage.at).getTime() < STAGE_FRESH_MS;

const STAGE_WORDS: Record<UpdateStage['name'], string> = {
  downloading: 'Downloading',
  waiting_for_jobs: 'Waiting for running jobs to end',
  installing: 'Installing',
  waiting_installer: 'Waiting for another installer',
  restarting: 'Restarting the service',
  done: 'Done',
  failed: 'Failed',
};

/** The live line of the work step: the stage in words and its percent when it has one. */
export function stageLine(u: UpdateStatus): string {
  const word = u.stage && stageLive(u) ? STAGE_WORDS[u.stage.name] : stateWord(u);
  const pc = u.stage?.percent;
  return pc === undefined ? word : `${word} ${pc}%`;
}

/** A reason as a sentence: it ends with a full stop. */
const sentence = (t: string): string => (t === '' || /[.!?]$/.test(t) ? t : `${t}.`);

export function jobLine(j: UpdateJob): string {
  return `${j.repository}${j.workflow ? ` ${j.workflow}` : ''}, run ${j.run_id}, job ${j.job}`;
}

const numbers = (v: string | undefined): number[] | null => {
  const m = /^v?(\d+)\.(\d+)\.(\d+)$/.exec((v ?? '').trim());
  return m ? [Number(m[1]), Number(m[2]), Number(m[3])] : null;
};

/** A release is offered only when it is newer than the running version, whatever the flag says. */
export function offered(u: UpdateStatus): boolean {
  if (!u.update_available || !u.latest_version) return false;
  const cur = numbers(u.current_version);
  const next = numbers(u.latest_version);
  if (!cur || !next) return u.update_available;
  for (let i = 0; i < 3; i += 1) {
    if (next[i] !== cur[i]) return next[i]! > cur[i]!;
  }
  return false;
}

// The idle state is two real cases; say the one that is true (a newer release waits, or nothing is newer).
function idleWord(s: UpdateStatus): string {
  if (offered(s))
    return `Version ${s.latest_version ?? ''} is waiting for your yes`.replace('  ', ' ');
  return s.latest_version ? 'Up to date' : 'Not looked for yet';
}

const STATE_WORDS: Record<Exclude<UpdateStatus['state'], 'idle'>, string> = {
  checking: 'Looking for updates',
  waiting_for_jobs: 'Waiting for running jobs to end',
  downloading: 'Downloading and checking the release',
  installing: 'Installing',
  restarting: 'Restarting the service',
  failed: 'Failed',
};

function stateWord(s: UpdateStatus): string {
  return s.state === 'idle' ? idleWord(s) : STATE_WORDS[s.state];
}

// ------------------------------------------------------------------------------------------ the Review step

type Kind = 'update' | 'restart';

// The weighted bar of the work step (UX-88), one list per kind: what the service goes through, in order.
const UPDATE_PHASES = [
  { name: 'Waiting for running jobs to end', weight: 2 },
  { name: 'Downloading the release', weight: 3 },
  { name: 'Installing', weight: 2 },
  { name: 'Restarting the service', weight: 1 },
] as const;
const RESTART_PHASES = [
  { name: 'Waiting for running jobs to end', weight: 2 },
  { name: 'Restarting the service', weight: 1 },
  { name: 'Connecting again', weight: 1 },
] as const;
const UPDATE_AT: Partial<Record<UpdateStatus['state'], number>> = {
  waiting_for_jobs: 0,
  downloading: 1,
  installing: 2,
  restarting: 3,
};
const RESTART_AT: Partial<Record<UpdateStatus['state'], number>> = {
  waiting_for_jobs: 0,
  installing: 1,
  restarting: 1,
};

/** How long a restart took, in words: "40 seconds", "1 minute 24 seconds". */
export function took(ms: number): string {
  const total = Math.max(1, Math.round(ms / 1000));
  const min = Math.floor(total / 60);
  const sec = total % 60;
  const secs = `${sec} ${sec === 1 ? 'second' : 'seconds'}`;
  if (min === 0) return secs;
  const mins = `${min} ${min === 1 ? 'minute' : 'minutes'}`;
  return sec === 0 ? mins : `${mins} ${secs}`;
}

/** The Review step of an update or a restart: reads, Review, the work (drain, install), Done. */
export function UpdateWizard({ compact = false }: { compact?: boolean } = {}) {
  const client = useClient();
  const [params, setParams] = useSearchParams();
  const kind: Kind = params.get('kind') === 'restart' ? 'restart' : 'update';
  // Set once the yes is given, so a reload can tell a stored run from a fresh page.
  const startedTo = useRef(params.get('to')).current;
  const wasStarted = useRef(params.get('started') === '1').current;
  // The attempt this page started (CRSP205): only its outcome counts. An older update's failure never shows here.
  const attemptId = useRef(params.get('attempt') ?? undefined);
  const startedHere = useRef(false);
  const tookMs = useRef<number | undefined>(undefined);
  const startedAt = useRef<string | undefined>(undefined);
  const mine = (u: UpdateStatus) =>
    u.state === 'failed' &&
    (attemptId.current
      ? u.error_attempt === attemptId.current
      : startedHere.current && u.error_attempt !== 'earlier');
  // A restart is over when the service runs as a new process, started after the yes of this attempt.
  const came = (u: UpdateStatus) =>
    !!u.attempt &&
    u.attempt.id === attemptId.current &&
    !!u.service_started_at &&
    Date.parse(u.service_started_at) > Date.parse(u.attempt.started_at);
  const measure = (u: UpdateStatus) => {
    if (!u.attempt) return;
    const doneAt = u.stage?.name === 'done' ? Date.parse(u.stage.at) : NaN;
    const end =
      doneAt >= Date.parse(u.attempt.started_at)
        ? doneAt
        : Date.parse(u.server_time ?? '');
    const took = end - Date.parse(u.attempt.started_at);
    tookMs.current = Number.isFinite(took) && took >= 0 ? took : undefined;
  };
  const navigate = useNavigate();
  const live = useLiveLog(kind === 'update' ? UPDATE_PHASES : RESTART_PHASES);
  const SERVICE = { name: 'The service', role: 'controller' as const, os: '' };
  /** One row per change of what the service reports; the percent only when the service sends one. */
  const observe = (u: UpdateStatus, over?: 'done' | 'away') => {
    const table = kind === 'update' ? UPDATE_AT : RESTART_AT;
    const last = (kind === 'update' ? UPDATE_PHASES : RESTART_PHASES).length - 1;
    const machine = SERVICE;
    if (over === 'done')
      return live.say({
        at: last,
        pct: 100,
        line:
          kind === 'update'
            ? `Version ${u.current_version} answers.`
            : 'The service answers again.',
        machine,
      });
    if (over === 'away')
      return live.say({
        at: last,
        pct: null,
        reason: 'The service reports no percent while it restarts.',
        line: 'The service is restarting. Waiting for it to answer.',
        machine,
      });
    const at = table[u.state] ?? (u.state === 'idle' ? 0 : undefined);
    if (at === undefined) return;
    const pct = u.state === 'downloading' ? (u.stage?.percent ?? null) : null;
    live.say({
      at,
      pct,
      ...(pct === null ? { reason: 'The service reports no percent here.' } : {}),
      line: u.state === 'idle' ? 'Starting' : (STATE_WORDS[u.state] ?? stateWord(u)),
      machine,
    });
  };

  const [phase, setPhase] = useState<WizardPhase>('loading');
  const [status, setStatus] = useState<UpdateStatus | undefined>(undefined);
  const [problem, setProblem] = useState<string | undefined>(undefined);
  const [lostService, setLostService] = useState(false);
  const polling = useRef(false);
  const mounted = useRef(true);
  useEffect(() => {
    mounted.current = true;
    return () => {
      mounted.current = false;
    };
  }, []);

  const working = kind === 'update' ? 'Updating' : 'Restarting';
  const STEPS =
    compact && kind === 'update' ? COMPACT_STEPS : (['Review', working, 'Done'] as const);
  // The compact page marks a failure at the step it reached.
  const reached = useRef(1);

  const settle = (u: UpdateStatus, afterReload: boolean) => {
    setStatus(u);
    const live = IN_PROGRESS.includes(u.state) || (compact && stageLive(u));
    if (live && compact && !wasStarted) {
      // The tray opened this page on an update that already runs: follow it, there is nothing to start again.
      setPhase('running');
      void follow(u.latest_version ?? u.stage?.version);
    } else if (live) {
      setPhase(afterReload ? 'paused' : 'running');
    } else if (u.state === 'failed' && (compact || mine(u))) {
      setProblem(u.error ?? 'The service did not say why.');
      setPhase('failed');
    } else if (wasStarted && kind === 'restart') {
      if (came(u)) {
        measure(u);
        setPhase(afterReload ? 'done-after-reload' : 'done');
      } else {
        // The restart of this page has not happened yet, or the page was reloaded while it runs: follow it.
        setPhase('paused');
      }
    } else if (wasStarted) {
      if (kind === 'update' && startedTo && u.current_version !== startedTo) {
        setProblem(`The service still runs ${u.current_version}, not ${startedTo}.`);
        setPhase('failed');
      } else setPhase(afterReload ? 'done-after-reload' : 'done');
    } else if (compact && stageRecentDone(u) && !offered(u)) {
      setPhase('done-after-reload');
    } else if (kind === 'update' && !offered(u)) {
      setPhase(u.latest_version ? 'stopped' : 'first-run');
    } else setPhase('ready-to-review');
  };

  useEffect(() => {
    let on = true;
    unwrap(client.GET('/api/v1/update'))
      .then((u) => on && settle(u, true))
      .catch((e: unknown) => {
        if (!on) return;
        setProblem(e instanceof Error ? e.message : 'The service did not answer.');
        setPhase('failed');
      });
    return () => {
      on = false;
    };
    // The stored run is read once, when the page opens.
  }, [client]);

  const follow = async (target: string | undefined) => {
    if (polling.current) return;
    polling.current = true;
    setPhase('running');
    let since: number | undefined; // when the service went away or began to restart
    while (mounted.current) {
      await new Promise((r) => setTimeout(r, TIMING.planPollMs));
      if (!mounted.current) break;
      try {
        const u = await unwrap(client.GET('/api/v1/update'));
        setStatus(u);
        observe(u);
        if (
          (compact ? u.state === 'failed' : mine(u)) ||
          (compact && u.stage?.name === 'failed' && !stageLive(u))
        ) {
          setProblem(u.error ?? u.stage?.error ?? 'The service did not say why.');
          setPhase('failed');
          break;
        }
        const finished =
          !IN_PROGRESS.includes(u.state) &&
          !(compact && stageLive(u)) &&
          (kind === 'update' ? u.current_version === target : came(u));
        if (finished) {
          if (kind === 'restart') measure(u);
          setLostService(false);
          observe(u, 'done');
          setPhase('done');
          break;
        }
        if (kind === 'restart' && (u.state === 'restarting' || u.state === 'installing'))
          since ??= Date.now();
        setLostService(false);
      } catch {
        // The service stops to install and comes back: keep following.
        setLostService(true);
        observe(
          status ?? ({ state: 'restarting', current_version: '' } as UpdateStatus),
          'away',
        );
        since ??= Date.now();
      }
      if (
        kind === 'restart' &&
        since !== undefined &&
        Date.now() - since > TIMING.restartLimitMs
      ) {
        const at = formatDateTime(startedAt.current ?? new Date().toISOString());
        setProblem(
          `The restart you started at ${at} did not finish: the service did not come back within ${Math.round(TIMING.restartLimitMs / 60000)} minutes.`,
        );
        setLostService(false);
        setPhase('failed');
        break;
      }
    }
    polling.current = false;
  };

  const start = async () => {
    setPhase('running');
    live.clear();
    live.say({
      at: 0,
      pct: null,
      reason: 'Asking the service to begin.',
      command:
        kind === 'update'
          ? `Update to ${status?.latest_version ?? 'the newest release'}`
          : 'Restart the service',
      machine: { name: 'This console', role: 'controller', os: '' },
    });
    try {
      const u = await unwrap(
        kind === 'update'
          ? client.POST('/api/v1/update/apply', {
              params: { query: { confirmed: true } },
            })
          : client.POST('/api/v1/service/restart', {
              params: { query: { confirmed: true } },
            }),
      );
      setStatus(u);
      observe(u);
      startedHere.current = true;
      attemptId.current = u.attempt?.id;
      startedAt.current = u.attempt?.started_at;
      const next = new URLSearchParams(params);
      next.set('started', '1');
      if (u.attempt?.id) next.set('attempt', u.attempt.id);
      if (kind === 'update' && u.latest_version) next.set('to', u.latest_version);
      setParams(next, { replace: true });
      void follow(u.latest_version);
    } catch (e) {
      setProblem(e instanceof Error ? e.message : 'Could not start.');
      setPhase('failed');
    }
  };

  const startAgain = () => {
    attemptId.current = undefined;
    startedHere.current = false;
    setParams(new URLSearchParams({ kind }), { replace: true });
    setProblem(undefined);
    setPhase('loading');
    unwrap(client.GET('/api/v1/update'))
      .then((u) => {
        setStatus(u);
        if (IN_PROGRESS.includes(u.state)) void follow(u.latest_version);
        else setPhase(kind === 'update' && !offered(u) ? 'stopped' : 'ready-to-review');
      })
      .catch((e: unknown) => {
        setProblem(e instanceof Error ? e.message : 'The service did not answer.');
        setPhase('failed');
      });
  };

  const jobs = status?.running_jobs ?? [];
  const minutes = status?.drain_timeout_minutes;
  const target = status?.latest_version ?? '';
  const doneStep = STEPS.length - 1;
  if (compact && status && (phase === 'running' || phase === 'paused')) {
    reached.current =
      (status.stage && STAGE_STEP[status.stage.name]) ??
      STATE_STEP[status.state] ??
      reached.current;
  }
  const failedAt = status?.stage?.failed_at;
  const workStep =
    compact && phase === 'failed' && failedAt
      ? STAGE_STEP[failedAt]
      : compact
        ? reached.current
        : 1;
  const step =
    phase === 'running' || phase === 'paused' || phase === 'failed'
      ? workStep
      : phase === 'done' || phase === 'done-after-reload'
        ? doneStep
        : 0;
  const close = () => {
    window.close();
    // A window that was not opened by a program stays open: go to the console instead.
    void navigate('/');
  };
  // While the work runs, an idle answer means the service has not started the step yet: never "Not looked for yet".
  const shown = (u: UpdateStatus) =>
    u.state === 'idle' && phase === 'running'
      ? kind === 'update'
        ? 'Starting the update'
        : 'Restarting the service'
      : compact
        ? stageLine(u)
        : stateWord(u);
  const isDone = phase === 'done' || phase === 'done-after-reload';

  let body;
  if (phase === 'first-run') {
    body = (
      <Message tone="needs-you">
        The service has not looked for a release yet. Look for updates to go on.
      </Message>
    );
  } else if (phase === 'planning') {
    body = (
      <Message tone="note" dismissible={false}>
        Looking for a newer stable release.
      </Message>
    );
  } else if (phase === 'stopped') {
    body = (
      <Message tone="note" dismissible={false}>
        Stopped. Nothing changed: {status?.current_version} is the newest stable release.
      </Message>
    );
  } else if (phase === 'ready-to-review') {
    body = (
      <Stack>
        {compact ? (
          <Text>
            {`Version ${target} is ready. Running jobs finish first; the service restarts (about 1 min).`}
          </Text>
        ) : (
          <Text>
            {kind === 'update'
              ? `Update ${status?.current_version} to ${target}. This machine takes no new jobs, waits for the running jobs to end${minutes ? ` (up to ${minutes} minutes)` : ''}, then installs and restarts the service. No running job is stopped.`
              : `Restart the service. This machine takes no new jobs, waits for the running jobs to end${minutes ? ` (up to ${minutes} minutes)` : ''}, then restarts. No running job is stopped.`}
          </Text>
        )}
        {compact ? null : jobs.length > 0 ? (
          <Stack>
            <Text>These jobs run now and are waited for:</Text>
            <BulletList items={jobs.map(jobLine)} />
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
            Paused. The page closed while the service{' '}
            {kind === 'update' ? 'updated' : 'restarted'}. It goes on without this page.
            Start again to follow it.
          </Message>
        ) : null}
        {lostService ? (
          <Message tone="note">
            The service is restarting. This page waits for it.
          </Message>
        ) : null}
        {compact ? (
          <Text>Running jobs are safe.</Text>
        ) : (
          <Facts
            items={[
              { name: 'State', value: status ? shown(status) : 'Starting' },
              { name: 'Jobs still running', value: String(jobs.length) },
              ...jobs.map((j) => ({ name: jobLine(j), value: 'running' })),
            ]}
          />
        )}
      </Stack>
    );
  } else if (phase === 'failed') {
    body = (
      <Message tone="problem">
        {kind === 'update' ? 'The update did not finish.' : 'The restart did not finish.'}{' '}
        {sentence(problem ?? '')} The running version stays.{' '}
        {compact && status?.stage?.log_path
          ? `The log is ${status.stage.log_path}. Open the log folder from the tray menu. Then try again.`
          : 'Try again.'}
      </Message>
    );
  } else if (phase === 'done' || phase === 'done-after-reload') {
    body = (
      <Stack>
        <Message tone="done">
          {kind === 'update'
            ? compact
              ? `Updated to ${status?.current_version}.`
              : `The service runs ${status?.current_version}.`
            : 'The service restarted.'}
        </Message>
        <Facts
          items={[
            { name: 'Version', value: status?.current_version ?? '' },
            ...(kind === 'restart' && tookMs.current !== undefined
              ? [{ name: 'Took', value: took(tookMs.current) }]
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
          label: 'Look for updates',
          busyLabel: 'Looking',
          onPress: () => {
            setPhase('planning');
            unwrap(client.POST('/api/v1/update/check'))
              .then((u) => settle(u, false))
              .catch((e: unknown) => {
                setProblem(e instanceof Error ? e.message : 'Could not look.');
                setPhase('failed');
              });
          },
        }
      : {
          label: compact
            ? 'Update now'
            : kind === 'update'
              ? `Update to ${target}`
              : 'Restart the service',
          busyLabel: working,
          onPress: () => void start(),
        };

  return (
    <Wizard
      name={kind === 'update' ? 'Update' : 'Restart the service'}
      steps={STEPS}
      current={step}
      firstStep={0}
      phase={phase}
      reading="Reading the service"
      live={compact ? undefined : live.events}
      progress={
        phase === 'running'
          ? {
              text: status
                ? `${shown(status)}${jobs.length ? `: ${jobs.length} ${jobs.length === 1 ? 'job' : 'jobs'} left` : ''}`
                : 'Starting',
              ...(compact && status?.stage?.percent !== undefined
                ? { value: status.stage.percent, max: 100 }
                : {}),
            }
          : undefined
      }
      onBack={compact && !isDone ? close : () => void navigate(settingsPath('updates'))}
      {...(compact
        ? {
            backLabel: isDone
              ? 'Open Updates'
              : ['failed', 'paused', 'stopped'].includes(phase)
                ? 'Close'
                : 'Not now',
            hide: { label: 'Hide', onPress: close },
          }
        : {})}
      primary={primary}
      review={phase === 'ready-to-review'}
      onStartAgain={startAgain}
      onTryAgain={startAgain}
      donePrimary={
        compact
          ? { label: 'Close', onPress: close }
          : {
              label: 'Open Updates',
              onPress: () => void navigate(settingsPath('updates')),
            }
      }
    >
      {body}
    </Wizard>
  );
}

/** The small window the tray opens (TRAY4): the same update steps, no shell around them, in the console theme. */
export function UpdateCompactPage() {
  return (
    <main>
      <Page frame="compact">
        <PageHeader title="Update" />
        <UpdateWizard compact />
      </Page>
    </main>
  );
}

export function UpdateReviewPage() {
  const [params] = useSearchParams();
  const restart = params.get('kind') === 'restart';
  return (
    <Page>
      <Breadcrumbs
        trail={[{ to: settingsPath('updates'), label: 'Updates' }, { label: 'Review' }]}
      />
      <PageHeader title={restart ? 'Restart the service' : 'Update'} />
      <UpdateWizard />
    </Page>
  );
}
