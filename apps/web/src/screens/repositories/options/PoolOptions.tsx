// TEMPORARY (CRSP338): six options for the Pool setup screen and its wizard, switched by ?option=1..6.
// Only shared parts of @crsp/ui; the words are in docs/design-system/next-version/content.md (CRSP338 options).
// Loaded only when VITE_CRSP338_DEMO is set; never part of the build.
import {
  ActionBar,
  BulletList,
  Button,
  Card,
  CardGrid,
  CheckRow,
  Cluster,
  Disclosure,
  Facts,
  LinkButton,
  ListToolbar,
  Message,
  Page,
  Pane,
  Progress,
  SearchBar,
  Segmented,
  Stack,
  StatGrid,
  StatTile,
  Status,
  SummaryLine,
  Switch,
  Table,
  Text,
  Wizard,
  unsavedStatus,
  type Column,
  type StatusKind,
  type WizardPhase,
} from '@crsp/ui';
import { useCallback, useEffect, useRef, useState } from 'react';
import { TIMING } from '../../../timing';
import { ApiError, errorText, unwrap, useClient } from '../../../api/api';
import { useMounted } from '../../../api/mounted';
import { PUBLIC_SETTING_PATH, mayTry, publicWhileOff, usePublicRepositories } from '../publicRepositories';
import { allRepositories, needsYes, type Plan, type Repository } from '../plans';
import { variableStatus } from '../Repositories';

type Dir = 'on' | 'off';
interface Change {
  repo: Repository;
  to: Dir;
}
type ReviewStyle = 'list' | 'groups' | 'plain';
type WorkStyle = 'list' | 'cards' | 'calm';

const count = (n: number, one: string, many: string) => `${n} ${n === 1 ? one : many}`;
const repos = (n: number) => count(n, 'repository', 'repositories');
const isRepair = (c: Change) => c.to === 'on' && c.repo.pool_on;

// ---------------------------------------------------------------- data

function useRepos() {
  const client = useClient();
  const [items, setItems] = useState<Repository[] | undefined>(undefined);
  const reload = useCallback(() => {
    void allRepositories(client).then((r) => setItems(r.items));
  }, [client]);
  useEffect(reload, [reload]);
  return { items, reload };
}

/** Why a repository cannot be changed, in words; none when it can. */
function whyNot(r: Repository, publicEnabled: boolean | undefined): string | undefined {
  if (publicWhileOff(r, publicEnabled)) return 'Public: the pool serves private repositories only.';
  if (!mayTry(r)) return 'You need admin rights on this repository in GitHub.';
  return undefined;
}

function stateStatus(r: Repository): { kind: StatusKind; word: string } {
  if (!r.pool_on) return { kind: 'off', word: 'not using the pool' };
  const v = variableStatus(r);
  if (v.kind === 'failed') return { kind: 'failed', word: v.word };
  return { kind: 'ok', word: 'using the pool' };
}

function RepoStatus({ r }: { r: Repository }) {
  const s = stateStatus(r);
  return <Status kind={s.kind} word={s.word} />;
}

function byOwner(list: readonly Repository[]) {
  return [...new Set(list.map((r) => r.owner))]
    .sort()
    .map((owner) => ({ owner, rows: list.filter((r) => r.owner === owner) }));
}

function HiddenNote({ list, enabled }: { list: readonly Repository[]; enabled: boolean | undefined }) {
  const hidden = list.filter((r) => publicWhileOff(r, enabled));
  if (hidden.length === 0) return null;
  return (
    <Message
      tone="note"
      action={<LinkButton to={PUBLIC_SETTING_PATH}>Open the setting</LinkButton>}
    >
      {`${count(hidden.length, 'public repository is', 'public repositories are')} not listed: the pool serves private repositories only.`}
    </Message>
  );
}

// ---------------------------------------------------------------- the change wizard (Review, work, Done)

interface Lead {
  name: string;
  body: React.ReactNode;
  ready: boolean;
  nextLabel: string;
  reason: string;
}

function repoSteps(plan: Plan | undefined, full: string) {
  return (plan?.steps ?? []).filter((s) => s.target === full);
}
function repoState(plan: Plan | undefined, full: string): { kind: StatusKind; word: string } {
  const st = repoSteps(plan, full);
  if (st.length > 0 && st.every((s) => s.state === 'done' || s.state === 'skipped'))
    return { kind: 'ok', word: 'done' };
  if (st.some((s) => s.state === 'running' || s.state === 'done')) return { kind: 'running', word: 'working' };
  return { kind: 'waiting', word: 'waiting' };
}

