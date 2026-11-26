// Runners (web-console 4.5, R5.23): live list with isolation named (UX-61), delete, replace a runner that is
// not managed.
import {
  Button,
  Chips,
  Cluster,
  Empty,
  LinkButton,
  Message,
  Page,
  Section,
  ListToolbar,
  SearchBar,
  Stack,
  Status,
  Table,
  Tag,
  Text,
  TextLink,
  formatDuration,
  SummaryLine,
  type SummaryItem,
  useNow,
  type Column,
  type StatusKind,
  ActionBar,
} from '@crsp/ui';
import { useEffect, useState } from 'react';
import { useNavigate, useSearchParams } from 'react-router-dom';
import { ApiError, unwrap, useClient, type Schemas } from '../../api/api';
import { useLoad, useQuestion } from '../../api/hooks';
import { useSession } from '../../auth/session';
import { useVersion } from '../../live/LiveProvider';
import { Ask, Load } from '../common';
import { needsYes } from '../repositories/plans';
import { KIND_NAMES, OS_WORD, oneStop } from './kinds';
import { keepsText, uncleanText } from './RunnerPage';
import { OptionBar } from './RunnerFilters';

type Runner = Schemas['Runner'];
type SizeClass = NonNullable<Runner['size_class']>;

/** LOOK71: the type tag is the isolation word alone; the long UX-61 text is the hover text. */
export function typeTag(r: Pick<Runner, 'isolation'>): string {
  return r.isolation;
}

/** UX-61: every runner names its isolation; a persistent runner also says its state is kept between jobs. */
export function isolationText(r: Pick<Runner, 'isolation' | 'keeps_state'>): string {
  return r.isolation === 'persistent' || r.keeps_state
    ? `${r.isolation}, state kept between jobs`
    : r.isolation;
}

export function runnerStatus(r: Runner): { kind: StatusKind; word: string } {
  // A runner whose clean-up failed takes no new job, whatever else it is (runners R5.90).
  if (r.unclean_reason) return { kind: 'waiting', word: 'not clean' };
  switch (r.state) {
    case 'idle':
      return { kind: 'ok', word: 'idle' };
    case 'busy':
      return { kind: 'running', word: 'busy' };
    case 'failed':
      return { kind: 'failed', word: 'failed' };
    case 'offline':
      return { kind: 'failed', word: 'offline' };
    case 'not_registered':
      return { kind: 'failed', word: 'not registered' };
    case 'stopped':
      return { kind: 'failed', word: 'stopped' };
    case 'unknown':
      return { kind: 'waiting', word: 'unknown' };
    default:
      return { kind: 'waiting', word: r.state };
  }
}

/** RES7: the running job's repository and name, and how long it has run; "none" for a runner with no job. */
export function currentJobText(r: Runner, now: number): string {
  const j = r.current_job;
  if (!j) return 'none';
  const text = `${j.repository} ${j.job_name}`;
  if (!j.started_at) return text;
  const seconds = Math.max(0, (now - Date.parse(j.started_at)) / 1000);
  return `${text}, running ${formatDuration(seconds)}`;
}

/** RES7: how many runners are idle and busy, at a glance. */
export function stateCounts(items: readonly Runner[]): {
  idle: number;
  busy: number;
  other: number;
} {
  const idle = items.filter((r) => r.state === 'idle').length;
  const busy = items.filter((r) => r.state === 'busy').length;
  return { idle, busy, other: items.length - idle - busy };
}

/** CRSP298: the pool above the list as one summary line, "1 machine · 5 runners — 4 idle · 0 busy · 1 not ready" (UX-86). */
export function runnerSummary(items: readonly Runner[]): {
  total: SummaryItem[];
  states: SummaryItem[];
} {
  const machines = new Set(items.map((r) => r.machine_id)).size;
  const c = stateCounts(items);
  return {
    total: [
      { count: machines, word: 'machine', plural: 'machines' },
      { count: items.length, word: 'runner', plural: 'runners' },
    ],
    states: [
      { count: c.idle, word: 'idle' },
      { count: c.busy, word: 'busy' },
      { count: c.other, word: 'not ready', tone: 'warning' },
    ],
  };
}

/** D32: the runners of each machine together, machines in name order. */
export function groupByMachine<T extends Pick<Runner, 'machine_id' | 'machine_name'>>(
  rows: readonly T[],
): { id: string; name: string; rows: T[] }[] {
  const by = new Map<string, { id: string; name: string; rows: T[] }>();
  for (const r of rows) {
    const g = by.get(r.machine_id) ?? {
      id: r.machine_id,
      name: r.machine_name,
      rows: [],
    };
    g.rows.push(r);
    by.set(r.machine_id, g);
  }
  return [...by.values()].sort((a, b) => a.name.localeCompare(b.name));
}

