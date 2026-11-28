// Repositories (web-console 4.3, R5.21): list with search and filters, selection, turn the pool on or off,
// and the policy of one repository.
import {
  ActionBar,
  Button,
  CheckRow,
  Cluster,
  Breadcrumbs,
  Empty,
  ErrorSummary,
  Facts,
  ChoiceField,
  LinkButton,
  Message,
  NumberField,
  Page,
  PageHeader,
  Pane,
  Stack,
  Loading,
  Status,
  TextAreaField,
  TextField,
  TextLink,
  formatPast,
  useNow,
  type StatusKind,
  EditBar,
} from '@crsp/ui';
import { lazy, Suspense, useEffect, useMemo, useRef, useState } from 'react';
import { useLocation, useNavigate, useParams, useSearchParams } from 'react-router-dom';
import { signInUrl } from '../../auth/session';
import { ApiError, unwrap, useClient, type Schemas } from '../../api/api';
import { useCommand, useLoad, useQuestion } from '../../api/hooks';
import { useSession } from '../../auth/session';
import { RunsOnPrPane } from './RunsOnPr';
import { RolloutPane } from './Rollout';
import { useVersion } from '../../live/LiveProvider';
import { Ask, Load } from '../common';
import {
  ANY_RUNNER_WARNING,
  NO_CAPACITY,
  kindChoices,
  kindSelection,
  runnerKindText,
  type KindSelection,
} from './runnerKinds';
import {
  FALLBACK_COST_ITEMS,
  HostedFields,
  WhereJobsGo,
  hostedBody,
  hostedChanged,
  hostedMistakes,
  hostedStart,
  routingWhy,
  turnsFallbackOn,
  type HostedState,
} from './fallback';
import { InstallAppButton } from './InstallApp';
import { CiChecksPane, GuardrailsPane, JobSlotsPane } from '../insights/RepositoryPanes';
import { MissingPermissionsGuide } from '../GrantGuide';
import { OrgPools } from './OrgPools';
import { PoolGroup, PoolHealth, groupsOf } from './PoolGroups';

// Job sizes is below the fold: it loads with the page, so the first JavaScript stays within its budget (R6.12).
const JobSizesPane = lazy(() =>
  import('./JobSizes').then((m) => ({ default: m.JobSizesPane })),
);
import { allRepositories, applyChanges, type Repository } from './plans';
import {
  PUBLIC_OFF_REASON,
  PUBLIC_SETTING_PATH,
  mayTry,
  publicWhileOff,
  usePublicRepositories,
  usePublicSwitch,
} from './publicRepositories';
import { DeliveryWhen, PollingNotice, deliveryStatus, learnedBy } from './webhookHealth';

/** Why the list is the last copy, with what GitHub really said (UX-28). */
function staleWhy(s: {
  reason?: 'session_expired' | 'permission_missing' | 'unreachable' | 'github_error';
  status?: number;
  message?: string;
}): string {
  switch (s.reason) {
    case 'session_expired':
      return 'Your GitHub sign-in ended. Sign in with GitHub again to refresh the list.';
    case 'unreachable':
      return 'GitHub could not be reached.';
    case 'permission_missing':
      return `GitHub refused (${s.status ?? 403}): ${s.message ?? 'a permission is missing'}. Grant the permission in the guide below.`;
    case 'github_error':
      return `GitHub answered ${s.status ?? 'with an error'}: ${s.message ?? 'no message'}.`;
    default:
      return 'The list could not be refreshed.';
  }
}

/** Why the pool is off, when the controller turned it off (github-integration R5.22). */
export function poolOffWhy(r: Repository): string | undefined {
  return r.pool_off_reason === 'left_installation'
    ? "This repository left the GitHub App's installation, so its pool was turned off."
    : undefined;
}

export function variableStatus(r: Repository): { kind: StatusKind; word: string } {
  if (!r.pool_on) return { kind: 'off', word: 'not used' };
  const bad = r.variables.find((v) => v.status === 'missing' || v.status === 'wrong');
  if (bad)
    return {
      kind: 'failed',
      word: `${bad.status === 'missing' ? 'missing' : 'wrong'} variable`,
    };
  if (r.variables.some((v) => v.status === 'not_managed'))
    return { kind: 'info', word: 'not managed' };
  return { kind: 'ok', word: 'variables ok' };
}