function verbOf(changes: readonly Change[]): { work: string; name: string } {
  if (changes.every((c) => c.to === 'on' && !c.repo.pool_on)) return { work: 'Turning on', name: 'Turn on the pool' };
  if (changes.every((c) => c.to === 'off')) return { work: 'Turning off', name: 'Turn off the pool' };
  if (changes.every(isRepair)) return { work: 'Repairing', name: 'Repair the pool' };
  return { work: 'Updating', name: 'Change the pool' };
}

function sentence(c: Change): string {
  if (isRepair(c)) return `${c.repo.full_name}: the missing variable is written again`;
  return `${c.repo.full_name}`;
}

function ReviewBody({ changes, plan, style }: { changes: readonly Change[]; plan: Plan | undefined; style: ReviewStyle }) {
  const on = changes.filter((c) => c.to === 'on' && !isRepair(c));
  const off = changes.filter((c) => c.to === 'off');
  const fix = changes.filter(isRepair);
  const technical = (
    <Disclosure summary={`Technical steps (${plan?.steps.length ?? 0})`}>
      <BulletList items={(plan?.steps ?? []).map((s) => s.text)} label="Technical steps" />
    </Disclosure>
  );
  if (style === 'list') {
    return (
      <Pane title="Review" count={changes.length}>
        <Stack>
          <SummaryLine
            total={[{ count: changes.length, word: 'change', plural: 'changes' }]}
            states={[
              { count: on.length, word: 'turn on' },
              { count: off.length, word: 'turn off' },
              { count: fix.length, word: 'repair' },
            ].filter((x) => x.count > 0)}
          />
          <Facts
            items={changes.map((c) => ({
              name: c.repo.full_name,
              value: (
                <Status
                  kind="info"
                  word={isRepair(c) ? 'will be repaired' : c.to === 'on' ? 'will turn on' : 'will turn off'}
                />
              ),
            }))}
          />
          {technical}
        </Stack>
      </Pane>
    );
  }
  if (style === 'groups') {
    return (
      <Stack>
        {on.length > 0 ? (
          <Pane title="These will start using the pool" count={on.length}>
            <BulletList items={on.map((c) => c.repo.full_name)} label="Will start using the pool" />
          </Pane>
        ) : null}
        {off.length > 0 ? (
          <Pane title="These will stop using the pool" count={off.length}>
            <Stack>
              <BulletList items={off.map((c) => c.repo.full_name)} label="Will stop using the pool" />
              <Text tone="muted">Their runners are removed and their settings are kept.</Text>
            </Stack>
          </Pane>
        ) : null}
        {fix.length > 0 ? (
          <Pane title="These will be repaired" count={fix.length}>
            <BulletList items={fix.map(sentence)} label="Will be repaired" />
          </Pane>
        ) : null}
        {technical}
      </Stack>
    );
  }
  return (
    <Pane title="Review">
      <Stack>
        {on.length > 0 ? (
          <>
            <Text>{`${count(on.length, 'repository', 'repositories')} will start using the pool:`}</Text>
            <BulletList items={on.map((c) => c.repo.full_name)} label="Will start using the pool" />
          </>
        ) : null}
        {off.length > 0 ? (
          <>
            <Text>{`${count(off.length, 'repository', 'repositories')} will stop using the pool:`}</Text>
            <BulletList items={off.map((c) => c.repo.full_name)} label="Will stop using the pool" />
          </>
        ) : null}
        {fix.length > 0 ? (
          <>
            <Text>{`${count(fix.length, 'repository', 'repositories')} will get the missing setting back:`}</Text>
            <BulletList items={fix.map((c) => c.repo.full_name)} label="Will be repaired" />
          </>
        ) : null}
        <Text tone="muted">
          Jobs of these repositories choose their runner by labels. Nothing runs on your machines until a job asks for it.
        </Text>
        {technical}
      </Stack>
    </Pane>
  );
}

