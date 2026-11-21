// Repositories (web-console 4.3, R5.21): list with search and filters, selection, turn the pool on or off,
// and the policy of one repository.
import {
  Button,
  CheckRow,
  Chips,
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
  Pager,
  Pane,
  ListToolbar,
  Segmented,
  SearchBar,
  Text,
  Stack,
  Status,
  Tag,
  Table,
  TextAreaField,
  TextField,
  TextLink,
  formatPast,
  paginate,
  useNow,
  type Column,
  type StatusKind,
  ActionBar,
  selectedStatus,
  EditBar,
} from '@crsp/ui';
import { useEffect, useMemo, useState } from 'react';
import { useLocation, useNavigate, useParams, useSearchParams } from 'react-router-dom';
import { signInUrl } from '../../auth/session';
import { ApiError, unwrap, useClient } from '../../api/api';
import { useCommand, useLoad, useQuestion } from '../../api/hooks';
import { useSession } from '../../auth/session';
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
import { allRepositories, applyChanges, type Repository } from './plans';
import {
  PUBLIC_OFF_REASON,
  PUBLIC_SETTING_PATH,
  activeTypes,
  mayTry,
  publicWhileOff,
  usePublicRepositories,
  usePublicSwitch,
  visibilityWord,
} from './publicRepositories';
import { DeliveryStatus, DeliveryWhen, PollingNotice, learnedBy } from './webhookHealth';

const PAGE = 25;

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

/** The listed repositories of the page, grouped by owner; totals count the whole group, search aside. */
function groupByOwner(shown: Repository[], listed: Repository[]) {
  const owners = [...new Set(shown.map((r) => r.owner))];
  return owners.map((owner) => {
    const all = listed.filter((r) => r.owner === owner);
    return {
      owner,
      rows: shown.filter((r) => r.owner === owner),
      total: all.length,
      on: all.filter((r) => r.pool_on).length,
    };
  });
}

/** The active types' words for the count: "private " (with the trailing space), empty while unknown. */
function typeWords(enabled: boolean | undefined, showPublic: boolean): string {
  if (enabled === undefined) return '';
  return enabled || showPublic ? 'private & public ' : 'private ';
}

