// "A newer version was found" (RES20, REG5 R5.37): replace an image of the registry with its newer version. One
// wizard of four steps: Compare, Review, Replacing, Done (UX-5, UX-30). The shared Wizard part owns the step, the
// locks, Stop and the busy state (UX-54); this page passes data only. Nothing changes before Review is accepted.
// The plan is stored on the server, so a reload (`?plan=`) shows where it stands (UX-19).
import {
  Breadcrumbs,
  Button,
  Facts,
  Message,
  NumberField,
  Page,
  PageHeader,
  Progress,
  Stack,
  Status,
  Table,
  Text,
  Wizard,
  formatDate,
  formatSize,
  type Column,
  type WizardPhase,
} from '@crsp/ui';
import { useEffect, useRef, useState } from 'react';
import { useNavigate, useParams, useSearchParams } from 'react-router-dom';
import { ApiError, errorText, unwrap, useClient, type Schemas } from '../../api/api';
import { useCommand, useLoad } from '../../api/hooks';
import { TIMING } from '../../timing';
import { Load } from '../common';
import { shortKey } from './words';

const STEPS = ['Compare', 'Review', 'Replacing', 'Done'] as const;
type Plan = Schemas['RegistryReplacePlan'];
type Compare = Schemas['RegistryCompare'];
type FileChange = Schemas['RegistryStackFileChange'];
type Step = NonNullable<Plan['steps']>[number];

const sha = (s: string | undefined) => (s ? shortKey(s) : '–');

export function RegistryReplacePage() {
  const { id = '' } = useParams();
  const [params] = useSearchParams();
  const client = useClient();
  const planId = useRef(params.get('plan')).current;
  const { result, reload } = useLoad(async () => {
    if (planId) {
      const plan = await unwrap(
        client.GET('/api/v1/registry/replace-plans/{planId}', {
          params: { path: { planId } },
        }),
      );
      return { plan, compare: undefined as Compare | undefined };
    }
    const compare = await unwrap(
      client.GET('/api/v1/registry/images/{registryImageId}/compare', {
        params: { path: { registryImageId: id } },
      }),
    );
    return { plan: undefined as Plan | undefined, compare };
  }, [id, planId]);
  return (
    <Page>
      <Breadcrumbs
        trail={[
          { to: '/setup/images?tab=registry', label: '‹ Registry' },
          { label: 'Replace an image' },
        ]}
      />
      <PageHeader
        title="Replace an image"
        lead="A run needs a newer version of an image. Replace the old one so the registry does not keep both."
      />
      <Load result={result} what="the comparison" reload={reload}>
        {(d) => <ReplaceBody imageId={id} compare={d.compare} stored={d.plan} />}
      </Load>
    </Page>
  );
}

function phaseOf(plan: Plan | undefined, stored: boolean): WizardPhase {
  if (!plan) return 'first-run';
  switch (plan.state) {
    case 'draft':
      return 'ready-to-review';
    case 'running':
      return 'running';
    case 'stopped':
      return 'stopped';
    case 'failed':
      return 'failed';
    default:
      return stored ? 'done-after-reload' : 'done';
  }
}

const stepOf = (phase: WizardPhase): number =>
  phase === 'first-run' || phase === 'planning' || phase === 'loading'
    ? 0
    : phase === 'ready-to-review'
      ? 1
      : phase === 'done' || phase === 'done-after-reload'
        ? 3
        : 2;