function WorkBody({
  changes,
  plan,
  style,
  verb,
}: {
  changes: readonly Change[];
  plan: Plan | undefined;
  style: WorkStyle;
  verb: string;
}) {
  if (style === 'cards')
    return (
      <CardGrid>
        {changes.map((c) => {
          const st = repoSteps(plan, c.repo.full_name);
          const done = st.filter((s) => s.state === 'done').length;
          const s = repoState(plan, c.repo.full_name);
          return (
            <Card key={c.repo.id} title={c.repo.full_name}>
              <Stack gap="tight">
                <Status kind={s.kind} word={s.word} />
                <Progress label={`${verb} ${c.repo.full_name}`} value={done} max={Math.max(st.length, 1)} />
              </Stack>
            </Card>
          );
        })}
      </CardGrid>
    );
  const list = (
    <Facts
      items={changes.map((c) => {
        const s = repoState(plan, c.repo.full_name);
        return { name: c.repo.full_name, value: <Status kind={s.kind} word={s.word} /> };
      })}
    />
  );
  if (style === 'calm')
    return (
      <Pane title={verb}>
        <Stack>
          {list}
          <Disclosure summary={`Technical steps (${plan?.steps.length ?? 0})`}>
            <Facts
              items={(plan?.steps ?? []).map((s) => ({
                name: s.text,
                value: (
                  <Status
                    kind={s.state === 'done' ? 'ok' : s.state === 'running' ? 'running' : 'waiting'}
                    word={s.state === 'done' ? 'done' : s.state === 'running' ? 'working' : 'waiting'}
                  />
                ),
              }))}
            />
          </Disclosure>
        </Stack>
      </Pane>
    );
  return <Pane title={verb} count={changes.length}>{list}</Pane>;
}

export function ChangeWizard({
  changes,
  review,
  work,
  lead,
  onClose,
  onFinished,
  doneLabel = 'Back to the repositories',
}: {
  changes: readonly Change[];
  review: ReviewStyle;
  work: WorkStyle;
  lead?: Lead | undefined;
  onClose: () => void;
  onFinished: () => void;
  doneLabel?: string;
}) {
  const client = useClient();
  const { mounted, sleep } = useMounted();
  const verb = verbOf(changes.length > 0 ? changes : [{ repo: {} as Repository, to: 'on' }]);
  const names = lead ? [lead.name, 'Review', verb.work, 'Done'] : ['Review', verb.work, 'Done'];
  const off = lead ? 1 : 0;
  const [step, setStep] = useState(0);
  const [phase, setPhase] = useState<WizardPhase>(lead ? 'first-run' : 'planning');
  const [plan, setPlan] = useState<Plan | undefined>(undefined);
  const [problem, setProblem] = useState<string | undefined>(undefined);
  const [stopping, setStopping] = useState(false);
  const started = useRef(false);

  const toReview = useCallback(async () => {
    setStep(off);
    setPhase('planning');
    try {
      const p = await unwrap(
        client.POST('/api/v1/repository-plans', {
          body: { changes: changes.map((c) => ({ repository_id: c.repo.id, pool: c.to })) },
        }),
      );
      setPlan(p);
      setPhase('ready-to-review');
    } catch (e) {
      setProblem(errorText(e));
      setStep(off + 2);
      setPhase('failed');
    }
  }, [client, changes, off]);

  useEffect(() => {
    if (!lead && !started.current) {
      started.current = true;
      void toReview();
    }
  }, [lead, toReview]);

  const apply = async () => {
    if (!plan) return;
    setStep(off + 1);
    setPhase('running');
    try {
      let p = await unwrap(
        client.POST('/api/v1/repository-plans/{planId}/apply', {
          params: { path: { planId: plan.id } },
          body: { approved_steps: needsYes(plan) },
        }),
      );
      setPlan(p);
      while (p.state === 'applying' || p.state === 'planned') {
        if (!(await sleep(TIMING.planPollMs))) return;
        p = await unwrap(client.GET('/api/v1/repository-plans/{planId}', { params: { path: { planId: p.id } } }));
        if (!mounted.current) return;
        setPlan(p);
      }
      setStep(off + 2);
      setPhase('done');
      onFinished();
    } catch (e) {
      setProblem(errorText(e));
      setStep(off + 2);
      setPhase('failed');
    }
  };

  const stop = async () => {
    if (!plan) return;
    setStopping(true);
    try {
      await unwrap(client.POST('/api/v1/repository-plans/{planId}/stop', { params: { path: { planId: plan.id } } }));
    } catch (e) {
      if (!(e instanceof ApiError && e.status === 409)) setStopping(false);
    }
  };

  const total = plan?.steps.length ?? 0;
  const stepsDone = plan?.steps.filter((s) => s.state === 'done').length ?? 0;
  const reposDone = changes.filter((c) => repoState(plan, c.repo.full_name).word === 'done').length;
  const current = changes.find((c) => repoState(plan, c.repo.full_name).word === 'working');

  let body: React.ReactNode;
  if (lead && step === 0) body = lead.body;
  else if (step === off) body = <ReviewBody changes={changes} plan={plan} style={review} />;
  else if (step === off + 1) body = <WorkBody changes={changes} plan={plan} style={work} verb={verb.work} />;
  else {
    const on = changes.filter((c) => c.to === 'on' && !isRepair(c)).length;
    const offN = changes.filter((c) => c.to === 'off').length;
    const fix = changes.filter(isRepair).length;
    const parts = [
      on > 0 ? `${repos(on)} now ${on === 1 ? 'uses' : 'use'} the pool.` : '',
      offN > 0 ? `${repos(offN)} no longer ${offN === 1 ? 'uses' : 'use'} the pool.` : '',
      fix > 0 ? `${repos(fix)} repaired.` : '',
    ].filter(Boolean);
    body = (
      <Pane title="Result">
        <Stack>
          {phase === 'failed' ? (
            <Message tone="problem">{`Nothing more was changed. ${problem ?? ''}`}</Message>
          ) : (
            <Message tone="done">{parts.join(' ')}</Message>
          )}
          <Facts
            items={changes.map((c) => ({
              name: c.repo.full_name,
              value: (
                <Status
                  kind={c.to === 'on' ? 'ok' : 'off'}
                  word={isRepair(c) ? 'repaired' : c.to === 'on' ? 'using the pool' : 'not using the pool'}
                />
              ),
            }))}
          />
        </Stack>
      </Pane>
    );
  }

  return (
    <Wizard
      name={verb.name}
      steps={names}
      current={step}
      firstStep={-1}
      phase={phase}
      reading="Reading the repositories"
      progress={
        phase === 'running'
          ? {
              text: `${verb.work} ${Math.min(reposDone + 1, changes.length)} of ${changes.length}${current ? `: ${current.repo.full_name}` : ''}`,
              value: stepsDone,
              max: Math.max(total, 1),
            }
          : undefined
      }
      canStop={phase === 'running' && Boolean(plan)}
      stopping={stopping}
      onStop={() => void stop()}
      onBack={() => {
        if (step === 0) onClose();
        else if (lead && step === 1) {
          setStep(0);
          setPhase('first-run');
        }
      }}
      onStep={(i) => {
        if (lead && i === 0 && phase === 'ready-to-review') {
          setStep(0);
          setPhase('first-run');
        }
      }}
      primary={
        lead && step === 0
          ? { label: lead.nextLabel, onPress: () => void toReview(), disabled: !lead.ready, reason: lead.reason }
          : {
              label: `${verb.name.replace('the pool', '')}${repos(changes.length)}`.replace(/^Turn on /, 'Turn on ').trim(),
              busyLabel: verb.work,
              onPress: () => void apply(),
            }
      }
      review={step === off && phase === 'ready-to-review'}
      onStartAgain={onClose}
      onTryAgain={onClose}
      donePrimary={{ label: doneLabel, onPress: onClose }}
    >
      {body}
    </Wizard>
  );
}

