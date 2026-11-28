// Settings, Updates, Import agent installers (operations R5.132, web-console R5.x): the offline path. A controller
// that cannot reach the release gets SHA256SUMS and the CrspAgent-* files of a release that the owner carried over.
// Steps: Files, Review, Importing, Done. SHA256SUMS goes first, then each file; the controller checks every file
// against SHA256SUMS and keeps nothing that does not match, and a refused file is named. The shared wizard owns the
// steps, the locks, Stop and the busy state; this page passes data only. The words are in content.md ("Import agent
// installers").
import {
  Breadcrumbs,
  BulletList,
  Facts,
  FileListField,
  Message,
  Page,
  PageHeader,
  Stack,
  Text,
  Wizard,
  formatSize,
  type WizardPhase,
} from '@crsp/ui';
import { useEffect, useRef, useState } from 'react';
import { useNavigate, useSearchParams } from 'react-router-dom';
import { errorText, unwrap, useClient, type Schemas } from '../../api/api';
import { useSession } from '../../auth/session';
import { settingsPath } from './sections';

type Installers = Schemas['AgentInstallers'];

const STEPS = ['Files', 'Review', 'Importing', 'Done'] as const;
const SUMS = 'SHA256SUMS';

/** SHA256SUMS first (every installer is checked against it), then the others by name. */
export function sendOrder(files: readonly File[]): File[] {
  return [...files].sort((a, b) =>
    a.name === SUMS ? -1 : b.name === SUMS ? 1 : a.name.localeCompare(b.name),
  );
}

/** The names a SHA256SUMS text lists ("<hash>  <name>" or "<hash> *<name>" per line). */
export function listedNames(text: string): Set<string> {
  const names = new Set<string>();
  for (const line of text.split(/\r?\n/)) {
    const m = /^[0-9a-fA-F]{64}\s+\*?(.+?)\s*$/.exec(line);
    if (m?.[1]) names.add(m[1]);
  }
  return names;
}

interface Refused {
  name: string;
  why: string;
}