function ReplaceBody({
  imageId,
  compare,
  stored,
}: {
  imageId: string;
  compare: Compare | undefined;
  stored: Plan | undefined;
}) {
  const navigate = useNavigate();
  const client = useClient();
  const [, setParams] = useSearchParams();
  const [plan, setPlan] = useState<Plan | undefined>(stored);
  const [keep, setKeep] = useState(compare?.keep_days_default ?? stored?.keep_days ?? 7);
  const [planning, setPlanning] = useState(false);
  const [problem, setProblem] = useState<string | undefined>(undefined);
  const [back, setBack] = useState(false);
  const [stopping, setStopping] = useState(false);
  const fromStore = useRef(stored !== undefined).current;
  const phase: WizardPhase = planning
    ? 'planning'
    : back
      ? 'first-run'
      : phaseOf(plan, fromStore && plan?.id === stored?.id);
  const current = stepOf(phase);
  const failed = phase === 'failed';

  // The server is the truth while it runs (UX-19): ask again until the plan ends.
  const planId = plan?.id;
  const running = plan?.state === 'running';
  useEffect(() => {
    if (!planId || !running) return;
    let live = true;
    const t = setInterval(() => {
      void unwrap(
        client.GET('/api/v1/registry/replace-plans/{planId}', {
          params: { path: { planId } },
        }),
      )
        .then((p) => {
          if (!live) return;
          setPlan(p);
          if (p.state !== 'running') setStopping(false);
        })
        .catch(() => undefined);
    }, TIMING.planPollMs);
    return () => {
      live = false;
      clearInterval(t);
    };
  }, [client, planId, running]);

  const make = async () => {
    setPlanning(true);
    setProblem(undefined);
    try {
      const p = await unwrap(
        client.POST('/api/v1/registry/replace-plans', {
          body: { new_image_id: imageId, keep_days: keep },
        }),
      );
      setPlan(p);
      setBack(false);
      setParams({ plan: p.id }, { replace: true });
    } catch (e) {
      setProblem(errorText(e));
    } finally {
      setPlanning(false);
    }
  };
  const start = useCommand(async () => {
    if (!plan) return;
    setPlan(
      await unwrap(
        client.POST('/api/v1/registry/replace-plans/{planId}/start', {
          params: { path: { planId: plan.id } },
        }),
      ),
    );
  });
  const stop = async () => {
    if (!plan) return;
    setStopping(true);
    try {
      setPlan(
        await unwrap(
          client.POST('/api/v1/registry/replace-plans/{planId}/stop', {
            params: { path: { planId: plan.id } },
          }),
        ),
      );
    } catch (e) {
      // 409: the plan has already ended; the next read shows how.
      if (!(e instanceof ApiError && e.status === 409)) setProblem(errorText(e));
      setStopping(false);
    }
  };
  const rollback = useCommand(async () => {
    if (!plan) return;
    setPlan(
      await unwrap(
        client.POST('/api/v1/registry/replace-plans/{planId}/rollback', {
          params: { path: { planId: plan.id } },
        }),
      ),
    );
  });

  const nu = plan?.new_image ?? compare?.new;
  const old = plan?.old_image ?? compare?.old;
  const nk = nu ? shortKey(nu.key) : '';
  const ok = old ? shortKey(old.key) : '';
  const repositories = plan?.repositories ?? compare?.repositories ?? [];
  const freed = plan?.frees_bytes ?? compare?.frees_bytes ?? 0;
  const until = plan?.keep_until ? formatDate(plan.keep_until) : 'the days you chose end';

  const fileCols: Column<FileChange>[] = [
    { id: 'file', header: 'Stack file', cell: (f) => f.path, kind: 'fit' },
    {
      id: 'before',
      header: `In ${ok}`,
      cell: (f) => (f.change === 'added' ? 'not there' : sha(f.before_sha256)),
      kind: 'grow',
    },
    {
      id: 'after',
      header: `In ${nk}`,
      cell: (f) => (f.change === 'removed' ? 'removed' : sha(f.after_sha256)),
      kind: 'grow',
    },
  ];
  // The server lists the steps and says where each stands; the page only words them.
  const stepName = (s: Step) =>
    s.kind === 'point'
      ? `Point ${s.targets.join(', ')} at ${nk}`
      : s.kind === 'warm'
        ? `Warm ${s.targets.join(' and ') || 'the machines'} with ${nk}`
        : `Mark ${ok} superseded until ${until}`;
  const stepState = {
    done: { kind: 'ok', word: 'done' },
    running: { kind: 'running', word: 'running' },
    failed: { kind: 'failed', word: 'failed' },
    waiting: { kind: 'waiting', word: 'waits' },
  } as const;
  const units = plan?.steps_total ?? 0;
  const doneN = plan?.steps_done ?? 0;
  const rows = (plan?.steps ?? []).map((s) => ({
    name: stepName(s),
    state: stepState[s.state],
  }));
  const workCols: Column<(typeof rows)[number]>[] = [
    { id: 'name', header: 'Step', cell: (r) => r.name, kind: 'grow' },
    { id: 'state', header: 'State', cell: (r) => <Status {...r.state} />, kind: 'fit' },
  ];

  return (
    <Stack>
      <Wizard
        name="Replace an image"
        steps={STEPS}
        current={current}
        phase={phase}
        progress={
          phase === 'running'
            ? {
                text:
                  plan?.step_text ??
                  `Replacing ${ok} with ${nk}, step ${Math.min(doneN + 1, units)} of ${units}`,
                value: doneN,
                max: units + 1,
              }
            : undefined
        }
        canStop={phase === 'running'}
        stopping={stopping}
        onStop={() => void stop()}
        onBack={current === 1 && compare ? () => setBack(true) : undefined}
        onStep={(i) => {
          if (i === 0 && compare && current === 1) setBack(true);
        }}
        firstStep={compare ? 0 : 1}
        failedAt={2}
        review={phase === 'ready-to-review'}
        stateNote={
          phase === 'stopped'
            ? 'Stopped. Pointers already moved stay moved.'
            : phase === 'done-after-reload'
              ? 'This page was reloaded. Here is what was done.'
              : undefined
        }
        primary={
          current === 0
            ? { label: 'Review', onPress: () => void make() }
            : {
                label: 'Replace the image',
                busyLabel: 'Replacing',
                onPress: () => void start.run(),
                disabled: start.busy,
              }
        }
        donePrimary={{
          label: 'Open the registry',
          onPress: () => void navigate('/setup/images?tab=registry'),
        }}
        onStartAgain={() => void navigate(`/setup/images/registry/${imageId}`)}
        onTryAgain={() => void start.run()}
      >
        {current === 0 && compare ? (
          <Stack>
            <Message tone="needs-you">
              {`${nu?.profile ?? 'The image'} for ${repositories.join(', ') || 'a repository'} has a newer version. The registry holds both. Replacing keeps one.`}
            </Message>
            <Table
              caption="What changed in the stack files"
              columns={fileCols}
              rows={compare.stack_files}
              rowKey={(f) => f.path}
              oneLine
            />
            <Text tone="muted">
              {compare.base_changed
                ? 'The base image changed too, so the new image is built on a newer base.'
                : 'Same profile, same repository, same base image. Only the stack files above differ, so the new image is an update of the old one.'}
            </Text>
            <NumberField
              label="Keep the old image for"
              unit="days"
              min={0}
              max={365}
              value={keep}
              onChange={(n) => setKeep(n ?? compare.keep_days_default)}
              help="So you can go back to it. After that it is deleted."
            />
            {problem ? (
              <Message tone="problem">{`Could not plan the replacement. ${problem}`}</Message>
            ) : null}
          </Stack>
        ) : null}
        {current === 1 ? (
          <Stack>
            <Facts
              items={[
                { name: 'Old image', value: `${old?.profile ?? ''}, key ${ok}` },
                { name: 'New image', value: `${nu?.profile ?? ''}, key ${nk}` },
                { name: 'Repositories that switch', value: repositories.join(', ') },
                { name: 'The old image', value: `kept until ${until}, then deleted` },
                { name: 'Disk after that', value: `${formatSize(freed)} freed` },
              ]}
            />
            <Text tone="muted">
              A job that runs now keeps the old image. The next job takes the new one.
            </Text>
            {start.error ? (
              <Message tone="problem">{`Could not start. ${start.error}`}</Message>
            ) : null}
          </Stack>
        ) : null}
        {current === 2 ? (
          <Stack>
            {failed ? (
              <Message tone="problem">
                {`${plan?.error ?? 'A step failed.'} Pointers already moved stay moved. Try again, or go back to the old image.`}
              </Message>
            ) : null}
            {phase === 'stopped' ? (
              <Message tone="needs-you">
                Stopped. The repositories that were not switched still use the old image.
              </Message>
            ) : null}
            {problem ? (
              <Message tone="problem">{`Could not stop. ${problem}`}</Message>
            ) : null}
            <Progress
              label="Replacing the image"
              value={failed ? 0 : Math.min(doneN, units)}
              max={Math.max(units, 1)}
            />
            <Table
              caption="What is being done"
              columns={workCols}
              rows={rows}
              rowKey={(r) => r.name}
              oneLine
            />
          </Stack>
        ) : null}
        {current === 3 ? (
          <Stack>
            <Message tone="done">
              {plan?.state === 'rolled_back'
                ? `${ok} is in use again. ${nk} stays in the registry.`
                : `${nk} replaced ${ok}. ${ok} is kept until ${until}, then deleted.`}
            </Message>
            {plan?.can_rollback ? (
              <Stack gap="tight">
                <Button
                  onPress={() => void rollback.run()}
                  busy={rollback.busy}
                  busyLabel="Going back"
                >
                  Go back to the old image
                </Button>
                {rollback.error ? (
                  <Message tone="problem">{`Could not go back. ${rollback.error}`}</Message>
                ) : null}
              </Stack>
            ) : null}
          </Stack>
        ) : null}
      </Wizard>
    </Stack>
  );
}
