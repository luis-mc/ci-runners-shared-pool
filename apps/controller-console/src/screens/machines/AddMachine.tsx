// The "Add a machine" wizard (web-console 4.1 and 4.4, R5.17). Steps: Machine, Review, Joining, Done.
// Its behaviour comes from the shared wizard (UX-54); this page passes data only. The phase is read from the
// server: a stored join code that is still waiting with no live wait here reads Paused (UX-30).
import { ReadyDoor, needsEngine } from '../dependencies/ReadyDoor';
import {
  Breadcrumbs,
  CodeBlock,
  Page,
  PageHeader,
  Pane,
  ErrorSummary,
  Facts,
  LinkButton,
  Message,
  ChoiceField,
  NumberField,
  Status,
  TextAreaField,
  TextField,
  formatTimeLeft,
  formatNumber,
  formatSize,
  Text,
  Stack,
  Wizard,
  useNow,
  type WizardPhase,
} from '@crsp/ui';
import { useCallback, useEffect, useRef, useState } from 'react';
import { useNavigate, useSearchParams } from 'react-router-dom';
import { ApiError, unwrap, useClient, type Schemas } from '../../api/api';
import { fileNameOf, saveBlob } from '../../api/download';
import { Ask } from '../common';
import { TIMING } from '../../timing';

type JoinCode = Schemas['JoinCode'];
type Machine = Schemas['Machine'];
type OS = Schemas['OS'];
type Progress = NonNullable<JoinCode['progress']>;
type Method = 'copy' | 'ssh' | 'download';

const STEPS = ['Machine', 'Review', 'Joining', 'Done'] as const;
const TTL_MINUTES = 15;

/** RES3: the three live steps of a join, in order, and the words the page says for each. */
export const PROGRESS_STEPS: readonly { id: Progress; name: string }[] = [
  { id: 'waiting', name: 'Waiting for the agent' },
  { id: 'connected', name: 'Agent connected' },
  { id: 'ready', name: 'Machine ready' },
];

/** A controller that sends no progress for a joined code is an older one: it counts as ready. */
export function progressOf(join: JoinCode): Progress {
  if (join.progress) return join.progress;
  return join.state === 'joined' ? 'ready' : 'waiting';
}

function ProgressSteps({ at }: { at: Progress }) {
  const index = PROGRESS_STEPS.findIndex((p) => p.id === at);
  return (
    <Facts
      items={PROGRESS_STEPS.map((p, i) => ({
        name: p.name,
        value:
          i < index || at === 'ready' ? (
            <Status kind="ok" word="done" />
          ) : i === index ? (
            <Status kind="running" word="in progress" />
          ) : (
            <Status kind="off" word="not yet" />
          ),
      }))}
    />
  );
}

/** What to do with the downloaded file on the machine, per system (machines R5.65). */
export function downloadWords(os: OS, name: string): string {
  const saved = `The installer is saved as ${name}. It holds a one-time join code, valid ${TTL_MINUTES} minutes. Delete the file after you use it.`;
  if (os === 'windows')
    return `${saved} On the machine, double-click it and accept the administrator prompt. Waiting for the agent.`;
  if (os === 'macos')
    return `${saved} On the machine, open it. If it does not open, run it with sh in Terminal. Waiting for the agent.`;
  return `${saved} On the machine, run it with sh. It asks for sudo. Waiting for the agent.`;
}

interface Props {
  /** The next action on Done, when the page has one (the first-run checklist goes on). */
  onDone?: (machine: Machine | undefined) => void;
}