type JobGroupRow = Schemas['JobGroupRow'];

/** The label the repository's jobs use on GitHub-hosted runners while no machine can take them (R5.42), or none. */
export function fallbackLabel(r: Pick<Repository, 'policy' | 'pool_on'>): string {
  if (!r.pool_on || !r.policy.hosted_fallback) return '—';
  const labels = r.policy.hosted_labels ?? {};
  const set = Object.values(labels).filter((x) => x);
  return set.length > 0 ? set.join(', ') : 'default hosted labels';
}

/** Running and queued jobs of a repository, as the Jobs now column says them. */
export function jobsNowText(
  g: Pick<JobGroupRow, 'running' | 'queued'> | undefined,
): string {
  const n = g ? g.running + g.queued : 0;
  return `${n} ${n === 1 ? 'job' : 'jobs'}`;
}

/** The "GitHub App" pane (LOOK64): the App, whether its webhooks deliver, and the owners whose repositories it sees. */
export function GithubAppPane({ items }: { items: readonly Repository[] }) {
  const on = items.filter((r) => r.pool_on);
  const bad = on.filter((r) => deliveryStatus(r).kind === 'failed').length;
  const owners = [...new Set(items.map((r) => r.owner))].sort();
  return (
    <Pane title="GitHub App">
      <Facts
        items={[
          { name: 'App', value: <Status kind="ok" word="installed" /> },
          {
            name: 'Webhooks',
            value:
              bad > 0 ? (
                <Status
                  kind="failed"
                  word={`${bad} ${bad === 1 ? 'repository' : 'repositories'} missed a delivery`}
                />
              ) : (
                <Status kind="ok" word="delivering" />
              ),
          },
          { name: 'Owners', value: owners.join(', ') },
        ]}
      />
    </Pane>
  );
}