// ---------------------------------------------------------------- shared page pieces

interface Ctx {
  list: Repository[];
  enabled: boolean | undefined;
  start: (changes: Change[]) => void;
  reload: () => void;
}

function toggle<T>(set: ReadonlySet<T>, v: T): Set<T> {
  const n = new Set(set);
  if (!n.delete(v)) n.add(v);
  return n;
}

function matches(r: Repository, q: string, f: '' | 'on' | 'off'): boolean {
  return r.full_name.toLowerCase().includes(q.trim().toLowerCase()) && (!f || (f === 'on') === r.pool_on);
}

// ---------------------------------------------------------------- option 1: a switch per repository, changes staged

function Option1({ list, enabled, start }: Ctx) {
  const [staged, setStaged] = useState<ReadonlyMap<number, Dir>>(new Map());
  const [q, setQ] = useState('');
  const [f, setF] = useState<'' | 'on' | 'off'>('');
  const shown = list.filter((r) => !publicWhileOff(r, enabled) && matches(r, q, f));
  const effective = (r: Repository) => (staged.has(r.id) ? staged.get(r.id) === 'on' : r.pool_on);
  const flip = (r: Repository, on: boolean) =>
    setStaged((m) => {
      const n = new Map(m);
      if (on === r.pool_on) n.delete(r.id);
      else n.set(r.id, on ? 'on' : 'off');
      return n;
    });
  const changes: Change[] = list.filter((r) => staged.has(r.id)).map((r) => ({ repo: r, to: staged.get(r.id)! }));
  return (
    <Page>
      <Pane title="Repositories" count={shown.length}>
        <Stack>
          <HiddenNote list={list} enabled={enabled} />
          <ListToolbar
            label="Repositories"
            search={<SearchBar value={q} onChange={setQ} label="Search repositories" />}
            filters={
              <Segmented
                label="Show"
                options={[
                  { value: 'on', label: 'Using the pool' },
                  { value: 'off', label: 'Not using the pool' },
                ]}
                value={f}
                onChange={setF}
              />
            }
            count={repos(shown.length)}
          />
          {byOwner(shown).map((g) => (
            <Pane key={g.owner} title={g.owner} count={g.rows.length} level={3}>
              <Stack gap="tight">
                {g.rows.map((r) => {
                  const why = whyNot(r, enabled);
                  return (
                    <Cluster key={r.id}>
                      <Switch
                        label={r.name}
                        on={effective(r)}
                        onChange={(on) => flip(r, on)}
                        {...(why && !r.pool_on ? { disabledReason: why } : {})}
                      />
                      {staged.has(r.id) ? (
                        <Status kind="waiting" word={staged.get(r.id) === 'on' ? 'will turn on' : 'will turn off'} />
                      ) : (
                        <RepoStatus r={r} />
                      )}
                    </Cluster>
                  );
                })}
              </Stack>
            </Pane>
          ))}
        </Stack>
      </Pane>
      <ActionBar
        status={unsavedStatus(changes.length) ?? 'No changes'}
        secondary={
          <Button onPress={() => setStaged(new Map())} disabled={changes.length === 0} reason="Nothing is changed.">
            Discard changes
          </Button>
        }
        primary={
          <Button
            kind="primary"
            disabled={changes.length === 0}
            reason="Switch a repository on or off first."
            onPress={() => start(changes)}
          >
            {`Review ${count(changes.length, 'change', 'changes')}`}
          </Button>
        }
      />
    </Page>
  );
}

