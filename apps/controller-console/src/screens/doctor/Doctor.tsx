// Doctor (web-console 4.8, R5.26): findings grouped by area with a status word (UX-58), what is wrong and its fix,
// the time of the last run (UX-53), and "Run checks again" as the one primary, in the action bar (UX-71); a fix button in a row is a normal button (D27). It is busy while the run lasts
// and shows its progress (UX-13, UX-29); the busy state is the server's run state, not a timer (UX-19).
import { consoleAddress } from './serverLinks';
import {
  Badge,
  BulletList,
  Button,
  Card,
  CardGrid,
  CodeBlock,
  Cluster,
  Empty,
  LinkButton,
  Message,
  Page,
  PageHeader,
  Progress,
  Section,
  Stack,
  Status,
  Table,
  Text,
  formatCount,
  formatPast,
  formatSize,
  useNow,
  type Column,
  type StatusKind,
  ActionBar,
} from '@crsp/ui';
import { useEffect, useState } from 'react';
import { unwrap, useClient, type Schemas } from '../../api/api';
import { useCommand, useLoad, useQuestion } from '../../api/hooks';
import { useSession } from '../../auth/session';
import { useVersion } from '../../live/LiveProvider';
import { Ask, Load } from '../common';

type Finding = Schemas['DoctorFinding'];
type Run = Schemas['DoctorRun'];
type Severity = Schemas['Severity'];
type Leftover = Schemas['RunnerLeftover'];
type LeftoverResult = Schemas['RemoveRunnerLeftoversResult']['results'][number];

const KIND_WORD: Record<Leftover['kind'], string> = {
  service: 'Service',
  folder: 'Folder',
  account: 'Account',
  download: 'Download',
};

const OUTCOME_WORD: Record<LeftoverResult['outcome'], string> = {
  removed: 'Removed',
  already_gone: 'Already gone',
  refused: 'Refused',
  failed: 'Failed',
};

/** What the question card and the list say of one leftover: its kind, its name and its size. */
export function leftoverText(i: Pick<Leftover, 'kind' | 'name' | 'size_bytes'>): string {
  const size = i.size_bytes === undefined ? '' : ` (${formatSize(i.size_bytes)})`;
  return `${KIND_WORD[i.kind]} ${i.name}${size}`;
}

/** The list of leftovers inside a finding row (doctor R5.48): kind, name, size, account and why. */
function LeftoverList({ items }: { items: readonly Leftover[] }) {
  return (
    <BulletList
      label="Runner leftovers"
      items={items.map(
        (i) => `${leftoverText(i)}${i.account ? `, account ${i.account}` : ''}. ${i.why}`,
      )}
    />
  );
}

/** The Remove button of a runner-leftovers finding, its question card (UX-9, UX-62) and the result per item. */
function RemoveLeftovers({
  finding,
  results,
  onResults,
  onRemoved,
}: {
  finding: Finding;
  results: readonly LeftoverResult[];
  onResults: (machineId: string, name: string, r: LeftoverResult[]) => void;
  onRemoved: (machineId: string) => void;
}) {
  const client = useClient();
  const session = useSession();
  const machineId = finding.subject.id;
  const items = finding.items ?? [];
  const noRights = finding.needs_admin;
  const admin = session.can('admin');
  const ask = useQuestion(
    async () => {
      const r = await unwrap(
        client.POST('/api/v1/machines/{machineId}/runner-leftovers/remove', {
          params: { path: { machineId } },
          body: { items: items.map((i) => ({ kind: i.kind, name: i.name })) },
        }),
      );
      onResults(machineId, finding.subject.name, r.results);
    },
    () => onRemoved(machineId),
  );
  const reason = noRights
    ? 'The agent has no administrator rights. Run it as a service with rights.'
    : !admin
      ? session.whyNot('admin')
      : undefined;
  return (
    <Stack gap="tight">
      <Button
        onPress={() =>
          ask.ask(
            items.map((i) => leftoverText(i)),
            [],
          )
        }
        busy={ask.busy}
        busyLabel="Removing"
        disabled={noRights || !admin}
        reason={reason}
      >
        {finding.action?.label ?? 'Remove'}
      </Button>
      {results.length > 0 ? (
        <BulletList
          label="Removal results"
          items={results.map(
            (r) =>
              `${KIND_WORD[r.kind]} ${r.name}: ${OUTCOME_WORD[r.outcome]}${r.error ? `. ${r.error}` : ''}`,
          )}
        />
      ) : null}
      <Ask
        title={`Remove these leftovers from ${finding.subject.name}?`}
        intro="Each item below is removed from this machine. This cannot be undone."
        action="Remove"
        busyLabel="Removing"
        q={ask.question}
        busy={ask.busy}
        error={ask.error}
        onCancel={ask.cancel}
        onConfirm={() => void ask.confirm()}
      />
    </Stack>
  );
}