export function RepositoriesList() {
  const location = useLocation();
  const client = useClient();
  const session = useSession();
  const navigate = useNavigate();
  const now = useNow();
  const v = useVersion('plans');
  const { result, reload } = useLoad(() => allRepositories(client), [v]);
  const { enabled } = usePublicRepositories();
  const [picked, setPicked] = useState<Set<number>>(new Set());
  const admin = session.can('admin');
  // GitHub's return from the App's install page carries setup_action (install, update or request).
  const [params, setParams] = useSearchParams();
  const [returned] = useState(() => params.get('setup_action') ?? undefined);
  useEffect(() => {
    if (!params.has('setup_action') && !params.has('installation_id')) return;
    const next = new URLSearchParams(params);
    next.delete('setup_action');
    next.delete('installation_id');
    setParams(next, { replace: true });
    reload();
    // Read once on arrival: the address is cleaned and the list is read again.
  }, []);

  const items =
    result.state === 'loading'
      ? undefined
      : (result as { data?: Awaited<ReturnType<typeof allRepositories>> }).data;
  const broken = (r: Repository) => variableStatus(r).kind === 'failed';
  // The cards of Needs you start ticked: the one action there is Repair.
  const seeded = useRef(false);
  useEffect(() => {
    if (seeded.current || !items) return;
    seeded.current = true;
    setPicked(
      new Set(items.items.filter((r) => r.pool_on && broken(r)).map((r) => r.id)),
    );
  }, [items]);
  const stale = items?.stale.stale === true;
  const why = (r: Repository): string | undefined => {
    if (r.pool_on) return undefined;
    // While the list is the last copy the admin flag is unknown, not "no": the plan checks it again (CRSP148, R5.19).
    if (!stale && !mayTry(r))
      return 'You need admin rights on this repository in GitHub.';
    if (publicWhileOff(r, enabled))
      return 'Public repositories are off. Turn them on in Settings first.';
    return undefined;
  };
  const blocked = admin ? undefined : session.whyNot('admin');
  const go = (to: 'on' | 'off' | 'repair', rows: Repository[]) =>
    void navigate(
      `/setup/pool/change?to=${to}&${rows.map((r) => `repo=${r.id}`).join('&')}`,
    );
  return (
    <Page>
      {returned ? (
        <Message
          tone={returned === 'request' ? 'needs-you' : 'done'}
          action={
            returned === 'request' ? undefined : (
              <LinkButton to="/setup/pool">Pool setup</LinkButton>
            )
          }
        >
          {returned === 'request'
            ? 'GitHub sent the install request to an owner of the account. The repositories show here when they approve it.'
            : 'GitHub saved the App installation. The list below is read again. Tick the repositories you want and press Turn on.'}
        </Message>
      ) : null}
      <Load result={result} what="repositories" reload={reload}>
        {(data) => {
          // Only repositories of an active type are listed (LOOK D16); the rest are counted in a note.
          const unlisted = data.items.filter((r) => publicWhileOff(r, enabled));
          const listed = data.items.filter((r) => !publicWhileOff(r, enabled));
          const g = groupsOf(listed, broken);
          const mainBad = g.bad.length > 0;
          const mainRows = mainBad ? g.bad : g.not;
          const mainPicked = mainRows.filter((r) => picked.has(r.id));
          const mainWord = mainBad ? 'Repair' : 'Turn on';
          if (data.items.length === 0)
            return (
              <Pane title="Repositories">
                <Empty
                  title="No repositories yet. Install the App on a repository to start."
                  action={<InstallAppButton label="Add a repository" onReturn={reload} />}
                />
              </Pane>
            );
          return (
            <Stack>
              <PoolHealth all={data.items} listed={listed} needsYou={g.bad.length} />
              {data.stale.stale ? (
                <Message
                  tone="needs-you"
                  action={
                    data.stale.reason === 'session_expired' ? (
                      <Button
                        onPress={() =>
                          window.location.assign(
                            signInUrl(`${location.pathname}${location.search}`),
                          )
                        }
                      >
                        Sign in with GitHub again
                      </Button>
                    ) : undefined
                  }
                >
                  {staleWhy(data.stale)}
                  {data.stale.as_of
                    ? ` The list is the last copy, from ${formatPast(data.stale.as_of, now)}.`
                    : ' The list is the last copy.'}
                </Message>
              ) : null}
              {unlisted.length > 0 ? (
                <Message
                  tone="note"
                  action={
                    <LinkButton to={PUBLIC_SETTING_PATH}>Open the setting</LinkButton>
                  }
                >
                  {`${unlisted.length} public ${unlisted.length === 1 ? 'repository is' : 'repositories are'} not listed: the pool serves private repositories only.`}
                </Message>
              ) : null}
              {g.bad.length > 0 ? (
                <PoolGroup
                  title="Needs you"
                  rows={g.bad}
                  picked={picked}
                  setPicked={setPicked}
                  why={why}
                  aside={(r) => ({ word: variableStatus(r).word, needsYou: true })}
                  onAct={(rows) => go('repair', rows)}
                />
              ) : null}
              <PoolGroup
                title="Using the pool"
                rows={g.good}
                picked={picked}
                setPicked={setPicked}
                action={(n) => (n === 0 ? 'Turn off' : `Turn off ${n}`)}
                blocked={blocked}
                why={why}
                onAct={(rows) => go('off', rows)}
              />
              <PoolGroup
                title="Not using the pool"
                rows={g.not}
                picked={picked}
                setPicked={setPicked}
                {...(mainBad
                  ? { action: (n: number) => (n === 0 ? 'Turn on' : `Turn on ${n}`) }
                  : {})}
                blocked={blocked}
                why={why}
                aside={(r) =>
                  r.pool_off_reason === 'left_installation'
                    ? { word: 'left the installation', needsYou: false }
                    : undefined
                }
                onAct={(rows) => go('on', rows)}
              />
              <ActionBar
                status={`${mainPicked.length} selected`}
                secondary={
                  <InstallAppButton label="Add a repository" onReturn={reload} />
                }
                primary={
                  <Button
                    kind="primary"
                    disabled={mainPicked.length === 0 || blocked !== undefined}
                    reason={blocked ?? 'Tick at least one repository.'}
                    onPress={() => go(mainBad ? 'repair' : 'on', mainPicked)}
                  >
                    {mainPicked.length === 0
                      ? mainWord
                      : `${mainWord} ${mainPicked.length}`}
                  </Button>
                }
              />
            </Stack>
          );
        }}
      </Load>
      {items && items.items.length > 0 ? <GithubAppPane items={items.items} /> : null}
      <MissingPermissionsGuide onReady={reload} />
      <OrgPools version={v} />
      {items?.items.some((r) => r.pool_on) ? <RunsOnPrPane /> : null}
    </Page>
  );
}