// ---------------------------------------------------------------- option 2: two lists, Add and Remove

function PickList({
  title,
  rows,
  picked,
  setPicked,
  action,
  actionLabel,
  reason,
  enabled,
}: {
  title: string;
  rows: Repository[];
  picked: ReadonlySet<number>;
  setPicked: (s: Set<number>) => void;
  action: () => void;
  actionLabel: (n: number) => string;
  reason: string;
  enabled: boolean | undefined;
}) {
  const n = rows.filter((r) => picked.has(r.id)).length;
  return (
    <Card
      title={title}
      count={rows.length}
      footer={
        <Button disabled={n === 0} reason={reason} onPress={action}>
          {actionLabel(n)}
        </Button>
      }
    >
      <Stack gap="tight">
        <Cluster>
          <Button
            onPress={() => setPicked(new Set(rows.filter((r) => !whyNot(r, enabled) || r.pool_on).map((r) => r.id)))}
            disabled={rows.length === 0}
            reason="No repository is listed."
          >
            {`Select all ${rows.length}`}
          </Button>
          <Button onPress={() => setPicked(new Set())} disabled={picked.size === 0} reason="Nothing is selected.">
            Clear
          </Button>
        </Cluster>
        {rows.map((r) => {
          const why = r.pool_on ? undefined : whyNot(r, enabled);
          const s = stateStatus(r);
          return (
            <CheckRow
              key={r.id}
              label={r.full_name}
              checked={picked.has(r.id)}
              onChange={() => setPicked(toggle(picked, r.id))}
              {...(why ? { disabledReason: why } : {})}
              {...(s.kind === 'failed' ? { aside: s.word, asideTone: 'needs-you' as const } : {})}
            />
          );
        })}
      </Stack>
    </Card>
  );
}

function Option2({ list, enabled, start }: Ctx) {
  const [add, setAdd] = useState<ReadonlySet<number>>(new Set());
  const [rem, setRem] = useState<ReadonlySet<number>>(new Set());
  const visible = list.filter((r) => !publicWhileOff(r, enabled));
  const using = visible.filter((r) => r.pool_on);
  const not = visible.filter((r) => !r.pool_on);
  return (
    <Page>
      <HiddenNote list={list} enabled={enabled} />
      <CardGrid>
        <PickList
          title="Using the pool"
          rows={using}
          picked={rem}
          setPicked={setRem}
          enabled={enabled}
          reason="Tick the repositories to take out of the pool."
          actionLabel={(n) => (n === 0 ? 'Remove from the pool' : `Remove ${n} from the pool`)}
          action={() => start(using.filter((r) => rem.has(r.id)).map((r) => ({ repo: r, to: 'off' as const })))}
        />
        <PickList
          title="Not using the pool"
          rows={not}
          picked={add}
          setPicked={setAdd}
          enabled={enabled}
          reason="Tick the repositories to add to the pool."
          actionLabel={(n) => (n === 0 ? 'Add to the pool' : `Add ${n} to the pool`)}
          action={() => start(not.filter((r) => add.has(r.id)).map((r) => ({ repo: r, to: 'on' as const })))}
        />
      </CardGrid>
    </Page>
  );
}

// ---------------------------------------------------------------- option 3: status and one button; the wizard owns the pick

