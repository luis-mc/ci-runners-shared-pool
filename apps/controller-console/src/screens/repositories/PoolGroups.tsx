// Pool setup (CRSP340, the owner's pick of option 5): the health tiles, then the repositories in three groups, Needs
// you, Using the pool and Not using the pool, one action for each group and one tick row for each repository. The page
// passes data; the shared parts draw (UX-7, UX-11, UX-16, CORE-1 to CORE-7). The words are in content.md.
import { Button, CheckRow, Pane, Stack, StatGrid, StatTile, Text } from '@crsp/ui';
import { unwrap, useClient } from '../../api/api';
import { useLoad } from '../../api/hooks';
import { deliveryStatus } from './webhookHealth';
import type { Repository } from './plans';

const count = (n: number, one: string, many: string) => `${n} ${n === 1 ? one : many}`;

/** The three groups of the page. A repository that uses the pool and has a wrong or missing variable needs you. */
export function groupsOf(
  list: readonly Repository[],
  broken: (r: Repository) => boolean,
): { bad: Repository[]; good: Repository[]; not: Repository[] } {
  const bad = list.filter((r) => r.pool_on && broken(r));
  return {
    bad,
    good: list.filter((r) => r.pool_on && !broken(r)),
    not: list.filter((r) => !r.pool_on),
  };
}

/** The four health tiles: machines, runners, the GitHub App and the repositories. */
export function PoolHealth({
  all,
  listed,
  needsYou,
}: {
  all: readonly Repository[];
  listed: readonly Repository[];
  needsYou: number;
}) {
  const client = useClient();
  const machines = useLoad(() => unwrap(client.GET('/api/v1/machines')), []);
  const runners = useLoad(() => unwrap(client.GET('/api/v1/runners')), []);
  const ms = machines.result.state === 'ready' ? machines.result.data.items : undefined;
  const rs = runners.result.state === 'ready' ? runners.result.data.items : undefined;
  const online = ms?.filter((m) => m.online).length ?? 0;
  const ready = rs?.filter((r) => r.state === 'idle' || r.state === 'busy') ?? [];
  const idle = ready.filter((r) => r.state === 'idle').length;
  const using = listed.filter((r) => r.pool_on);
  const missed = all.filter(
    (r) => r.pool_on && deliveryStatus(r).kind === 'failed',
  ).length;
  return (
    <StatGrid>
      <StatTile
        label="Machines"
        {...(ms ? { value: String(online), unit: 'online' } : {})}
        change={ms && online < ms.length ? 'worse' : 'same'}
        changeText={
          !ms
            ? ''
            : online < ms.length
              ? `${ms.length - online} offline`
              : ms.length === 2
                ? 'both ready'
                : 'all ready'
        }
        to="/setup/machines"
        linkName="Open Machines"
        loading={!ms}
      />
      <StatTile
        label="Runners"
        {...(rs ? { value: String(ready.length), unit: 'ready' } : {})}
        change={rs && ready.length < rs.length ? 'worse' : 'same'}
        changeText={rs ? `${idle} idle, ${ready.length - idle} busy` : ''}
        to="/setup/runners"
        linkName="Open Runners"
        loading={!rs}
      />
      <StatTile
        label="GitHub App"
        value="Installed"
        change={missed > 0 ? 'worse' : 'better'}
        changeText={
          missed > 0
            ? `${count(missed, 'repository', 'repositories')} missed a delivery`
            : 'webhooks delivering'
        }
      />
      <StatTile
        label="Repositories"
        value={`${using.length} of ${listed.length}`}
        unit="use the pool"
        change={needsYou > 0 ? 'worse' : 'same'}
        changeText={needsYou > 0 ? `${needsYou} needs you` : 'all well'}
      />
    </StatGrid>
  );
}

/** One group: its title and count, a button for its ticked rows (unless the bar owns it), and one tick row each. */
export function PoolGroup({
  title,
  rows,
  picked,
  setPicked,
  action,
  blocked,
  why,
  aside,
  onAct,
}: {
  title: string;
  rows: readonly Repository[];
  picked: ReadonlySet<number>;
  setPicked: (next: Set<number>) => void;
  /** The button word for n ticked rows. Omitted when the page bar carries this group's action. */
  action?: ((n: number) => string) | undefined;
  /** The action is locked for everybody (not an admin). */
  blocked?: string | undefined;
  /** Why one repository cannot be ticked. */
  why: (r: Repository) => string | undefined;
  /** A word at the end of the row: what needs the owner, or why the pool is off. */
  aside?:
    ((r: Repository) => { word: string; needsYou: boolean } | undefined) | undefined;
  onAct: (rows: Repository[]) => void;
}) {
  const sel = rows.filter((r) => picked.has(r.id));
  const toggle = (id: number) => {
    const n = new Set(picked);
    if (!n.delete(id)) n.add(id);
    setPicked(n);
  };
  return (
    <Pane
      title={title}
      count={rows.length}
      actions={
        action ? (
          <Button
            disabled={sel.length === 0 || blocked !== undefined}
            reason={blocked ?? 'Tick at least one repository.'}
            onPress={() => onAct(sel)}
          >
            {action(sel.length)}
          </Button>
        ) : undefined
      }
    >
      <Stack gap="tight">
        {rows.length === 0 ? <Text tone="muted">Nothing here.</Text> : null}
        {rows.map((r) => {
          const locked = why(r);
          const a = aside?.(r);
          return (
            <CheckRow
              key={r.id}
              label={r.full_name}
              checked={picked.has(r.id)}
              onChange={() => toggle(r.id)}
              {...(locked ? { disabledReason: locked } : {})}
              {...(a
                ? {
                    aside: a.word,
                    ...(a.needsYou ? { asideTone: 'needs-you' as const } : {}),
                  }
                : {})}
            />
          );
        })}
      </Stack>
    </Pane>
  );
}