export function failedWords(n: number): string {
  return `${n} ${n === 1 ? 'runner' : 'runners'} failed. The State column says why. Remove a failed runner with Remove, then add runners again.`;
}

const LEAVING_POLL_MS = 3000;

export function RunnersList() {
  const client = useClient();
  const navigate = useNavigate();
  const session = useSession();
  const now = useNow();
  const v = useVersion('runners');
  const { result, reload } = useLoad(() => unwrap(client.GET('/api/v1/runners')), [v]);
  // A runner on its way out (draining, removing) is read again every few seconds, so the row goes
  // without a reload even when the live stream is quiet (OPS19).
  const leaving =
    result.state === 'ready' &&
    result.data.items.some((r) => r.state === 'draining' || r.state === 'removing');
  useEffect(() => {
    if (!leaving) return;
    const t = setInterval(reload, LEAVING_POLL_MS);
    return () => clearInterval(t);
  }, [leaving, reload]);
  const [q, setQ] = useState('');
  const [os, setOs] = useState<'' | 'linux' | 'windows' | 'macos'>('');
  const [iso, setIso] = useState<'' | Exclude<Schemas['Isolation'], 'persistent'>>('');
  const [kind, setKind] = useState<'' | Schemas['RunnerKind']>('');
  const [size, setSize] = useState<'' | SizeClass>('');
  const [picked, setPicked] = useState<Set<string>>(new Set());
  // CRSP299 (temporary): ?option=1..3 shows one of three filter bar designs; without it, the current bar.
  const option = useSearchParams()[0].get('option');
  const admin = session.can('admin');
  const why = session.whyNot('admin');

  const del = useQuestion(
    async () => {
      for (const id of picked) {
        try {
          await unwrap(
            client.DELETE('/api/v1/runners/{runnerId}', {
              params: { path: { runnerId: id }, query: { confirm: true } },
            }),
          );
        } catch (e) {
          // A runner that is already gone is what the person asked for: not a problem (OPS19).
          if (e instanceof ApiError && e.code === 'not_found') continue;
          throw e;
        }
      }
    },
    () => {
      setPicked(new Set());
      reload();
    },
  );

  const [replacing, setReplacing] = useState<Runner | undefined>(undefined);
  const replace = useQuestion(
    async () => {
      if (!replacing) return;
      const plan = await unwrap(
        client.POST('/api/v1/runners/{runnerId}/replace', {
          params: { path: { runnerId: replacing.id } },
        }),
      );
      await unwrap(
        client.POST('/api/v1/runner-plans/{planId}/apply', {
          params: { path: { planId: plan.id } },
          body: { approved_steps: needsYes(plan) },
        }),
      );
    },
    () => {
      setReplacing(undefined);
      reload();
    },
  );

  // github-integration R5.33: the yes path for an offline runner whose machine cannot remove it.
  const [cleaning, setCleaning] = useState<Runner | undefined>(undefined);
  const cleanup = useQuestion(
    async () => {
      if (!cleaning) return;
      await unwrap(
        client.DELETE('/api/v1/runners/{runnerId}/registration', {
          params: { path: { runnerId: cleaning.id }, query: { confirm: true } },
        }),
      );
    },
    () => {
      setCleaning(undefined);
      reload();
    },
  );

  const columns: Column<Runner>[] = [
    {
      id: 'name',
      header: 'Name',
      cell: (r) => <TextLink to={`/setup/runners/${r.id}`}>{r.name}</TextLink>,
      title: (r) => r.name,
      grow: 2,
      sort: (r) => r.name,
    },
    {
      id: 'iso',
      header: 'Type',
      cell: (r) => <Tag>{typeTag(r)}</Tag>,
      kind: 'fit',
      // The whole words of UX-61 are the hover text; the backend and the kind are on the runner's page (UX-72).
      title: (r) =>
        [
          isolationText(r),
          `${r.os} ${r.arch}, ${r.backend}`,
          [KIND_NAMES[r.kind], keepsText(r)].filter(Boolean).join('. '),
        ].join('. '),
      sort: (r) => r.isolation,
    },
    {
      id: 'size',
      header: 'Size',
      cell: (r) => (r.size_class ? <Tag>{r.size_class}</Tag> : 'not set'),
      kind: 'fit',
      title: (r) => r.size_class ?? 'No size class is set for this runner.',
      sort: (r) => r.size_class ?? '',
    },
    {
      id: 'system',
      header: 'System',
      cell: (r) => <Tag>{OS_WORD[r.os]}</Tag>,
      kind: 'fit',
      title: (r) => `${OS_WORD[r.os]} ${r.arch}`,
      sort: (r) => r.os,
    },
    {
      id: 'state',
      header: 'State',
      cell: (r) => {
        const s = runnerStatus(r);
        return <Status kind={s.kind} word={s.word} />;
      },
      grow: 1,
      long: (r) => {
        const full = r.unclean_reason
          ? uncleanText(r.unclean_reason)
          : (r.reason ?? (r.state === 'failed' ? r.failed_reason : null));
        return full ? { full: oneStop(full) } : null;
      },
      sort: (r) => r.state,
    },
    {
      id: 'job',
      header: 'Current job',
      cell: (r) => currentJobText(r, now),
      kind: 'grow',
      title: (r) => currentJobText(r, now),
      secondary: true,
    },
    {
      id: 'managed',
      header: 'Managed',
      kind: 'fit',
      cell: (r) =>
        r.managed ? (
          r.state === 'offline' ? (
            <Button
              onPress={() => {
                cleanup.cancel();
                setCleaning(r);
                cleanup.ask(
                  [
                    `The registration of offline runner ${r.name} on GitHub.`,
                    `Its record here, and its folder if its machine answers.`,
                  ],
                  [],
                );
              }}
              disabled={!admin}
              reason={why}
            >
              Delete registration
            </Button>
          ) : (
            'yes'
          )
        ) : (
          <Button
            onPress={() => {
              replace.cancel();
              setReplacing(r);
              replace.ask(
                [
                  `Runner ${r.name} is replaced by one this pool manages.`,
                  `The old runner is removed after the new one is ready.`,
                ],
                r.current_job
                  ? [`${r.current_job.repository} ${r.current_job.job_name}`]
                  : [],
              );
            }}
            disabled={!admin}
            reason={why}
          >
            Replace
          </Button>
        ),
    },
  ];

  const chosen = (result.state === 'ready' ? result.data.items : []).filter((r) =>
    picked.has(r.id),
  );

  return (
    <Page>
      <Text tone="muted">
        The runners of the pool, by machine. They serve every repository that uses the
        pool.
      </Text>
      <Load result={result} what="runners" reload={reload}>
        {(data) => {
          const rows = data.items.filter(
            (r) =>
              (!os || r.os === os) &&
              (!iso || r.isolation === iso) &&
              (!kind || r.kind === kind) &&
              (!size || r.size_class === size) &&
              `${r.name} ${r.machine_name} ${r.labels.join(' ')}`
                .toLowerCase()
                .includes(q.trim().toLowerCase()),
          );
          const selection = (
            <>
              <Button onPress={() => setPicked(new Set(rows.map((r) => r.id)))}>
                {`Select all ${rows.length}`}
              </Button>
              <Button
                onPress={() => setPicked(new Set())}
                disabled={picked.size === 0}
                title="Nothing is selected."
              >
                Clear
              </Button>
              {picked.size > 0 ? (
                <>
                  <span>{`${picked.size} selected`}</span>
                  <Button
                    kind="danger"
                    disabled={!admin}
                    reason={why}
                    onPress={() =>
                      del.ask(
                        chosen.map((r) => `Runner ${r.name}`),
                        chosen.flatMap((r) =>
                          r.current_job
                            ? [`${r.current_job.repository} ${r.current_job.job_name}`]
                            : [],
                        ),
                      )
                    }
                  >
                    {`Delete ${chosen.length} ${chosen.length === 1 ? 'runner' : 'runners'}`}
                  </Button>
                </>
              ) : null}
            </>
          );
          if (data.items.length === 0)
            return (
              <Section title="Runners" count={0}>
                <Empty title="No runners yet. Use Add runners at the top to start." />
              </Section>
            );
          return (
            <Stack>
              <SummaryLine {...runnerSummary(data.items)} />
              {data.items.some((r) => r.state === 'failed') ? (
                <Message
                  tone="needs-you"
                  action={<LinkButton to="/setup/runners/add">Add runners</LinkButton>}
                >
                  {failedWords(data.items.filter((r) => r.state === 'failed').length)}
                </Message>
              ) : null}
              <Section title="Runners" count={rows.length}>
                <Stack>
                  {option === '1' || option === '2' || option === '3' ? (
                    <OptionBar
                      which={option}
                      state={{ q, os, iso, kind, size }}
                      set={(k, v) => {
                        if (k === 'q') setQ(v);
                        else if (k === 'os') setOs(v as typeof os);
                        else if (k === 'iso') setIso(v as typeof iso);
                        else if (k === 'kind') setKind(v as typeof kind);
                        else setSize(v as typeof size);
                      }}
                      shown={rows.length}
                      selection={selection}
                    />
                  ) : (
                    <ListToolbar
                      label="Runners"
                      search={
                        <SearchBar value={q} onChange={setQ} label="Search runners" />
                      }
                      filters={
                        <Stack>
                          <Cluster>
                            <Text tone="muted">System</Text>
                            <Chips
                              label="System"
                              options={[
                                { value: 'linux', label: 'Linux' },
                                { value: 'windows', label: 'Windows' },
                                { value: 'macos', label: 'macOS' },
                              ]}
                              value={os}
                              onChange={setOs}
                            />
                          </Cluster>
                          <Cluster>
                            <Text tone="muted">Isolation</Text>
                            <Chips
                              label="Isolation"
                              options={[
                                { value: 'container', label: 'container' },
                                { value: 'VM', label: 'VM' },
                                { value: 'microVM', label: 'microVM' },
                              ]}
                              value={iso}
                              onChange={setIso}
                            />
                          </Cluster>
                          <Cluster>
                            <Text tone="muted">Kind</Text>
                            <Chips
                              label="Kind"
                              options={[
                                { value: 'persistent', label: 'persistent' },
                                { value: 'ephemeral', label: 'ephemeral' },
                              ]}
                              value={kind}
                              onChange={setKind}
                            />
                          </Cluster>
                          <Cluster>
                            <Text tone="muted">Size</Text>
                            <Chips
                              label="Size"
                              options={[
                                { value: 'small', label: 'small' },
                                { value: 'medium', label: 'medium' },
                                { value: 'large', label: 'large' },
                                { value: 'xl', label: 'xl' },
                              ]}
                              value={size}
                              onChange={setSize}
                            />
                          </Cluster>
                        </Stack>
                      }
                      selection={selection}
                    />
                  )}
                  {rows.length === 0 ? (
                    <Message
                      tone="note"
                      action={
                        <Button
                          onPress={() => {
                            setQ('');
                            setOs('');
                            setIso('');
                            setKind('');
                            setSize('');
                          }}
                        >
                          Clear search and filters
                        </Button>
                      }
                    >
                      {data.items.length} runners are hidden by the search and the
                      filters. Clear them to see all.
                    </Message>
                  ) : null}
                </Stack>
              </Section>
              {rows.length > 0 ? (
                <Stack>
                  {groupByMachine(rows).map((g) => (
                    <Section
                      key={g.id}
                      title={`Machine ${g.name}`}
                      count={g.rows.length}
                      actions={
                        <LinkButton to={`/setup/machines/${g.id}`}>
                          {`Open ${g.name}`}
                        </LinkButton>
                      }
                    >
                      <Table
                        caption={`Runners on ${g.name}`}
                        columns={columns}
                        rows={g.rows}
                        rowKey={(r) => r.id}
                        expandLabel="Full error"
                        rowLabel={(r) => r.name}
                        oneLine
                        action={{
                          label: 'Remove',
                          name: (r) => r.name,
                          press: (r) =>
                            r.managed && r.state === 'failed'
                              ? {
                                  kind: 'danger',
                                  onPress: () => {
                                    setPicked(new Set([r.id]));
                                    del.ask([`Runner ${r.name}`], []);
                                  },
                                  disabled: !admin,
                                  reason: why,
                                }
                              : null,
                        }}
                        selected={picked}
                        onToggle={(k) =>
                          setPicked((p) => {
                            const n = new Set(p);
                            if (n.has(k)) n.delete(k);
                            else n.add(k);
                            return n;
                          })
                        }
                        toggleLabel={(r) => `Select ${r.name}`}
                      />
                    </Section>
                  ))}
                </Stack>
              ) : null}
            </Stack>
          );
        }}
      </Load>
      <Ask
        title="Delete these runners?"
        action="Delete"
        busyLabel="Deleting"
        q={del.question}
        busy={del.busy}
        error={del.error}
        onCancel={del.cancel}
        onConfirm={() => void del.confirm()}
      />
      <Ask
        title={`Replace ${replacing?.name ?? 'the runner'}?`}
        action="Replace"
        busyLabel="Replacing"
        q={replace.question}
        busy={replace.busy}
        error={replace.error}
        onCancel={() => {
          replace.cancel();
          setReplacing(undefined);
        }}
        onConfirm={() => void replace.confirm()}
      />
      <Ask
        title={`Delete ${cleaning?.name ?? 'the runner'}?`}
        action="Delete registration"
        busyLabel="Deleting"
        q={cleanup.question}
        busy={cleanup.busy}
        error={cleanup.error}
        onCancel={() => {
          cleanup.cancel();
          setCleaning(undefined);
        }}
        onConfirm={() => void cleanup.confirm()}
      />
      <ActionBar
        primary={
          <Button
            kind="primary"
            disabled={!admin}
            reason={admin ? undefined : why}
            onPress={() => void navigate('/setup/runners/add')}
          >
            Add runners
          </Button>
        }
      />
    </Page>
  );
}