function Option3({ list, enabled, reload }: Ctx) {
  const [open, setOpen] = useState(false);
  const using = list.filter((r) => r.pool_on);
  const [want, setWant] = useState<ReadonlySet<number>>(new Set(using.map((r) => r.id)));
  const choosable = list;
  const changes: Change[] = choosable.flatMap((r) => {
    const on = want.has(r.id);
    return on !== r.pool_on ? [{ repo: r, to: on ? ('on' as const) : ('off' as const) }] : [];
  });
  const lead: Lead = {
    name: 'Repositories',
    nextLabel: `Review ${count(changes.length, 'change', 'changes')}`,
    reason: 'Tick a repository to add it, or untick one to remove it.',
    ready: changes.length > 0,
    body: (
      <Pane title="Repositories that use the pool" count={want.size}>
        <Stack gap="tight">
          <Text>Tick the repositories that should use the pool.</Text>
          <Cluster>
            <Button
              onPress={() => setWant(new Set(choosable.filter((r) => r.pool_on || !whyNot(r, enabled)).map((r) => r.id)))}
            >
              {`Select all ${choosable.filter((r) => r.pool_on || !whyNot(r, enabled)).length}`}
            </Button>
            <Button onPress={() => setWant(new Set())} disabled={want.size === 0} reason="Nothing is selected.">
              Clear
            </Button>
          </Cluster>
          {choosable.map((r) => {
            const why = r.pool_on ? undefined : whyNot(r, enabled);
            return (
              <CheckRow
                key={r.id}
                label={r.full_name}
                checked={want.has(r.id)}
                changed={want.has(r.id) !== r.pool_on}
                onChange={() => setWant(toggle(want, r.id))}
                {...(why ? { disabledReason: why } : {})}
              />
            );
          })}
        </Stack>
      </Pane>
    ),
  };
  if (open)
    return (
      <Page>
        <ChangeWizard
          changes={changes}
          review="plain"
          work="calm"
          lead={lead}
          onClose={() => {
            setOpen(false);
            reload();
          }}
          onFinished={reload}
        />
      </Page>
    );
  return (
    <Page>
      <Pane title="The pool">
        <Stack>
          <SummaryLine
            total={[{ count: using.length, word: 'repository uses the pool', plural: 'repositories use the pool' }]}
            states={[{ count: list.filter((r) => !r.pool_on && !whyNot(r, enabled)).length, word: 'could be added' }]}
          />
          <Facts
            items={using.map((r) => ({ name: r.full_name, value: <RepoStatus r={r} /> }))}
          />
        </Stack>
      </Pane>
      <ActionBar
        status={`${using.length} of ${list.length} repositories use the pool`}
        primary={
          <Button kind="primary" onPress={() => setOpen(true)}>
            Set up the pool
          </Button>
        }
      />
    </Page>
  );
}

// ---------------------------------------------------------------- option 4: bulk bar with both actions

function Option4({ list, enabled, start }: Ctx) {
  const [picked, setPicked] = useState<ReadonlySet<number>>(new Set());
  const [q, setQ] = useState('');
  const [f, setF] = useState<'' | 'on' | 'off'>('');
  const shown = list.filter((r) => !publicWhileOff(r, enabled) && matches(r, q, f));
  const chosen = shown.filter((r) => picked.has(r.id));
  const ons = chosen.filter((r) => !r.pool_on && !whyNot(r, enabled));
  const offs = chosen.filter((r) => r.pool_on);
  return (
    <Page>
      <Pane title="Repositories" count={shown.length}>
        <Stack>
          <HiddenNote list={list} enabled={enabled} />
          <ListToolbar
            label="Repositories"
            search={<SearchBar value={q} onChange={setQ} label="Search repositories" />}
            filters={
              <Segmented
                label="Show"
                options={[
                  { value: 'on', label: 'Using the pool' },
                  { value: 'off', label: 'Not using the pool' },
                ]}
                value={f}
                onChange={setF}
              />
            }
            selection={
              <>
                <Button onPress={() => setPicked(new Set(shown.map((r) => r.id)))} disabled={shown.length === 0} reason="No repository is shown.">
                  {`Select all ${shown.length}`}
                </Button>
                <Button onPress={() => setPicked(new Set())} disabled={picked.size === 0} reason="Nothing is selected.">
                  Clear
                </Button>
              </>
            }
            count={repos(shown.length)}
          />
          {byOwner(shown).map((g) => (
            <Pane key={g.owner} title={g.owner} count={g.rows.length} level={3}>
              <Stack gap="tight">
                {g.rows.map((r) => {
                  const s = stateStatus(r);
                  return (
                    <CheckRow
                      key={r.id}
                      label={r.name}
                      help={s.word}
                      checked={picked.has(r.id)}
                      onChange={() => setPicked(toggle(picked, r.id))}
                      {...(!r.pool_on && whyNot(r, enabled) ? { disabledReason: whyNot(r, enabled)! } : {})}
                    />
                  );
                })}
              </Stack>
            </Pane>
          ))}
        </Stack>
      </Pane>
      <ActionBar
        status={`${chosen.length} selected: ${ons.length} to turn on, ${offs.length} to turn off`}
        secondary={
          <Button
            disabled={offs.length === 0}
            reason="Tick repositories that use the pool."
            onPress={() => start(offs.map((r) => ({ repo: r, to: 'off' as const })))}
          >
            {`Turn off (${offs.length})`}
          </Button>
        }
        primary={
          <Button
            kind="primary"
            disabled={ons.length === 0}
            reason="Tick repositories that do not use the pool."
            onPress={() => start(ons.map((r) => ({ repo: r, to: 'on' as const })))}
          >
            {`Turn on (${ons.length})`}
          </Button>
        }
      />
    </Page>
  );
}