/** The Remove button of a half-registered runner (doctor R5.50): a GitHub runner no machine runs, or a runner record
 * GitHub does not list. It asks first (UX-9, UX-62); the server reads GitHub and the records again before it removes. */
function RemoveRegistration({
  finding,
  onRemoved,
}: {
  finding: Finding;
  onRemoved: (check: string) => void;
}) {
  const client = useClient();
  const session = useSession();
  const admin = session.can('admin');
  const action = finding.action;
  const ask = useQuestion(
    async () => {
      if (!action?.args) return;
      await unwrap(
        client.POST('/api/v1/doctor/registration-fixes', {
          params: { query: { confirm: true } },
          body: {
            action:
              action.id === 'remove_runner_record'
                ? 'remove_runner_record'
                : 'remove_github_runner',
            args: action.args,
          },
        }),
      );
    },
    () => onRemoved(finding.check),
  );
  const isRecord = action?.id === 'remove_runner_record';
  const name = action?.args?.name ?? finding.subject.name;
  return (
    <Stack gap="tight">
      <Button
        onPress={() =>
          ask.ask([
            isRecord
              ? `The record of runner ${name} in crsp, and its service and folder on the machine`
              : `The registration of runner ${name} on GitHub`,
          ])
        }
        busy={ask.busy}
        busyLabel="Removing"
        disabled={!admin}
        reason={!admin ? session.whyNot('admin') : undefined}
      >
        {action?.label ?? 'Remove'}
      </Button>
      <Ask
        title={isRecord ? `Remove the record of ${name}?` : `Remove ${name} from GitHub?`}
        intro={
          isRecord
            ? 'GitHub does not list this runner, so it cannot take jobs. Its record is removed from crsp.'
            : 'No machine runs this runner. Only this registration is removed from GitHub.'
        }
        action="Remove"
        busyLabel="Removing"
        q={ask.question}
        busy={ask.busy}
        error={ask.error}
        onCancel={ask.cancel}
        onConfirm={() => void ask.confirm()}
      />
    </Stack>
  );
}

/** The one-click fix of `runner.logon` (runners R5.89, doctor R5.45): the agent gives the account the right to log
 * on as a service again and starts the service. It is the call the Problem banner makes. */
function StartRunnerService({ machineId }: { machineId: string }) {
  const client = useClient();
  const session = useSession();
  const admin = session.can('admin');
  const start = useCommand(async () => {
    await unwrap(
      client.POST('/api/v1/machines/{machineId}/redetect', {
        params: { path: { machineId } },
      }),
    );
  });
  return (
    <Button
      onPress={() => void start.run()}
      busy={start.busy}
      busyLabel="Starting"
      disabled={!admin}
      reason={!admin ? session.whyNot('admin') : undefined}
    >
      Start
    </Button>
  );
}

/** How often a running run is asked again when no live event came. */
export const POLL_MS = 3000;