export function RepositoriesList() {
  const location = useLocation();
  const client = useClient();
  const session = useSession();
  const navigate = useNavigate();
  const now = useNow();
  const v = useVersion('plans');
  const { result, reload } = useLoad(() => allRepositories(client), [v]);
  const { setting, enabled } = usePublicRepositories();
  const [q, setQ] = useState('');
  const showPublic = new URLSearchParams(useLocation().search).get('public') === '1';
  const [pool, setPool] = useState<'' | 'on' | 'off'>('');
  const [page, setPage] = useState(1);
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

  const offQ = useQuestion(
    async () => {
      const plan = await applyChanges(
        client,
        [...picked].map((id) => ({ repository_id: id, pool: 'off' as const })),
      );
      if (plan.state !== 'done') throw new Error('Not every repository was turned off.');
    },
    () => {
      setPicked(new Set());
      reload();
    },
  );

  const items =
    result.state === 'loading'
      ? undefined
      : (result as { data?: Awaited<ReturnType<typeof allRepositories>> }).data;
  const chosen = (items?.items ?? []).filter((r) => picked.has(r.id));
  // While the list is the last copy the admin flag is unknown, not "no": the
  // plan checks it again at the change (CRSP148, R5.19).
  const noAdmin = items?.stale.stale ? [] : chosen.filter((r) => !mayTry(r));
  const publicOff = chosen.filter((r) => publicWhileOff(r, enabled));
  const allUse = chosen.length > 0 && chosen.every((r) => r.pool_on);
  const blocked = !admin
    ? session.whyNot('admin')
    : chosen.length === 0
      ? 'Tick the repositories that should use the pool.'
      : !allUse && noAdmin.length > 0
        ? `You need admin rights on ${noAdmin.map((r) => r.full_name).join(', ')} in GitHub.`
        : !allUse && publicOff.length > 0
          ? `Public repositories are off: ${publicOff.map((r) => r.full_name).join(', ')}. Turn them on in Settings first.`
          : undefined;

  const columns: Column<Repository>[] = [
    {
      id: 'name',
      header: 'Repository',
      cell: (r) => (
        <Stack gap="tight">
          <Text>
            <TextLink to={`/repositories/${r.owner}/${r.name}`}>{r.name}</TextLink>
            {r.visibility !== 'private' || r.problem ? (
              <>
                {' '}
                <VisibilityCell r={r} inactive={publicWhileOff(r, enabled)} />
              </>
            ) : null}
          </Text>
          <Text tone="muted">
            {`${r.policy.max_parallel_jobs} parallel · priority ${r.policy.priority} · ${variableStatus(r).word}`}
          </Text>
        </Stack>
      ),
      sort: (r) => r.name,
    },
    {
      id: 'pool',
      header: 'Pool',
      cell: (r) => (
        <Status kind={r.pool_on ? 'ok' : 'off'} word={r.pool_on ? 'on' : 'off'} />
      ),
      sort: (r) => String(r.pool_on),
    },
    {
      id: 'where',
      header: 'Jobs go to',
      cell: (r) => <WhereJobsGo r={r} />,
      sort: (r) => r.routing?.where ?? '',
    },
    {
      id: 'runnerKind',
      header: 'Runner type',
      cell: (r) => (
        <Stack gap="tight">
          <Text>{runnerKindText(r.policy)}</Text>
          {r.pool_on && r.ephemeral_capacity === false ? (
            <Text tone="muted">{NO_CAPACITY}</Text>
          ) : null}
        </Stack>
      ),
      sort: (r) => r.policy.runner_kinds ?? '',
    },
    {
      id: 'delivery',
      header: 'Last delivery',
      cell: (r) =>
        r.pool_on ? (
          <DeliveryStatus r={r} />
        ) : (
          <Status
            kind="off"
            word={
              r.pool_off_reason === 'left_installation'
                ? 'left the installation'
                : 'pool off'
            }
          />
        ),
      sort: (r) => r.webhook?.last_delivery_at ?? '',
    },
  ];

  return (
    <Page>
      <PageHeader
        title="Repositories"
        lead="Choose which repositories use the pool, and with which limits."
      />
      {returned ? (
        <Message
          tone={returned === 'request' ? 'needs-you' : 'done'}
          action={
            returned === 'request' ? undefined : (
              <LinkButton to="/repositories">Use pool</LinkButton>
            )
          }
        >
          {returned === 'request'
            ? 'GitHub sent the install request to an owner of the account. The repositories show here when they approve it.'
            : 'GitHub saved the App installation. The list below is read again. Tick the repositories you want and press Use pool.'}
        </Message>
      ) : null}
      <Pane title="Repositories">
        <Load result={result} what="repositories" reload={reload}>
          {(data) => {
            // Only repositories of an active type are listed (LOOK D16); the rest are counted in a note.
            const unlisted = data.items.filter((r) => publicWhileOff(r, enabled));
            const listed = showPublic
              ? data.items
              : data.items.filter((r) => !publicWhileOff(r, enabled));
            const filtered = listed
              .filter(
                (r) =>
                  (!pool || (pool === 'on') === r.pool_on) &&
                  r.full_name.toLowerCase().includes(q.trim().toLowerCase()),
              )
              .sort((a, b) => a.owner.localeCompare(b.owner));
            const pages = Math.max(1, Math.ceil(filtered.length / PAGE));
            const shown = paginate(filtered, Math.min(page, pages), PAGE);
            const groups = groupByOwner(shown, listed);
            const selected = new Set([...picked].map(String));
            if (data.items.length === 0)
              return (
                <Empty
                  title="No repositories yet. Install the App on a repository to start."
                  action={<InstallAppButton label="Add a repository" onReturn={reload} />}
                />
              );
            return (
              <Stack>
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
                  <Message tone="note">
                    {showPublic ? (
                      <>
                        {`${unlisted.length} public ${unlisted.length === 1 ? 'repository is' : 'repositories are'} listed in grey: the pool serves private repositories only. `}
                        <TextLink to="/repositories">Hide them</TextLink>
                      </>
                    ) : (
                      <>
                        {`${unlisted.length} public ${unlisted.length === 1 ? 'repository is' : 'repositories are'} not listed: the pool serves private repositories only. `}
                        <TextLink to="/repositories?public=1">List them too</TextLink>
                      </>
                    )}
                  </Message>
                ) : null}
                <ListToolbar
                  label="Repositories"
                  search={
                    <SearchBar
                      value={q}
                      onChange={(x) => {
                        setQ(x);
                        setPage(1);
                      }}
                      label="Search repositories"
                    />
                  }
                  filters={
                    <>
                      <Segmented
                        label="Pool"
                        options={[
                          { value: 'on', label: 'Pool on' },
                          { value: 'off', label: 'Pool off' },
                        ]}
                        value={pool}
                        onChange={(x) => {
                          setPool(x);
                          setPage(1);
                        }}
                      />
                      {setting ? (
                        <Chips
                          label="Repository types"
                          options={[
                            {
                              value: 'types',
                              label: `Types: ${activeTypes(setting.enabled)}`,
                            },
                          ]}
                          value=""
                          onChange={() => void navigate(PUBLIC_SETTING_PATH)}
                        />
                      ) : null}
                    </>
                  }
                  selection={
                    <>
                      <Button
                        onPress={() => setPicked(new Set(filtered.map((r) => r.id)))}
                        disabled={filtered.length === 0}
                        reason="No repository is shown."
                      >
                        {`Select all ${filtered.length}`}
                      </Button>
                      <Button
                        onPress={() => setPicked(new Set())}
                        disabled={picked.size === 0}
                        reason="Nothing is selected."
                      >
                        Clear
                      </Button>
                    </>
                  }
                  count={`${filtered.length} ${typeWords(enabled, showPublic)}${filtered.length === 1 ? 'repository' : 'repositories'}`}
                  actions={
                    <InstallAppButton label="Add a repository" onReturn={reload} />
                  }
                />
                {filtered.length === 0 ? (
                  listed.length > 0 ? (
                    <Message tone="note">
                      {listed.length} repositories are hidden by the search and the
                      filter. Clear them to see all.
                    </Message>
                  ) : null
                ) : (
                  groups.map((g) => {
                    const rows = filtered.filter((r) => r.owner === g.owner);
                    return (
                      <Stack key={g.owner} as="section" gap="tight">
                        <CheckRow
                          label={g.owner}
                          help={`${g.total} ${g.total === 1 ? 'repository' : 'repositories'} · ${g.on} use the pool`}
                          checked={rows.every((r) => picked.has(r.id))}
                          onChange={(on) =>
                            setPicked((p) => {
                              const n = new Set(p);
                              for (const r of rows) {
                                if (on) n.add(r.id);
                                else n.delete(r.id);
                              }
                              return n;
                            })
                          }
                        />
                        <Table
                          caption={`${g.owner} repositories`}
                          columns={columns}
                          rows={g.rows}
                          rowKey={(r) => String(r.id)}
                          selected={selected}
                          onToggle={(key) =>
                            setPicked((p) => {
                              const n = new Set(p);
                              const id = Number(key);
                              if (n.has(id)) n.delete(id);
                              else n.add(id);
                              return n;
                            })
                          }
                          toggleLabel={(r) => `Select ${r.full_name}`}
                          inactive={(r) => publicWhileOff(r, enabled)}
                        />
                      </Stack>
                    );
                  })
                )}
                <Pager
                  page={Math.min(page, pages)}
                  pages={pages}
                  onPage={setPage}
                  label="Repository pages"
                />
              </Stack>
            );
          }}
        </Load>
      </Pane>
      <MissingPermissionsGuide onReady={reload} />
      <OrgPools version={v} />
      <ActionBar
        status={chosen.length === 0 ? 'Nothing selected' : selectedStatus(chosen.length)}
        primary={
          <Button
            kind="primary"
            disabled={blocked !== undefined}
            reason={blocked}
            onPress={() =>
              allUse
                ? offQ.ask(
                    chosen.flatMap((r) => [
                      `${r.full_name}: pool off`,
                      ...r.variables.map((x) => `${r.full_name}: variable ${x.name}`),
                    ]),
                  )
                : void navigate(
                    `/repositories/turn-on?${chosen.map((r) => `repo=${r.id}`).join('&')}`,
                  )
            }
          >
            {allUse ? 'Stop using pool' : 'Use pool'}
          </Button>
        }
      />
      <Ask
        title="Turn off these repositories?"
        action="Turn off"
        busyLabel="Turning off"
        q={offQ.question}
        busy={offQ.busy}
        error={offQ.error}
        onCancel={offQ.cancel}
        onConfirm={() => void offQ.confirm()}
      />
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
          { to: '/repositories', label: 'Repositories' },
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
              onPress={() => void navigate(`/repositories/turn-on?repo=${r.id}`)}
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
                <Status kind={r.pool_on ? 'ok' : 'off'} word={r.pool_on ? 'on' : 'off'} />
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
                  action={<LinkButton to="/pool/runners/add">Add runners</LinkButton>}
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

/** The Visibility cell: the word, and for a repository with a Problem, the Problem and its link. The public-off reason is not repeated per row (LOOK4). */
function VisibilityCell({ r, inactive }: { r: Repository; inactive: boolean }) {
  const word = (
    <>
      {visibilityWord(r)}
      {inactive ? (
        <>
          {' '}
          <Tag>inactive type</Tag>
        </>
      ) : null}
    </>
  );
  if (!r.problem) return word;
  return (
    <Stack gap="tight">
      <Text>{word}</Text>
      <Text>
        {r.problem.text}{' '}
        <TextLink to={`/repositories/${r.owner}/${r.name}`}>Fix it</TextLink>
      </Text>
    </Stack>
  );
}