export function RepositoryDetail() {
  const { owner = '', name = '' } = useParams();
  const client = useClient();
  const v = useVersion('plans');
  const { result, reload } = useLoad(async () => {
    const list = await unwrap(
      client.GET('/api/v1/github/repositories', {
        params: { query: { q: name, limit: 100 } },
      }),
    );
    const hit = list.items.find((r) => r.owner === owner && r.name === name);
    if (!hit)
      throw new ApiError(
        'not_found',
        `${owner}/${name} is not listed. Install the App on it.`,
        404,
      );
    return unwrap(
      client.GET('/api/v1/repositories/{repositoryId}', {
        params: { path: { repositoryId: hit.id } },
      }),
    );
  }, [owner, name, v]);
  return (
    <Page>
      <Breadcrumbs
        trail={[
          { to: '/setup/pool', label: 'Repositories' },
          { label: `${owner}/${name}` },
        ]}
      />
      {result.state === 'loading' ||
      (result.state === 'failed' && result.data === undefined) ? (
        <PageHeader title={`${owner}/${name}`} />
      ) : null}
      <Load result={result} what="the repository" reload={reload}>
        {(r) => <RepositoryBody r={r} onChanged={reload} />}
      </Load>
    </Page>
  );
}

function RepositoryBody({ r, onChanged }: { r: Repository; onChanged: () => void }) {
  const client = useClient();
  const session = useSession();
  const navigate = useNavigate();
  const admin = session.can('admin');
  const p = r.policy;
  const [labels, setLabels] = useState(p.labels.join(', '));
  const [groups, setGroups] = useState(p.runner_groups.join(', '));
  const [max, setMax] = useState(p.max_parallel_jobs);
  const [priority, setPriority] = useState(p.priority);
  const [forks, setForks] = useState(p.allow_fork_pull_requests);
  const [kinds, setKinds] = useState<KindSelection>(kindSelection(p));
  const [hosted, setHosted] = useState<HostedState>(() => hostedStart(r));
  const startPackages = (p.extra_packages ?? []).join('\n');
  const [packages, setPackages] = useState(startPackages);
  const [errors, setErrors] = useState<string[]>([]);
  const [saved, setSaved] = useState<string | undefined>(undefined);

  const split = (s: string) =>
    s
      .split(',')
      .map((x) => x.trim())
      .filter(Boolean);
  const body = useMemo(
    () => ({
      labels: split(labels),
      runner_groups: split(groups),
      max_parallel_jobs: max,
      priority,
      allow_fork_pull_requests: forks,
      ...(kinds !== kindSelection(p) ? { runner_kinds: kinds } : {}),
    }),
    [labels, groups, max, priority, forks, kinds, p],
  );
  const diff = {
    labels: labels !== p.labels.join(', '),
    groups: groups !== p.runner_groups.join(', '),
    max: max !== p.max_parallel_jobs,
    priority: priority !== p.priority,
    forks: forks !== p.allow_fork_pull_requests,
    kinds: kinds !== kindSelection(p),
    hosted: hostedChanged(hosted, hostedStart(r)),
    packages: packages !== startPackages,
  };
  const policyChanged =
    diff.labels ||
    diff.groups ||
    diff.max ||
    diff.priority ||
    diff.forks ||
    diff.kinds ||
    diff.hosted;
  const packagesChanged = diff.packages;
  const changeCount = Object.values(diff).filter(Boolean).length;
  /** Cancel: the form goes back to what is saved. */
  const cancel = () => {
    setLabels(p.labels.join(', '));
    setGroups(p.runner_groups.join(', '));
    setMax(p.max_parallel_jobs);
    setPriority(p.priority);
    setForks(p.allow_fork_pull_requests);
    setKinds(kindSelection(p));
    setHosted(hostedStart(r));
    setPackages(startPackages);
    setErrors([]);
    setSaved(undefined);
  };
  const forkRisk = forks && !p.allow_fork_pull_requests;
  const costRisk = turnsFallbackOn(r, hosted);
  // A public repository on Any runner lets its jobs reach a persistent runner (github-integration R5.60).
  const anyRisk = diff.kinds && r.visibility === 'public' && kinds === 'any_runner';

  /** One request for the policy and the hosted fallback, one for the packages: only what changed. */
  const send = async (confirm: { fork?: boolean; cost?: boolean }) => {
    if (policyChanged)
      await unwrap(
        client.PATCH('/api/v1/repositories/{repositoryId}/policy', {
          params: { path: { repositoryId: r.id } },
          body: {
            ...body,
            ...hostedBody(hosted),
            ...(confirm.fork ? { confirm_fork_risk: true } : {}),
            ...(confirm.cost ? { confirm_fallback_cost: true } : {}),
          },
        }),
      );
    if (packagesChanged) {
      const list = await unwrap(
        client.PUT('/api/v1/repositories/{repositoryId}/extra-packages', {
          params: { path: { repositoryId: r.id } },
          body: { text: packages },
        }),
      );
      setPackages(list.text);
    }
    setSaved(policyChanged || packagesChanged ? 'Saved.' : 'Nothing changed.');
    onChanged();
  };
  const riskQ = useQuestion(
    () => send({ fork: forkRisk, cost: costRisk }),
    () => undefined,
  );
  const save = useCommand(async () => {
    setSaved(undefined);
    const problems: string[] = [];
    if (max < 1) problems.push('Maximum parallel jobs must be at least 1.');
    if (split(labels).length === 0) problems.push('Add at least one label.');
    problems.push(...hostedMistakes(hosted), ...packageMistakes(packages));
    setErrors(problems);
    if (problems.length) return;
    if (forkRisk || costRisk || anyRisk) {
      riskQ.ask([
        ...(anyRisk ? [ANY_RUNNER_WARNING] : []),
        ...(forkRisk
          ? [
              'Jobs from public forks run on your machines with your repository variables in reach.',
            ]
          : []),
        ...(costRisk ? FALLBACK_COST_ITEMS(r.full_name) : []),
      ]);
      return;
    }
    await send({});
  });
  const risks = [forkRisk, costRisk, anyRisk].filter(Boolean).length;
  const riskTitle =
    risks > 1
      ? 'Save these changes?'
      : anyRisk
        ? 'Let public jobs use persistent runners?'
        : costRisk
          ? 'Run on GitHub-hosted runners?'
          : 'Allow jobs from public forks?';

  const offQ = useQuestion(async () => {
    const plan = await applyChanges(client, [
      { repository_id: r.id, pool: 'off' as const },
    ]);
    if (plan.state !== 'done') throw new Error('The pool was not turned off.');
  }, onChanged);

  const { setting, enabled, reload: reloadSetting } = usePublicRepositories();
  const sw = usePublicSwitch(setting, () => {
    reloadSetting();
    onChanged();
  });
  const publicOff = publicWhileOff(r, enabled);
  const vs = variableStatus(r);
  return (
    <>
      <PageHeader
        title={r.full_name}
        lead={`${r.visibility} repository`}
        action={
          r.pool_on ? (
            <Button
              disabled={!admin}
              reason={session.whyNot('admin')}
              onPress={() =>
                offQ.ask([
                  `${r.full_name}: pool off`,
                  ...r.variables.map((x) => `Variable ${x.name}`),
                ])
              }
            >
              Stop using pool
            </Button>
          ) : (
            <Button
              disabled={!admin || !mayTry(r) || publicOff}
              reason={
                !admin
                  ? session.whyNot('admin')
                  : !mayTry(r)
                    ? 'You need admin rights on this repository in GitHub.'
                    : publicOff
                      ? 'Public repositories are off. Turn them on in Settings first.'
                      : undefined
              }
              onPress={() => void navigate(`/setup/pool/turn-on?repo=${r.id}`)}
            >
              Use pool
            </Button>
          )
        }
      />
      {r.problem ? (
        <Message
          tone="problem"
          action={
            <Cluster>
              {r.problem.actions.map((a) =>
                a.id === 'turn_on_public_repositories' ? (
                  <Button
                    key={a.id}
                    onPress={() => sw.ask(true)}
                    disabled={!session.can('owner')}
                    reason={session.whyNot('owner')}
                  >
                    {a.label}
                  </Button>
                ) : (
                  <Button
                    key={a.id}
                    kind="danger"
                    disabled={!admin}
                    reason={session.whyNot('admin')}
                    onPress={() =>
                      offQ.ask([
                        `${r.full_name}: pool off`,
                        ...r.variables.map((x) => `Variable ${x.name}`),
                      ])
                    }
                  >
                    {a.label}
                  </Button>
                ),
              )}
            </Cluster>
          }
        >
          {r.problem.text}
        </Message>
      ) : null}
      {sw.error ? (
        <Message tone="problem">
          Could not save Enable for public repositories. {sw.error}
        </Message>
      ) : null}
      {sw.dialog}
      {publicOff && !r.problem ? (
        <Message tone="note">
          {PUBLIC_OFF_REASON}{' '}
          <TextLink to={PUBLIC_SETTING_PATH}>Turn on public repositories</TextLink>
        </Message>
      ) : null}
      <Pane title="State">
        <Facts
          items={[
            {
              name: 'Pool',
              value: (
                <Status
                  kind={r.pool_on ? 'ok' : 'off'}
                  word={r.pool_on ? 'pool on' : 'pool off'}
                />
              ),
            },
            ...(poolOffWhy(r) ? [{ name: 'Why it is off', value: poolOffWhy(r) }] : []),
            { name: 'Variables', value: <Status kind={vs.kind} word={vs.word} /> },
            { name: 'Jobs go to', value: <WhereJobsGo r={r} /> },
            { name: 'Runner type', value: runnerKindText(r.policy) },
            { name: 'Why', value: routingWhy(r) },
            ...(r.pool_on
              ? [
                  { name: 'Last webhook delivery', value: <DeliveryWhen r={r} /> },
                  {
                    name: 'Jobs are learned by',
                    value: learnedBy(r),
                  },
                ]
              : []),
            ...(r.routing && r.policy.hosted_fallback
              ? [
                  {
                    name: 'Hosted minutes this month',
                    value: String(r.routing.used_minutes),
                  },
                ]
              : []),
            ...r.variables.map((x) => ({
              name: x.name,
              value: x.status.replace('_', ' '),
            })),
          ]}
        />
      </Pane>
      {r.pool_on ? <PollingNotice r={r} onChanged={onChanged} /> : null}
      <form
        onSubmit={(e) => {
          e.preventDefault();
          void save.run();
        }}
      >
        <Stack>
          <ErrorSummary errors={errors} />
          <Pane title="Policy">
            <Stack>
              <TextField
                label="Labels"
                help="Separate labels with commas."
                value={labels}
                changed={diff.labels}
                onChange={setLabels}
                width="full"
              />
              <TextField
                label="Runner groups"
                optional
                value={groups}
                changed={diff.groups}
                onChange={setGroups}
                width="full"
              />
              <Cluster>
                <NumberField
                  label="Maximum parallel jobs"
                  value={max}
                  changed={diff.max}
                  min={1}
                  max={64}
                  onChange={setMax}
                />
                <NumberField
                  label="Priority"
                  value={priority}
                  changed={diff.priority}
                  min={0}
                  max={100}
                  onChange={setPriority}
                />
              </Cluster>
              <CheckRow
                label="Run jobs from public forks"
                checked={forks}
                changed={diff.forks}
                onChange={setForks}
              />
              <ChoiceField
                label="Runner type"
                help="Which runners take this repository's jobs."
                value={kinds}
                changed={diff.kinds}
                onChange={(v) => setKinds(v as KindSelection)}
                choices={kindChoices(r)}
              />
              {kinds === 'any_runner' && r.visibility === 'public' ? (
                <Message tone="needs-you">{ANY_RUNNER_WARNING}</Message>
              ) : null}
              {r.pool_on && r.ephemeral_capacity === false ? (
                <Message
                  tone="needs-you"
                  action={<LinkButton to="/setup/runners/add">Add runners</LinkButton>}
                >
                  {NO_CAPACITY}
                </Message>
              ) : null}
              {forks ? (
                <Message tone="needs-you">
                  Jobs from public forks run code you did not write. Save asks once more
                  before this takes effect.
                </Message>
              ) : null}
            </Stack>
          </Pane>
          <Pane title="Hosted fallback">
            <HostedFields r={r} value={hosted} onChange={setHosted} />
          </Pane>
          <Pane title="Extra packages">
            <Stack>
              <TextAreaField
                label="Extra packages"
                help="Distribution packages added to the pool image, one name per line."
                value={packages}
                changed={diff.packages}
                onChange={setPackages}
                rows={6}
                width="full"
                mono
                optional
                error={packageMistakes(packages)[0]}
              />
              <Message tone="note">
                A changed list builds a new image aside. Running jobs keep the old image
                until they end.
              </Message>
            </Stack>
          </Pane>
        </Stack>
      </form>
      <CiChecksPane repositoryId={r.id} />
      <GuardrailsPane fullName={r.full_name} />
      <JobSlotsPane repositoryId={r.id} />
      {r.pool_on ? (
        <RolloutPane repositoryId={r.id} fullName={r.full_name} poolOn={r.pool_on} />
      ) : null}
      {r.pool_on ? <RunsOnPrPane repositoryId={r.id} /> : null}
      <Suspense fallback={<Loading what="Loading job sizes" />}>
        <JobSizesPane fullName={r.full_name} />
      </Suspense>
      {saved ? <Message tone="done">{saved}</Message> : null}
      {save.error ? <Message tone="problem">Could not save. {save.error}</Message> : null}
      <EditBar
        changes={changeCount}
        submit={false}
        onSave={() => void save.run()}
        onCancel={cancel}
        busy={save.busy}
        blocked={!admin}
        why={session.whyNot('admin')}
      />
      <Ask
        title={riskTitle}
        action={risks > 1 ? 'Save' : costRisk ? 'Turn on' : 'Allow'}
        busyLabel="Saving"
        q={riskQ.question}
        busy={riskQ.busy}
        error={riskQ.error}
        onCancel={riskQ.cancel}
        onConfirm={() => void riskQ.confirm()}
      />
      <Ask
        title={`Turn off ${r.full_name}?`}
        action="Turn off"
        busyLabel="Turning off"
        q={offQ.question}
        busy={offQ.busy}
        error={offQ.error}
        onCancel={offQ.cancel}
        onConfirm={() => void offQ.confirm()}
      />
    </>
  );
}

const PACKAGE_NAME = /^[A-Za-z0-9][A-Za-z0-9+.-]*$/;

/** The first mistake of a package list, with its line number, as the controller words it (runners R5.51). */
export function packageMistakes(text: string): string[] {
  const out: string[] = [];
  text
    .replace(/\r\n/g, '\n')
    .split('\n')
    .forEach((raw, i) => {
      const line = raw.trim();
      if (line === '' || line.startsWith('#') || PACKAGE_NAME.test(line)) return;
      out.push(
        `Line ${i + 1}: ${line} is not a package name. Use letters, digits and + - . only, and do not start with -.`,
      );
    });
  return out;
}