export function AgentInstallersWizard() {
  const client = useClient();
  const session = useSession();
  const navigate = useNavigate();
  const [params, setParams] = useSearchParams();
  const wasStarted = useRef(params.get('started') === '1').current;
  const mounted = useRef(true);
  const abort = useRef<AbortController | undefined>(undefined);
  const [phase, setPhase] = useState<WizardPhase>('loading');
  const [list, setList] = useState<Installers | undefined>(undefined);
  const [files, setFiles] = useState<File[]>([]);
  const [unlisted, setUnlisted] = useState<string[]>([]);
  const [sent, setSent] = useState<string[]>([]);
  const [refused, setRefused] = useState<Refused[]>([]);
  const [at, setAt] = useState<{ n: number; of: number; name: string } | undefined>(
    undefined,
  );
  const [stopping, setStopping] = useState(false);
  const [problem, setProblem] = useState<string | undefined>(undefined);
  const admin = session.can('admin');
  const why = session.whyNot('admin');

  useEffect(() => {
    mounted.current = true;
    return () => {
      mounted.current = false;
      abort.current?.abort();
    };
  }, []);

  const read = () => unwrap(client.GET('/api/v1/agent-installers'));
  const stored = (l: Installers) => l.installers.filter((i) => i.stored).length;

  const settle = (l: Installers, afterReload: boolean) => {
    setList(l);
    if (!wasStarted || !afterReload) {
      setPhase('first-run');
      return;
    }
    const complete =
      l.have_sums && l.installers.length > 0 && stored(l) === l.installers.length;
    setPhase(complete ? 'done-after-reload' : 'paused');
  };
  const load = (afterReload: boolean) => {
    setPhase('loading');
    read()
      .then((l) => mounted.current && settle(l, afterReload))
      .catch((e: unknown) => {
        if (!mounted.current) return;
        setProblem(errorText(e));
        setPhase('failed');
      });
  };
  useEffect(() => {
    load(true);
    // The controller's list is read once, when the page opens.
  }, [client]);

  const toReview = async () => {
    setPhase('planning');
    const sums = files.find((f) => f.name === SUMS);
    let named: Set<string> | undefined;
    if (sums) {
      try {
        named = listedNames(await sums.text());
      } catch {
        named = undefined;
      }
    }
    if (!mounted.current) return;
    setUnlisted(
      named
        ? files.filter((f) => f.name !== SUMS && !named.has(f.name)).map((f) => f.name)
        : [],
    );
    setPhase('ready-to-review');
  };

  const start = async () => {
    const order = sendOrder(files);
    const ctl = new AbortController();
    abort.current = ctl;
    setStopping(false);
    setSent([]);
    setRefused([]);
    setProblem(undefined);
    setPhase('running');
    const next = new URLSearchParams(params);
    next.set('started', '1');
    setParams(next, { replace: true });
    const ok: string[] = [];
    const bad: Refused[] = [];
    let last: Installers | undefined;
    for (const [i, file] of order.entries()) {
      if (ctl.signal.aborted) break;
      setAt({ n: i + 1, of: order.length, name: file.name });
      try {
        last = await unwrap(
          client.PUT('/api/v1/agent-installers/{name}', {
            params: { path: { name: file.name } },
            body: file as unknown as string,
            bodySerializer: (b: unknown) => b as BodyInit,
            headers: { 'Content-Type': 'application/octet-stream' },
            signal: ctl.signal,
          }),
        );
        ok.push(file.name);
      } catch (e) {
        if (ctl.signal.aborted) break;
        bad.push({ name: file.name, why: errorText(e) });
      }
      if (!mounted.current) return;
      setSent([...ok]);
      setRefused([...bad]);
    }
    if (!mounted.current) return;
    abort.current = undefined;
    setAt(undefined);
    if (last) setList(last);
    if (ctl.signal.aborted) setPhase('stopped');
    else if (bad.length > 0) {
      setProblem(`${bad.length} of ${order.length} files were not stored.`);
      setPhase('failed');
    } else setPhase('done');
  };

  const stop = () => {
    setStopping(true);
    abort.current?.abort();
  };

  const reset = (keepFiles: boolean) => {
    if (!keepFiles) setFiles([]);
    setUnlisted([]);
    setSent([]);
    setRefused([]);
    setProblem(undefined);
    setParams(new URLSearchParams(), { replace: true });
    if (!list) load(false);
    else setPhase('first-run');
  };

  const toUpdates = () => void navigate(settingsPath('updates'));
  const done = phase === 'done' || phase === 'done-after-reload';
  const total = files.length;
  const word = (n: number) => `${n} ${n === 1 ? 'file' : 'files'}`;

  let body;
  if (phase === 'first-run') {
    body = (
      <Stack>
        {list?.from_release ? (
          <Message tone="note" dismissible={false}>
            This controller read SHA256SUMS from the release itself. Import installers
            only if it cannot download them.
          </Message>
        ) : null}
        <FileListField
          label="Release files"
          help="SHA256SUMS and the CrspAgent files of the release. The controller checks each file against SHA256SUMS."
          initial={files}
          onChange={setFiles}
        />
      </Stack>
    );
  } else if (phase === 'planning') {
    body = (
      <Message tone="note" dismissible={false}>
        Reading SHA256SUMS.
      </Message>
    );
  } else if (phase === 'ready-to-review') {
    body = (
      <Stack>
        <Text>
          {`Send ${word(total)} to the controller${list ? ` (version ${list.version})` : ''}. SHA256SUMS goes first. A file that does not match SHA256SUMS is refused and nothing of it is kept.`}
        </Text>
        <BulletList
          items={sendOrder(files).map((f) => `${f.name}, ${formatSize(f.size)}`)}
        />
        {unlisted.length > 0 ? (
          <Message tone="needs-you">
            {`SHA256SUMS does not list ${unlisted.join(', ')}. The controller will refuse ${unlisted.length === 1 ? 'it' : 'them'}.`}
          </Message>
        ) : null}
        {!files.some((f) => f.name === SUMS) && list && !list.have_sums ? (
          <Message tone="needs-you">
            The controller has no SHA256SUMS yet. Add it, or every installer is refused.
          </Message>
        ) : null}
      </Stack>
    );
  } else if (phase === 'running') {
    body = (
      <Stack>
        <Text>{`Stored so far: ${sent.length}.`}</Text>
        {refused.length > 0 ? (
          <BulletList items={refused.map((r) => `${r.name}: ${r.why}`)} />
        ) : null}
      </Stack>
    );
  } else if (phase === 'paused') {
    body = (
      <Message tone="needs-you">
        {`Paused. The page closed while the files were sent. The controller keeps the ${list ? stored(list) : 0} it stored. Start again and choose the files that are left.`}
      </Message>
    );
  } else if (phase === 'stopped') {
    body = (
      <Stack>
        <Message tone="note" dismissible={false}>
          {`Stopped. ${sent.length} of ${total} files were stored; the rest were not sent. Nothing stored is removed.`}
        </Message>
        {refused.length > 0 ? (
          <BulletList items={refused.map((r) => `${r.name}: ${r.why}`)} />
        ) : null}
      </Stack>
    );
  } else if (phase === 'failed') {
    body = (
      <Stack>
        <Message tone="problem">
          {`${problem ?? 'The import did not finish.'} The files that matched are kept. Choose the refused files again.`}
        </Message>
        {refused.length > 0 ? (
          <BulletList items={refused.map((r) => `${r.name}: ${r.why}`)} />
        ) : null}
      </Stack>
    );
  } else if (done) {
    body = (
      <Stack>
        <Message tone="done">
          {phase === 'done'
            ? `${word(sent.length)} stored and checked against SHA256SUMS.`
            : 'The agent installers are stored.'}
        </Message>
        <Facts
          items={[
            ...(list ? [{ name: 'Version', value: list.version }] : []),
            ...(list
              ? [
                  {
                    name: 'Installers stored',
                    value: `${stored(list)} of ${list.installers.length}`,
                  },
                ]
              : []),
          ]}
        />
      </Stack>
    );
  } else body = null;

  const primary =
    phase === 'first-run'
      ? {
          label: 'Next',
          onPress: () => void toReview(),
          disabled: files.length === 0,
          reason: files.length === 0 ? 'Choose the release files first.' : undefined,
        }
      : {
          label: `Import ${word(total)}`,
          busyLabel: 'Importing',
          onPress: () => void start(),
          disabled: !admin,
          reason: why,
        };

  return (
    <Page>
      <Breadcrumbs
        trail={[
          { to: settingsPath('updates'), label: 'Updates' },
          { label: 'Import agent installers' },
        ]}
      />
      <PageHeader title="Import agent installers" />
      <Wizard
        name="Import agent installers"
        steps={STEPS}
        current={
          phase === 'ready-to-review'
            ? 1
            : phase === 'running' || phase === 'paused' || phase === 'failed'
              ? 2
              : done
                ? 3
                : 0
        }
        firstStep={0}
        failedAt={2}
        phase={phase}
        reading="Reading the agent installers"
        progress={
          at
            ? {
                text: `Sending ${at.n} of ${at.of}: ${at.name}`,
                value: at.n - 1,
                max: at.of,
              }
            : undefined
        }
        canStop
        stopping={stopping}
        onStop={stop}
        onBack={() => (phase === 'ready-to-review' ? setPhase('first-run') : toUpdates())}
        onStep={(i) => i === 0 && phase === 'ready-to-review' && setPhase('first-run')}
        primary={primary}
        review={phase === 'ready-to-review'}
        onStartAgain={() => reset(false)}
        onTryAgain={() => reset(true)}
        donePrimary={{ label: 'Open Updates', onPress: toUpdates }}
      >
        {body}
      </Wizard>
    </Page>
  );
}