// ---------------------------------------------------------------- option 5: status first, grouped by state

function Group({
  title,
  rows,
  picked,
  setPicked,
  label,
  onAct,
  kind,
}: {
  title: string;
  rows: Repository[];
  picked: ReadonlySet<number>;
  setPicked: (s: Set<number>) => void;
  label: (n: number) => string;
  onAct: (rows: Repository[]) => void;
  kind: 'primary' | 'default';
}) {
  const sel = rows.filter((r) => picked.has(r.id));
  return (
    <Pane
      title={title}
      count={rows.length}
      actions={
        <Button kind={kind} disabled={sel.length === 0} reason="Tick at least one repository." onPress={() => onAct(sel)}>
          {label(sel.length)}
        </Button>
      }
    >
      <Stack gap="tight">
        {rows.length === 0 ? <Text tone="muted">Nothing here.</Text> : null}
        {rows.map((r) => {
          const why = r.pool_on ? undefined : whyNot(r, undefined);
          return (
            <CheckRow
              key={r.id}
              label={r.full_name}
              checked={picked.has(r.id)}
              onChange={() => setPicked(toggle(picked, r.id))}
              {...(why ? { disabledReason: why } : {})}
              {...(variableStatus(r).kind === 'failed' && r.pool_on ? { aside: variableStatus(r).word, asideTone: 'needs-you' as const } : {})}
            />
          );
        })}
      </Stack>
    </Pane>
  );
}

function Option5({ list, enabled, start }: Ctx) {
  const visible = list.filter((r) => !publicWhileOff(r, enabled));
  const bad = visible.filter((r) => r.pool_on && variableStatus(r).kind === 'failed');
  const good = visible.filter((r) => r.pool_on && !bad.includes(r));
  const not = visible.filter((r) => !r.pool_on);
  const [pBad, setPBad] = useState<ReadonlySet<number>>(new Set(bad.map((r) => r.id)));
  const [pGood, setPGood] = useState<ReadonlySet<number>>(new Set());
  const [pNot, setPNot] = useState<ReadonlySet<number>>(new Set());
  const runners = 5;
  return (
    <Page>
      <StatGrid>
        <StatTile label="Machines" value="2" unit="online" change="same" changeText="both ready" to="/setup/machines" linkName="Open Machines" />
        <StatTile label="Runners" value={String(runners)} unit="ready" change="same" changeText="3 idle, 2 busy" to="/setup/runners" linkName="Open Runners" />
        <StatTile label="GitHub App" value="Installed" change="better" changeText="webhooks delivering" />
        <StatTile label="Repositories" value={`${good.length + bad.length} of ${visible.length}`} unit="use the pool" change={bad.length > 0 ? 'worse' : 'same'} changeText={bad.length > 0 ? `${bad.length} needs you` : 'all well'} />
      </StatGrid>
      <HiddenNote list={list} enabled={enabled} />
      {bad.length > 0 ? (
        <Group
          title="Needs you"
          rows={bad}
          picked={pBad}
          setPicked={setPBad}
          kind="primary"
          label={(n) => (n === 0 ? 'Repair' : `Repair ${n}`)}
          onAct={(rows) => start(rows.map((r) => ({ repo: r, to: 'on' as const })))}
        />
      ) : null}
      <Group
        title="Using the pool"
        rows={good}
        picked={pGood}
        setPicked={setPGood}
        kind="default"
        label={(n) => (n === 0 ? 'Turn off' : `Turn off ${n}`)}
        onAct={(rows) => start(rows.map((r) => ({ repo: r, to: 'off' as const })))}
      />
      <Group
        title="Not using the pool"
        rows={not}
        picked={pNot}
        setPicked={setPNot}
        kind={bad.length > 0 ? 'default' : 'primary'}
        label={(n) => (n === 0 ? 'Turn on' : `Turn on ${n}`)}
        onAct={(rows) => start(rows.map((r) => ({ repo: r, to: 'on' as const })))}
      />
    </Page>
  );
}