export function AddMachineWizard({ onDone }: Props) {
  const client = useClient();
  const navigate = useNavigate();
  const now = useNow();
  const [params, setParams] = useSearchParams();
  const storedJoin = useRef(params.get('join')).current;

  const [phase, setPhase] = useState<WizardPhase>('loading');
  const [step, setStep] = useState(0);
  const [os, setOs] = useState<OS>('linux');
  const [command, setCommand] = useState<string | undefined>(undefined);
  const [created, setCreated] = useState<
    { code: string; join: JoinCode; command: string } | undefined
  >(undefined);
  const [problem, setProblem] = useState<string | undefined>(undefined);
  const [machine, setMachine] = useState<Machine | undefined>(undefined);
  const [stopping, setStopping] = useState(false);
  const [method, setMethod] = useState<Method>('copy');
  const [host, setHost] = useState('');
  const [user, setUser] = useState('');
  const [port, setPort] = useState(22);
  const [identity, setIdentity] = useState('');
  const [progress, setProgress] = useState<Progress>('waiting');
  const [hostKey, setHostKey] = useState<string | undefined>(undefined);
  const [sshError, setSshError] = useState<string | undefined>(undefined);
  const [downloadError, setDownloadError] = useState<string | undefined>(undefined);
  const [fileName, setFileName] = useState<string | undefined>(undefined);
  const [errors, setErrors] = useState<string[]>([]);
  const known = useRef<Set<string>>(new Set());
  const live = useRef(false);

  const finish = useCallback(
    async (join: JoinCode, afterReload: boolean) => {
      if (join.machine_id) {
        try {
          setMachine(
            await unwrap(
              client.GET('/api/v1/machines/{machineId}', {
                params: { path: { machineId: join.machine_id } },
              }),
            ),
          );
        } catch {
          setMachine(undefined);
        }
      }
      setStep(3);
      setPhase(afterReload ? 'done-after-reload' : 'done');
    },
    [client],
  );

  // Reading the controller: the machines that exist now, and the join code a reload left behind.
  useEffect(() => {
    let on = true;
    void (async () => {
      try {
        const list = await unwrap(client.GET('/api/v1/machines'));
        if (!on) return;
        known.current = new Set(list.items.map((m) => m.id));
        if (storedJoin) {
          const join = await unwrap(
            client.GET('/api/v1/join-codes/{joinCodeId}', {
              params: { path: { joinCodeId: storedJoin } },
            }),
          );
          if (!on) return;
          if (join.state === 'joined') await finish(join, true);
          else if (join.state === 'waiting') {
            setStep(2);
            setPhase('paused');
          } else if (join.state === 'revoked') {
            setStep(3);
            setPhase('stopped');
          } else {
            setStep(3);
            setProblem('The join code ran out before the agent joined.');
            setPhase('failed');
          }
          return;
        }
        setPhase('first-run');
      } catch (e) {
        if (!on) return;
        setProblem(e instanceof Error ? e.message : 'The controller did not answer.');
        setPhase('failed');
      }
    })();
    return () => {
      on = false;
    };
  }, [client, storedJoin, finish]);

  // Planning: the install command for the chosen system.
  const toReview = async () => {
    setStep(1);
    setPhase('planning');
    try {
      const cmds = await unwrap(client.GET('/api/v1/install-command'));
      setCommand(cmds.items.find((c) => c.os === os)?.command ?? '');
      setPhase('ready-to-review');
    } catch (e) {
      setProblem(e instanceof Error ? e.message : 'The controller did not answer.');
      setPhase('failed');
    }
  };

  // Joining: make the one-time code, then wait for the agent. The code never goes in a URL (R6.4).
  // Over SSH the controller runs the command itself; the key text is sent once and dropped here (RES3).
  const startJoining = async (confirmedHostKey?: string) => {
    setStep(2);
    setPhase('running');
    setProgress('waiting');
    setSshError(undefined);
    setDownloadError(undefined);
    live.current = true;
    try {
      if (method === 'download') {
        // The file is made only now, by this press: it mints the one-time code (machines R5.65).
        const res = await client.POST('/api/v1/join-files', {
          body: { os, ttl_minutes: TTL_MINUTES },
          parseAs: 'blob',
        });
        if (!res.response.ok) {
          const e = (
            res.error as { error?: { code?: string; message?: string } } | undefined
          )?.error;
          throw new ApiError(
            e?.code ?? 'internal',
            e?.message ?? `The controller answered ${res.response.status}.`,
            res.response.status,
          );
        }
        const id = res.response.headers.get('X-Join-Code-Id') ?? '';
        const name = fileNameOf(
          res.response.headers.get('Content-Disposition'),
          'crsp-join',
        );
        const join = await unwrap(
          client.GET('/api/v1/join-codes/{joinCodeId}', {
            params: { path: { joinCodeId: id } },
          }),
        );
        saveBlob(res.data instanceof Blob ? res.data : new Blob([]), name);
        setFileName(name);
        setCreated({ code: '', join, command: '' });
        const next = new URLSearchParams(params);
        next.set('join', join.id);
        setParams(next, { replace: true });
        return;
      }
      if (method === 'ssh') {
        const join = await unwrap(
          client.POST('/api/v1/join-codes/ssh', {
            body: {
              os,
              host: host.trim(),
              port,
              user: user.trim(),
              identity,
              ...(confirmedHostKey ? { host_fingerprint: confirmedHostKey } : {}),
            },
          }),
        );
        setIdentity('');
        setHostKey(undefined);
        setCreated({ code: '', join, command: '' });
        const next = new URLSearchParams(params);
        next.set('join', join.id);
        setParams(next, { replace: true });
        return;
      }
      const made = await unwrap(
        client.POST('/api/v1/join-codes', { body: { ttl_minutes: TTL_MINUTES } }),
      );
      const forOs =
        made.commands.items.find((c) => c.os === os)?.command ?? command ?? '';
      setCreated({ code: made.code, join: made.join_code, command: forOs });
      const next = new URLSearchParams(params);
      next.set('join', made.join_code.id);
      setParams(next, { replace: true });
    } catch (e) {
      live.current = false;
      if (e instanceof ApiError && e.code === 'needs_confirmation') {
        // The host key is asked once; nothing ran yet (UX-9).
        setHostKey(e.details[0]?.text ?? '');
        setStep(1);
        setPhase('ready-to-review');
        return;
      }
      if (
        method === 'download' &&
        e instanceof ApiError &&
        (e.status < 500 || e.status === 501)
      ) {
        setDownloadError(e.message);
        setStep(1);
        setPhase('ready-to-review');
        return;
      }
      if (method === 'ssh' && e instanceof ApiError && e.status < 500) {
        setSshError(e.message);
        setStep(1);
        setPhase('ready-to-review');
        return;
      }
      setProblem(e instanceof Error ? e.message : 'Could not make a join code.');
      setStep(3);
      setPhase('failed');
    }
  };

  useEffect(() => {
    if (phase !== 'running' || !created) return;
    const id = created.join.id;
    const timer = setInterval(() => {
      void unwrap(
        client.GET('/api/v1/join-codes/{joinCodeId}', {
          params: { path: { joinCodeId: id } },
        }),
      )
        .then(async (join) => {
          if (!live.current) return;
          setProgress(progressOf(join));
          if (join.state === 'joined' && progressOf(join) === 'ready') {
            live.current = false;
            await finish(join, false);
            if (onDone) onDone(undefined);
          } else if (join.state === 'expired') {
            live.current = false;
            setProblem('The join code ran out before the agent joined.');
            setStep(3);
            setPhase('failed');
          } else if (join.state === 'revoked') {
            live.current = false;
            setStep(3);
            setPhase('stopped');
          }
        })
        .catch(() => undefined);
    }, TIMING.joinPollMs);
    return () => clearInterval(timer);
  }, [phase, created, client, finish, onDone]);

  const stop = async () => {
    if (!created) return;
    setStopping(true);
    live.current = false;
    try {
      await unwrap(
        client.DELETE('/api/v1/join-codes/{joinCodeId}', {
          params: { path: { joinCodeId: created.join.id } },
        }),
      );
    } catch {
      // The code may already be gone; the wizard still ends at Done.
    }
    setStopping(false);
    setStep(3);
    setPhase('stopped');
  };

  const startAgain = () => {
    const next = new URLSearchParams(params);
    next.delete('join');
    setParams(next, { replace: true });
    setCreated(undefined);
    setCommand(undefined);
    setMachine(undefined);
    setProblem(undefined);
    setProgress('waiting');
    setHostKey(undefined);
    setSshError(undefined);
    setDownloadError(undefined);
    setFileName(undefined);
    setIdentity('');
    setStep(0);
    setPhase('first-run');
  };

  const next = async () => {
    if (method === 'ssh') {
      const problems: string[] = [];
      if (!host.trim()) problems.push('Enter the host name or address.');
      if (!user.trim()) problems.push('Enter the user name.');
      if (!identity.trim()) problems.push('Paste the SSH key file.');
      setErrors(problems);
      if (problems.length) return;
    }
    await toReview();
  };
  const usingSsh = method === 'ssh';
  let body;
  if (step === 0) {
    body = (
      <Stack>
        <ChoiceField
          label="Operating system of the machine"
          help="The machine runs the agent. Pick the system it has."
          value={os}
          onChange={(v) => {
            setOs(v as OS);
          }}
          choices={[
            { value: 'linux', label: 'Linux' },
            { value: 'windows', label: 'Windows' },
            { value: 'macos', label: 'macOS' },
          ]}
        />
        <ChoiceField
          label="How the machine joins"
          help="Copy one command and run it on the machine, let crsp sign in over SSH once, or download a file to open on the machine."
          value={method}
          onChange={(v) => setMethod(v as Method)}
          choices={[
            { value: 'copy', label: 'Copy a command' },
            { value: 'ssh', label: 'I have SSH' },
            { value: 'download', label: 'Download an installer' },
          ]}
        />
        {os === 'windows' && method !== 'download' ? (
          <Message tone="note">
            {method === 'ssh'
              ? 'A Windows machine needs the OpenSSH server, and the user must be an administrator.'
              : 'Run the command in an administrator terminal.'}
          </Message>
        ) : null}
        {usingSsh ? (
          <Stack>
            <ErrorSummary errors={errors} />
            <TextField label="Host" value={host} onChange={setHost} width="full" />
            <TextField label="User" value={user} onChange={setUser} />
            <NumberField
              label="SSH port"
              value={port}
              min={1}
              max={65535}
              onChange={setPort}
            />
            <TextAreaField
              label="SSH key file"
              help={
                os === 'windows'
                  ? 'Paste the content of the private key file. crsp uses it once for this join and never keeps it. There is no password option; the user must be an administrator.'
                  : 'Paste the content of the private key file. crsp uses it once for this join and never keeps it. There is no password option; the user needs sudo without a password.'
              }
              value={identity}
              onChange={setIdentity}
              rows={6}
              mono
              width="full"
            />
          </Stack>
        ) : null}
      </Stack>
    );
  } else if (step === 1) {
    body = (
      <Stack>
        <Facts
          items={[
            { name: 'System', value: os },
            {
              name: 'How it joins',
              value: usingSsh
                ? `Over SSH as ${user} on ${host}`
                : method === 'download'
                  ? 'A downloaded installer'
                  : 'A copied command',
            },
            { name: 'Join code', value: `Lasts ${TTL_MINUTES} minutes and works once` },
          ]}
        />
        <Text>
          {usingSsh
            ? 'crsp signs in once with your key, runs the install command and signs out. The key is not stored.'
            : method === 'download'
              ? 'Next you get an installer file for this system with the join code in it. Open it on the machine, then delete the file.'
              : 'Next you get one command with the join code in it. Run it on the machine.'}
        </Text>
        {sshError ? (
          <Message tone="problem">Could not join over SSH. {sshError}</Message>
        ) : null}
        {downloadError ? (
          <Message tone="problem">Could not make the installer. {downloadError}</Message>
        ) : null}
      </Stack>
    );
  } else if (step === 2) {
    const left = created ? Date.parse(created.join.expires_at) - now : undefined;
    body = (
      <Stack>
        {phase === 'paused' ? (
          <Message tone="needs-you">
            Paused. The wait for the agent ended when this page closed. Start again to
            make a new join code.
          </Message>
        ) : null}
        {created ? (
          <>
            {created.command ? (
              <>
                <Text>Run this command on the machine now. The code shows once.</Text>
                <CodeBlock label="Join command" text={created.command} />
              </>
            ) : fileName ? (
              <Text>{downloadWords(os, fileName)}</Text>
            ) : (
              <Text>crsp ran the install command over SSH. Waiting for the agent.</Text>
            )}
            <ProgressSteps at={progress} />
            <Facts
              items={[
                {
                  name: 'Time left',
                  value: left === undefined ? '' : formatTimeLeft(left),
                },
              ]}
            />
          </>
        ) : (
          <Text>Making the join code.</Text>
        )}
      </Stack>
    );
  } else {
    body =
      phase === 'failed' ? (
        <Message tone="problem">
          The machine did not join. {problem ?? ''} Try again to make a new join code.
        </Message>
      ) : phase === 'stopped' ? (
        <Message tone="note">
          Stopped. The join code is withdrawn and no machine joined. Start again to make a
          new one.
        </Message>
      ) : (
        <Stack>
          <Message tone="done">The machine joined the pool.</Message>
          {machine && machine.backends.some((b) => b.usable && !b.allowed) ? (
            <Message
              tone="needs-you"
              action={
                <LinkButton to={`/setup/machines/${machine.id}`}>
                  Open the machine
                </LinkButton>
              }
            >
              {`${machine.backends
                .filter((b) => b.usable && !b.allowed)
                .map((b) => b.name)
                .join(
                  ', ',
                )} is installed on this machine but not turned on. Open the machine, tick it and press Save to let it run runners.`}
            </Message>
          ) : null}
          {machine &&
          needsEngine(machine, 'linux') &&
          !machine.backends.some((b) => b.usable) ? (
            <ReadyDoor
              machine={machine}
              os="linux"
              kind="ephemeral"
              back="/setup/machines"
              words={`No container engine answers on ${machine.name}. Linux runners need one.`}
            />
          ) : null}
          {machine ? (
            <Facts
              items={[
                { name: 'Name', value: machine.name },
                { name: 'System', value: `${machine.os} ${machine.arch}` },
                { name: 'CPU', value: machine.facts?.cpu_model ?? 'not reported' },
                {
                  name: 'Memory',
                  value: machine.facts?.memory_bytes
                    ? formatSize(machine.facts.memory_bytes)
                    : 'not reported',
                },
                {
                  name: 'Backends found',
                  value:
                    machine.backends
                      .filter((b) => b.present)
                      .map((b) => b.name)
                      .join(', ') || formatNumber(0),
                },
              ]}
            />
          ) : (
            <Facts items={[{ name: 'Machines joined', value: '1' }]} />
          )}
        </Stack>
      );
  }

  return (
    <Wizard
      name="Add a machine"
      steps={STEPS}
      current={step}
      phase={phase}
      reading="Reading the controller"
      progress={
        phase === 'running'
          ? {
              text: created
                ? `${PROGRESS_STEPS.find((p) => p.id === progress)?.name ?? 'Waiting for the agent'}, ${formatTimeLeft(Date.parse(created.join.expires_at) - now)}`
                : usingSsh
                  ? 'Signing in over SSH'
                  : method === 'download'
                    ? 'Making the installer'
                    : 'Making the join code',
            }
          : undefined
      }
      canStop
      stopping={stopping}
      onStop={() => void stop()}
      onBack={() => {
        const i = Math.max(0, step - 1);
        setStep(i);
        setPhase(i === 0 ? 'first-run' : 'ready-to-review');
      }}
      onStep={(i) => {
        setStep(i);
        setPhase(i === 0 ? 'first-run' : 'ready-to-review');
      }}
      primary={
        step === 0
          ? { label: 'Next', onPress: () => void next() }
          : {
              label: 'Join a machine',
              busyLabel: 'Joining',
              onPress: () => void startJoining(),
            }
      }
      review={step === 1}
      onStartAgain={startAgain}
      onTryAgain={startAgain}
      {...(onDone
        ? {}
        : {
            donePrimary: {
              label: 'Open Machines',
              onPress: () => void navigate('/setup/machines'),
            },
          })}
    >
      {body}
      <Ask
        title="Is this the host key?"
        action="Yes, sign in"
        busyLabel="Signing in"
        q={
          hostKey !== undefined
            ? {
                items: [`Host ${host} shows the key ${hostKey}`],
                waitsFor: [],
              }
            : undefined
        }
        busy={false}
        error={undefined}
        onCancel={() => setHostKey(undefined)}
        onConfirm={() => void startJoining(hostKey)}
      />
    </Wizard>
  );
}

export function AddMachinePage() {
  return (
    <Page>
      <Breadcrumbs
        trail={[{ to: '/setup/machines', label: 'Machines' }, { label: 'Add a machine' }]}
      />
      <PageHeader title="Add a machine" />
      <Pane title="New machine">
        <AddMachineWizard />
      </Pane>
    </Page>
  );
}