/** The areas of the screen, in order; a check id starts with its area (doctor spec). */
const AREAS: [string, string][] = [
  ['machine', 'Machines'],
  ['backend', 'Backends'],
  ['runner', 'Runners'],
  ['github', 'GitHub registration'],
  ['repo', 'Repository variables'],
  ['workflow', 'Workflow labels'],
  ['controller', 'Controller'],
];

export function areaOf(check: string): string {
  const prefix = check.split('.')[0] ?? check;
  return (
    AREAS.find(([p]) => p === prefix)?.[1] ??
    prefix.charAt(0).toUpperCase() + prefix.slice(1)
  );
}

/** UX-58: every severity is a word, a glyph and a colour. */
export function severityStatus(s: Severity): { kind: StatusKind; word: string } {
  switch (s) {
    case 'ok':
      return { kind: 'ok', word: 'ok' };
    case 'fail':
      return { kind: 'failed', word: 'failed' };
    case 'warn':
      return { kind: 'waiting', word: 'warning' };
    case 'skip':
      return { kind: 'off', word: 'skipped' };
    default:
      return { kind: 'info', word: 'info' };
  }
}

const isProblem = (f: Finding) => f.severity === 'fail' || f.severity === 'warn';

/** The groups in screen order, each with the findings that are not simply ok. */
const needsYouColumns: Column<Finding>[] = [
  { id: 'what', header: 'What is wrong', cell: (f) => f.title, kind: 'grow', grow: 3 },
  { id: 'area', header: 'Area', cell: (f) => areaOf(f.check), kind: 'fit' },
];

/** Where the Open button of a Needs-you row goes: the fix link of the finding, else the Setup page of its area. */
const AREA_PAGE: Record<string, string> = {
  machine: '/setup/machines',
  backend: '/setup/size',
  runner: '/setup/runners',
  github: '/setup/pool',
  repo: '/setup/pool',
  workflow: '/setup/pool',
  controller: '/settings',
};

export function openTarget(f: Pick<Finding, 'check' | 'fix'>): string {
  if (f.fix.link?.startsWith('/')) return consoleAddress(f.fix.link);
  return AREA_PAGE[f.check.split('.')[0] ?? ''] ?? '/doctor';
}

export interface AreaChecks {
  area: string;
  total: number;
  problems: number;
  worst: Severity;
}

/** One line per area for the Checks pane: how many checks ran and how many are not ok. */
export function areaChecks(items: readonly Finding[]): AreaChecks[] {
  const order = AREAS.map(([, name]) => name);
  const by = new Map<string, AreaChecks>();
  for (const f of items) {
    const area = areaOf(f.check);
    const row = by.get(area) ?? { area, total: 0, problems: 0, worst: 'ok' as Severity };
    row.total += 1;
    if (isProblem(f)) {
      row.problems += 1;
      if (f.severity === 'fail' || row.worst !== 'fail') row.worst = f.severity;
    }
    by.set(area, row);
  }
  return [...by.values()].sort((a, b) => {
    const x = order.indexOf(a.area);
    const y = order.indexOf(b.area);
    return (x < 0 ? 99 : x) - (y < 0 ? 99 : y) || a.area.localeCompare(b.area);
  });
}

export function groupFindings(
  items: readonly Finding[],
): { area: string; items: Finding[] }[] {
  const order = [...AREAS.map(([, name]) => name)];
  const groups = new Map<string, Finding[]>();
  for (const f of items) {
    if (f.severity === 'ok') continue;
    const area = areaOf(f.check);
    groups.set(area, [...(groups.get(area) ?? []), f]);
  }
  return [...groups.entries()]
    .sort(([a], [b]) => {
      const x = order.indexOf(a);
      const y = order.indexOf(b);
      return (x < 0 ? 99 : x) - (y < 0 ? 99 : y) || a.localeCompare(b);
    })
    .map(([area, list]) => ({ area, items: list }));
}