// ---------------------------------------------------------------- option 6: the page as today, plain review and calm progress

function Option6({ list, enabled, start }: Ctx) {
  const [picked, setPicked] = useState<ReadonlySet<number>>(new Set());
  const [q, setQ] = useState('');
  const shown = list.filter((r) => !publicWhileOff(r, enabled) && matches(r, q, ''));
  const chosen = shown.filter((r) => picked.has(r.id));
  const ons = chosen.filter((r) => !r.pool_on && !whyNot(r, enabled));
  const offs = chosen.filter((r) => r.pool_on);
  const act: Dir = ons.length > 0 ? 'on' : 'off';
  const target = act === 'on' ? ons : offs;
  const columns: Column<Repository>[] = [
    { id: 'name', header: 'Repository', cell: (r) => r.full_name, sort: (r) => r.full_name },
    { id: 'pool', header: 'Pool', cell: (r) => <RepoStatus r={r} />, sort: (r) => String(r.pool_on) },
  ];
  return (
    <Page>
      <Pane title="Repositories" count={shown.length}>
        <Stack>
          <HiddenNote list={list} enabled={enabled} />
          <ListToolbar
            label="Repositories"
            search={<SearchBar value={q} onChange={setQ} label="Search repositories" />}
            selection={
              <>
                <Button onPress={() => setPicked(new Set(shown.map((r) => r.id)))}>{`Select all ${shown.length}`}</Button>
                <Button onPress={() => setPicked(new Set())} disabled={picked.size === 0} reason="Nothing is selected.">
                  Clear
                </Button>
              </>
            }
            count={repos(shown.length)}
          />
          <Table
            caption="Repositories"
            columns={columns}
            rows={shown}
            rowKey={(r) => String(r.id)}
            selected={new Set([...picked].map(String))}
            onToggle={(k) => setPicked(toggle(picked, Number(k)))}
            toggleLabel={(r) => `Select ${r.full_name}`}
          />
        </Stack>
      </Pane>
      <ActionBar
        status={
          chosen.length === 0
            ? 'Nothing selected'
            : ons.length > 0 && offs.length > 0
              ? `${chosen.length} selected: ${offs.length} already ${offs.length === 1 ? 'uses' : 'use'} the pool and stay${offs.length === 1 ? 's' : ''} as they are`
              : `${chosen.length} selected`
        }
        primary={
          <Button
            kind="primary"
            disabled={target.length === 0}
            reason="Tick the repositories to turn on or off."
            onPress={() => start(target.map((r) => ({ repo: r, to: act })))}
          >
            {target.length === 0 ? 'Turn on or off' : `${act === 'on' ? 'Turn on' : 'Turn off'} ${target.length}`}
          </Button>
        }
      />
    </Page>
  );
}

// ---------------------------------------------------------------- the switch

const STYLES: Record<number, { review: ReviewStyle; work: WorkStyle }> = {
  1: { review: 'list', work: 'list' },
  2: { review: 'groups', work: 'list' },
  3: { review: 'plain', work: 'calm' },
  4: { review: 'list', work: 'list' },
  5: { review: 'groups', work: 'cards' },
  6: { review: 'plain', work: 'calm' },
};

export function PoolOption({ option }: { option: number }) {
  const { items, reload } = useRepos();
  const { enabled } = usePublicRepositories();
  const [run, setRun] = useState<Change[] | undefined>(undefined);
  const style = STYLES[option] ?? STYLES[1]!;
  if (!items) return <Page><Text>Reading the repositories</Text></Page>;
  if (run)
    return (
      <Page>
        <ChangeWizard
          changes={run}
          review={style.review}
          work={style.work}
          onClose={() => {
            setRun(undefined);
            reload();
          }}
          onFinished={reload}
        />
      </Page>
    );
  const ctx: Ctx = { list: items, enabled, start: setRun, reload };
  switch (option) {
    case 2:
      return <Option2 {...ctx} />;
    case 3:
      return <Option3 {...ctx} />;
    case 4:
      return <Option4 {...ctx} />;
    case 5:
      return <Option5 {...ctx} />;
    case 6:
      return <Option6 {...ctx} />;
    default:
      return <Option1 {...ctx} />;
  }
}
