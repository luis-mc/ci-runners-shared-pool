// Pool setup (CRSP340, the owner's pick of option 5): the health tiles, then the repositories in three groups, Needs
// you, Using the pool and Not using the pool, one action for each group and one card for each repository. The page
// passes data; the shared parts draw (UX-7, UX-11, UX-16, CORE-1 to CORE-7). The words are in content.md.
import {
  Button,
  Card,
  CardGrid,
  CheckRow,
  Pane,
  Stack,
  StatGrid,
  StatTile,
  Status,
  Text,
  type StatusKind,
} from '@crsp/ui';
import { useState, type ReactNode } from 'react';
import { unwrap, useClient } from '../../api/api';
import { useLoad } from '../../api/hooks';
import { deliveryStatus } from './webhookHealth';
import type { Repository } from './plans';

const SHOWN = 25;

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
        changeText={ms ? `${ms.length} in the pool` : ''}
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

/** One group: its title and count, one action for the ticked cards, and one card per repository. */
export function PoolGroup({
  title,
  lead,
  rows,
  picked,
  setPicked,
  action,
  blocked,
  why,
  meta,
  extra,
  note,
  onAct,
}: {
  title: string;
  /** One line that says what the group is. */
  lead: string;
  rows: readonly Repository[];
  picked: ReadonlySet<number>;
  setPicked: (next: Set<number>) => void;
  /** The button word for n ticked cards. */
  action: (n: number) => string;
  /** The action is locked for everybody (not an admin). */
  blocked?: string | undefined;
  /** Why one repository cannot be ticked. */
  why: (r: Repository) => string | undefined;
  /** The quiet line under the name. */
  meta: (r: Repository) => string;
  /** More under the line: the visibility and a problem with its fix. */
  extra?: ((r: Repository) => ReactNode) | undefined;
  /** A word beside the status, for a repository that needs you. */
  note?:
    ((r: Repository) => { status: StatusKind; word: string } | undefined) | undefined;
  onAct: (rows: Repository[]) => void;
}) {
  const [all, setAll] = useState(false);
  const shown = all ? rows : rows.slice(0, SHOWN);
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
        <Button
          disabled={sel.length === 0 || blocked !== undefined}
          reason={blocked ?? 'Tick at least one repository.'}
          onPress={() => onAct(sel)}
        >
          {action(sel.length)}
        </Button>
      }
    >
      <Stack>
        <Text tone="muted">{rows.length === 0 ? 'Nothing here.' : lead}</Text>
        <CardGrid>
          {shown.map((r) => {
            const n = note?.(r);
            const locked = why(r);
            return (
              <Card
                key={r.id}
                title={r.full_name}
                to={`/setup/pool/${r.owner}/${r.name}`}
              >
                <Stack gap="tight">
                  <CheckRow
                    label={`Select ${r.full_name}`}
                    checked={picked.has(r.id)}
                    onChange={() => toggle(r.id)}
                    {...(locked ? { disabledReason: locked } : {})}
                  />
                  {n ? <Status kind={n.status} word={n.word} /> : null}
                  <Text tone="muted">{meta(r)}</Text>
                  {extra?.(r)}
                </Stack>
              </Card>
            );
          })}
        </CardGrid>
        {rows.length > SHOWN && !all ? (
          <Button onPress={() => setAll(true)}>{`Show all ${rows.length}`}</Button>
        ) : null}
      </Stack>
    </Pane>
  );
}