export function DoctorScreen() {
  const client = useClient();
  const session = useSession();
  const now = useNow();
  const v = useVersion('doctor');
  const admin = session.can('admin');
  const why = session.whyNot('admin');
  const { result, reload } = useLoad(
    () => unwrap(client.GET('/api/v1/doctor/findings', { params: { query: {} } })),
    [v],
  );
  const [run, setRun] = useState<Run | undefined>(undefined);
  const start = useCommand(async (scope?: Schemas['DoctorScope']) => {
    const r = await unwrap(
      client.POST('/api/v1/doctor/runs', { body: scope ? { scope } : {} }),
    );
    setRun(r);
    return r;
  });
  // The result of each removal of runner leftovers, by machine, until the next removal on that machine.
  const [removals, setRemovals] = useState<
    Record<string, { name: string; results: LeftoverResult[] }>
  >({});
  const running = run?.state === 'running';
  const hasRun = result.state === 'ready' && Boolean(result.data.last_run_at);

  // The run's own state is read from the server: on every live doctor event, and on a slow poll as a fallback.
  const runId = run?.id;
  useEffect(() => {
    if (!runId || !running) return;
    let live = true;
    const ask = () =>
      unwrap(client.GET('/api/v1/doctor/runs/{runId}', { params: { path: { runId } } }))
        .then((r) => {
          if (!live) return;
          setRun(r);
          if (r.state !== 'running') reload();
        })
        .catch(() => undefined);
    void ask();
    const timer = setInterval(() => void ask(), POLL_MS);
    return () => {
      live = false;
      clearInterval(timer);
    };
    // `v` makes a live doctor event ask again at once.
  }, [runId, running, v, client]);

  const columns: Column<Finding>[] = [
    {
      id: 'status',
      header: 'Status',
      cell: (f) => {
        const s = severityStatus(f.severity);
        return <Status kind={s.kind} word={s.word} />;
      },
      sort: (f) => f.severity,
    },
    {
      id: 'what',
      header: 'What is wrong',
      cell: (f) => (
        <Stack gap="tight">
          <span>{f.title}</span>
          <Text tone="muted">{f.subject.name}</Text>
          {f.items && f.items.length > 0 ? <LeftoverList items={f.items} /> : null}
        </Stack>
      ),
      long: (f) => (f.detail ? { full: f.detail } : null),
    },
    {
      id: 'fix',
      header: 'Fix',
      cell: (f) => (
        <Stack gap="tight">
          <span>{f.fix.text}</span>
          {f.action?.id === 'remove_runner_leftovers' && f.items ? (
            <RemoveLeftovers
              finding={f}
              results={removals[f.subject.id]?.results ?? []}
              onResults={(m, name, results) =>
                setRemovals((prev) => ({ ...prev, [m]: { name, results } }))
              }
              onRemoved={(m) =>
                void start.run({ machines: [m], checks: ['machine.runner_leftovers'] })
              }
            />
          ) : null}
          {(f.action?.id === 'remove_github_runner' ||
            f.action?.id === 'remove_runner_record') &&
          f.action.args ? (
            <RemoveRegistration
              finding={f}
              onRemoved={(check) => void start.run({ checks: [check] })}
            />
          ) : null}
          {f.fix.command ? <CodeBlock text={f.fix.command} label="Fix command" /> : null}
          {f.fix.link?.startsWith('/') ? (
            <Cluster>
              <LinkButton to={consoleAddress(f.fix.link)}>Open</LinkButton>
              {f.check === 'runner.logon' && f.severity === 'fail' ? (
                <StartRunnerService
                  machineId={consoleAddress(f.fix.link).slice('/setup/machines/'.length)}
                />
              ) : null}
            </Cluster>
          ) : null}
          {f.needs_admin && !admin ? <Badge>needs admin</Badge> : null}
        </Stack>
      ),
    },
  ];

  return (
    <Page>
      <PageHeader title="Doctor" lead="Every problem the pool has, with its fix." />
      {start.error ? (
        <Message tone="problem">
          Could not run the doctor. {start.error} Try again.
        </Message>
      ) : null}
      {running ? (
        <Stack gap="tight">
          <Text>
            Running the doctor.{' '}
            {formatCount(
              Object.values(run?.counts ?? {}).reduce((a, b) => a + b, 0),
              'check',
            )}{' '}
            done so far.
          </Text>
          <Progress label="Running the doctor" />
        </Stack>
      ) : null}
      {run?.state === 'failed' ? (
        <Message tone="problem">
          The doctor run failed. Run the doctor again. If it fails again, look at the
          controller log.
        </Message>
      ) : null}
      <Load result={result} what="the doctor findings" reload={reload}>
        {(data) => {
          const problems = data.items.filter(isProblem);
          const groups = groupFindings(data.items);
          const areas = areaChecks(data.items);
          if (data.items.length === 0 && !data.last_run_at)
            return (
              <Section title="Findings">
                <Empty title="No checks have run yet. Use Run checks below to look for problems." />
              </Section>
            );
          const last = data.last_run_at ? formatPast(data.last_run_at, now) : undefined;
          return (
            <Stack>
              {last ? <Text tone="muted">{`Last run ${last}.`}</Text> : null}
              {problems.length === 0 ? (
                <Message tone="done">
                  No problems found.{last ? ` The doctor ran ${last}.` : ''}
                </Message>
              ) : (
                <Message tone="needs-you">
                  {`${formatCount(problems.length, 'problem')} found. Each one has its fix below.`}
                </Message>
              )}
              {Object.entries(removals)
                .filter(
                  ([m]) =>
                    !data.items.some((f) => f.subject.id === m && f.action !== undefined),
                )
                .map(([m, { name, results }]) => (
                  <Message key={m} tone="done">
                    {`Runner leftovers on ${name}: ${results
                      .map(
                        (r) =>
                          `${KIND_WORD[r.kind]} ${r.name} ${OUTCOME_WORD[r.outcome].toLowerCase()}`,
                      )
                      .join(', ')}.`}
                  </Message>
                ))}
              {problems.length > 0 ? (
                <Section
                  title="Needs you"
                  count={problems.length}
                  hint="What is wrong now, first. The fix of each is in its area below."
                >
                  <Table
                    caption="Needs you"
                    columns={needsYouColumns}
                    rows={problems}
                    rowKey={(f) => `needs:${f.check}:${f.subject.id}`}
                    action={{
                      label: 'Open',
                      to: (f) => openTarget(f),
                      name: (f) => f.title,
                    }}
                  />
                </Section>
              ) : null}
              <Section title="Checks" count={areas.length}>
                <CardGrid>
                  {areas.map((a) => (
                    <Card key={a.area} title={a.area}>
                      {a.problems === 0 ? (
                        <Status kind="ok" word={`${a.total} of ${a.total} ok`} />
                      ) : (
                        <Status
                          kind={a.worst === 'fail' ? 'failed' : 'waiting'}
                          word={`${formatCount(a.problems, 'problem')} in ${formatCount(a.total, 'check')}`}
                        />
                      )}
                    </Card>
                  ))}
                </CardGrid>
              </Section>
              {groups.map((g) => (
                <Section key={g.area} title={`Fix: ${g.area}`}>
                  <Table
                    caption={`Fix: ${g.area}`}
                    columns={columns}
                    rows={g.items}
                    rowKey={(f) => `${f.check}:${f.subject.id}`}
                    expandLabel="Details"
                    rowLabel={(f) => f.title}
                  />
                </Section>
              ))}
            </Stack>
          );
        }}
      </Load>
      <ActionBar
        primary={
          <Button
            kind="primary"
            busy={running || start.busy}
            busyLabel="Running the doctor"
            disabled={!admin}
            reason={why}
            onPress={() => void start.run()}
          >
            {hasRun ? 'Run checks again' : 'Run checks'}
          </Button>
        }
      />
    </Page>
  );
}
