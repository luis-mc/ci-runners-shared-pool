// The lifecycle parts (install, update, remove): UpdateBanner, UpdatesCard, UpdateHistory, RemoteUpdate,
// RemoveDialog, FleetRows. They are ported from the dev-standards part templates and drawn with crsp's own
// shared parts (Pane, Button, Dialog, Status, Progress, Message), so they keep crsp's tokens and look.
// A part never holds a word of its own (LC-22): every word comes from the lifecycle words table. A part never
// writes a class or a style (LC-23). The page passes data only.
import { useState, type ReactNode } from 'react';
import { lw } from '../lifecycleWords';
import { Button, LinkButton, TextLink } from './Button';
import { Dialog } from './Dialog';
import { ChoiceField, TextField } from './Fields';
import { Facts } from './Card';
import { Stack, Text } from './layout';
import { Message } from './Message';
import { Pane } from './Pane';
import { Progress } from './Progress';
import { Status, type StatusKind } from './Status';
import { Table, type Column } from './Table';

// ---------------------------------------------------------------- UpdateBanner

export type BannerState = 'ready' | 'running' | 'done' | 'failed';
export type UpdateStep = 'downloading' | 'verifying' | 'installing' | 'testing';

export interface UpdateBannerProps {
  state: BannerState;
  version: string;
  /** For `running`: the step in words (the card draws it; the banner is one short line, CRSP364). */
  step?: UpdateStep | undefined;
  /** For `running`: where the one top line links to (Settings, Updates). Left out, the line has no link. */
  to?: string | undefined;
  /** For `failed`: the one-line reason, already a sentence from the words table. */
  reason?: string | undefined;
  onUpdate?: (() => void) | undefined;
  onNotNow?: (() => void) | undefined;
  onRetry?: (() => void) | undefined;
  onClose?: (() => void) | undefined;
  onWhatsNew?: (() => void) | undefined;
}

/** The update banner: ready, running, done, failed. One primary action per state (UX-4). */
export function UpdateBanner(p: UpdateBannerProps) {
  const tone = p.state === 'failed' ? 'problem' : p.state === 'done' ? 'done' : 'note';
  let body: ReactNode;
  let action: ReactNode;
  if (p.state === 'ready') {
    body = lw('available.title', { version: p.version });
    action = (
      <>
        <Button onPress={p.onNotNow}>{lw('available.secondary')}</Button>
        <Button kind="primary" onPress={p.onUpdate}>
          {lw('available.primary', { version: p.version })}
        </Button>
      </>
    );
  } else if (p.state === 'running') {
    // One short line, no bar: the progress lives only in the Updates pane (CRSP364).
    body = lw('banner.running_line');
    action = p.to ? <LinkButton to={p.to}>{lw('banner.open')}</LinkButton> : null;
  } else if (p.state === 'done') {
    body = lw('updated.title', { version: p.version });
    action = p.onWhatsNew ? (
      <Button onPress={p.onWhatsNew}>{lw('updated.action')}</Button>
    ) : null;
  } else {
    body = p.reason ?? lw('banner.failed');
    action = (
      <>
        <Button onPress={p.onClose}>{lw('action.close')}</Button>
        <Button kind="primary" onPress={p.onRetry}>
          {lw('action.retry')}
        </Button>
      </>
    );
  }
  return (
    <div data-lifecycle="update-banner" data-state={p.state}>
      <Message
        tone={tone}
        action={action}
        dismissible={false}
        id={`update-${p.state}-${p.version}`}
      >
        {body}
      </Message>
    </div>
  );
}

// ---------------------------------------------------------------- UpdatesCard

export type UpdatePolicy = 'automatic_when_idle' | 'ask_first';

/** The contract's update states, as the card shows them. */
export type UpdateState =
  | 'idle'
  | 'checking'
  | 'available'
  | 'waiting_for_jobs'
  | 'downloading'
  | 'verifying'
  | 'installing'
  | 'testing'
  | 'updated'
  | 'rolled_back'
  | 'refused'
  | 'failed';

export interface UpdateFailure {
  version: string;
  count: number;
  /** When the next try is, already formatted. Empty when the update is skipped until a newer version. */
  retryAt: string;
}

export interface UpdatesCardProps {
  policy: UpdatePolicy;
  window?: { start: string; end: string } | null | undefined;
  state: UpdateState;
  current: string;
  available?: string | undefined;
  required?: boolean | undefined;
  /** The version the machine ran before a rolled back update. */
  failure?: UpdateFailure | null | undefined;
  /** When the next check is, already formatted. */
  nextCheck?: string | undefined;
  /** The Check request is open. */
  checking?: boolean | undefined;
  /** The last check, already formatted (CRSP368): when, who asked and whether a person or crsp did. */
  lastCheck?: { time: string; by: string; kind: 'manual' | 'automatic' } | undefined;
  /** Where the full update history lives (the Audit log). */
  historyTo?: string | undefined;
  /** 0 to 100, while a step runs. A step without a percent shows an unknown-length bar. */
  percent?: number | undefined;
  /** The service is down for the install: one line in the pane, never a Note (CRSP364). */
  restarting?: boolean | undefined;
  /** An admin locked the policy: who set it. The controls are then read only. */
  lockedBy?: string | undefined;
  onPolicy?: ((policy: UpdatePolicy) => void) | undefined;
  onWindow?: ((window: { start: string; end: string } | null) => void) | undefined;
  onCheck?: (() => void) | undefined;
  onInstall?: (() => void) | undefined;
}

const RUNNING_STEPS: readonly UpdateState[] = [
  'downloading',
  'verifying',
  'installing',
  'testing',
];

const TIME = /^([01]\d|2[0-3]):[0-5]\d$/;

const STATE_KIND: Record<UpdateState, StatusKind> = {
  idle: 'ok',
  checking: 'running',
  available: 'info',
  waiting_for_jobs: 'waiting',
  downloading: 'running',
  verifying: 'running',
  installing: 'running',
  testing: 'running',
  updated: 'ok',
  rolled_back: 'failed',
  refused: 'failed',
  failed: 'failed',
};

/** The state of the update in one sentence (the retry and failure text included). */
export function updateStateLine(
  p: Pick<UpdatesCardProps, 'state' | 'current' | 'available' | 'failure'>,
): string {
  const version = p.available ?? p.current;
  switch (p.state) {
    case 'idle':
      return p.available === undefined && p.current === ''
        ? lw('not_looked')
        : lw('up_to_date', { version: p.current });
    case 'checking':
      return lw('checking');
    case 'available':
      return lw('available.title', { version });
    case 'waiting_for_jobs':
      return lw('waiting.title', { version });
    case 'downloading':
    case 'verifying':
    case 'installing':
    case 'testing':
      return lw(`step.${p.state}`);
    case 'updated':
      return lw('updated.title', { version: p.current });
    case 'rolled_back':
      return p.failure && p.failure.retryAt === ''
        ? lw('rolled_back.skipped', { version, previous: p.current })
        : lw('rolled_back.title', {
            version,
            previous: p.current,
            time: p.failure?.retryAt ?? '',
          });
    case 'refused':
      return lw('refused.title', { version });
    case 'failed':
      return lw('banner.failed');
  }
}

/** The Updates card: two policies, an optional time window, Check, and the state with its retry text. */
export function UpdatesCard(p: UpdatesCardProps) {
  const locked = Boolean(p.lockedBy);
  const [start, setStart] = useState(p.window?.start ?? '');
  const [end, setEnd] = useState(p.window?.end ?? '');
  const bad = (v: string) =>
    v !== '' && !TIME.test(v) ? lw('policy.window_help') : undefined;
  const commit = (s: string, e: string) => {
    if (s === '' && e === '') p.onWindow?.(null);
    else if (TIME.test(s) && TIME.test(e)) p.onWindow?.({ start: s, end: e });
  };
  return (
    <div
      data-lifecycle="updates-card"
      data-state={p.state}
      data-locked={locked || undefined}
    >
      <Pane
        title={lw('updates.title')}
        actions={
          <Button
            kind={p.state === 'available' && p.onInstall ? 'default' : 'primary'}
            onPress={p.onCheck}
            busy={p.checking === true || p.state === 'checking'}
            busyLabel={lw('checking')}
            disabled={locked}
          >
            {lw('check')}
          </Button>
        }
      >
        <Stack>
          {p.restarting ? (
            <Status kind="waiting" word={lw('state.restarting')} />
          ) : (
            <>
              <Status kind={STATE_KIND[p.state] ?? 'off'} word={lw(`state.${p.state}`)} />
              {updateStateLine(p) === lw(`state.${p.state}`) ? null : (
                <Text>{updateStateLine(p)}</Text>
              )}
              {RUNNING_STEPS.includes(p.state) ? (
                <Progress
                  label={lw(`step.${p.state as UpdateStep}`)}
                  value={p.percent}
                  max={100}
                />
              ) : null}
            </>
          )}
          {p.required ? <Text>{lw('required')}</Text> : null}
          {p.state === 'available' && p.onInstall ? (
            <Button kind="primary" onPress={p.onInstall}>
              {lw('available.primary', { version: p.available ?? p.current })}
            </Button>
          ) : null}
          <Text>
            {p.lastCheck
              ? lw(`updates.last_check.${p.lastCheck.kind}`, {
                  time: p.lastCheck.time,
                  who: p.lastCheck.by,
                })
              : lw('not_looked')}
          </Text>
          {p.historyTo ? (
            <Text>
              <TextLink to={p.historyTo}>{lw('updates.audit_link')}</TextLink>
            </Text>
          ) : null}
          <Facts
            items={[
              { name: lw('updates.current'), value: p.current },
              ...(p.nextCheck ? [{ name: lw('updates.next_check'), value: p.nextCheck }] : []),
            ]}
          />
          <ChoiceField
            label={lw('policy.title')}
            value={p.policy}
            onChange={(v) => p.onPolicy?.(v as UpdatePolicy)}
            choices={(['automatic_when_idle', 'ask_first'] as const).map((k) => ({
              value: k,
              label: lw(`policy.${k}`),
              description: lw(`policy.${k}.detail`),
              disabledReason: locked
                ? lw('updates.locked_by', { who: p.lockedBy ?? '' })
                : undefined,
            }))}
          />
          {p.policy === 'automatic_when_idle' ? (
            <Stack gap="tight">
              <TextField
                label={lw('policy.window_from')}
                value={start}
                optional
                mono
                width="short"
                error={bad(start)}
                readOnly={locked}
                onChange={(v) => {
                  setStart(v);
                  commit(v, end);
                }}
              />
              <TextField
                label={lw('policy.window_to')}
                value={end}
                optional
                mono
                width="short"
                help={lw('policy.window_help')}
                error={bad(end)}
                readOnly={locked}
                onChange={(v) => {
                  setEnd(v);
                  commit(start, v);
                }}
              />
            </Stack>
          ) : null}
        </Stack>
      </Pane>
    </div>
  );
}

// ---------------------------------------------------------------- UpdateHistory

export interface AuditEvent {
  at: string;
  actor: string;
  action: 'check' | 'install' | 'rollback' | 'remove';
  version: string;
  result: 'ok' | 'failed' | 'skipped';
}

const RESULT_KIND: Record<AuditEvent['result'], StatusKind> = {
  ok: 'ok',
  failed: 'failed',
  skipped: 'off',
};

/** One audit event per row: when, who, what, version, result. The result is a word and a glyph. */
export function UpdateHistory({ events }: { events: readonly AuditEvent[] }) {
  const columns: Column<AuditEvent>[] = [
    { id: 'at', header: lw('history.when'), cell: (e) => e.at, sort: (e) => e.at },
    { id: 'actor', header: lw('history.who'), cell: (e) => e.actor },
    { id: 'action', header: lw('history.what'), cell: (e) => lw(`audit.${e.action}`) },
    {
      id: 'version',
      header: lw('history.version'),
      cell: (e) => e.version,
      secondary: true,
    },
    {
      id: 'result',
      header: lw('history.result'),
      cell: (e) => (
        <Status kind={RESULT_KIND[e.result] ?? 'off'} word={lw(`result.${e.result}`)} />
      ),
    },
  ];
  return (
    <div data-lifecycle="update-history">
      {events.length === 0 ? (
        <Pane title={lw('history.title')}>
          <Text>{lw('history.empty')}</Text>
        </Pane>
      ) : (
        <Table
          caption={lw('history.title')}
          title={lw('history.title')}
          columns={columns}
          rows={events}
          rowKey={(e) => `${e.at}|${e.actor}|${e.action}|${e.version}`}
        />
      )}
    </div>
  );
}

// ---------------------------------------------------------------- RemoteUpdate

export interface RemoteUpdateProps {
  machine: string;
  from: string;
  to: string;
  state: 'ready' | 'running' | 'done' | 'failed';
  percent?: number | undefined;
  onStart?: (() => void) | undefined;
}

/** Start an update on another machine from a phone: one column, one primary action. */
export function RemoteUpdate(p: RemoteUpdateProps) {
  const name = { machine: p.machine, from: p.from, to: p.to };
  return (
    <div data-lifecycle="remote-update" data-state={p.state}>
      <Pane title={lw('remote.title', name)}>
        <Stack>
          <Text>{lw('remote.from_to', name)}</Text>
          {p.state === 'running' ? (
            <Progress label={lw('remote.running', name)} value={p.percent} max={100} />
          ) : null}
          {p.state === 'done' ? (
            <Status kind="ok" word={lw('remote.done', name)} />
          ) : null}
          {p.state === 'failed' ? (
            <Status kind="failed" word={lw('remote.failed', name)} />
          ) : null}
          {p.state === 'ready' || p.state === 'failed' ? (
            <Button kind="primary" onPress={p.onStart}>
              {p.state === 'failed' ? lw('action.retry') : lw('remote.start', name)}
            </Button>
          ) : null}
        </Stack>
      </Pane>
    </div>
  );
}

// ---------------------------------------------------------------- RemoveDialog

export type RemoveChoice = 'keep' | 'purge';

export interface RemoveDialogProps {
  product: string;
  /** Size of the user's data, already formatted (for example `1.4 GB`). Left out when the controller did not say. */
  size?: string | undefined;
  /** Where the data lives. Left out when the controller did not say. */
  path?: string | undefined;
  /** The request is open: Remove locks and says so. */
  busy?: boolean | undefined;
  onRemove: (choice: RemoveChoice) => void;
  onCancel: () => void;
}

/** Remove asks every time with no default: Remove stays disabled until a choice is made. */
export function RemoveDialog(p: RemoveDialogProps) {
  const [choice, setChoice] = useState<RemoveChoice | ''>('');
  const name = { product: p.product, size: p.size ?? '', path: p.path ?? '' };
  const known = p.size !== undefined && p.path !== undefined;
  return (
    <Dialog
      title={lw('remove.title', name)}
      onClose={p.onCancel}
      cancelLabel={lw('remove.cancel')}
      primary={{
        label: lw('remove.confirm', name),
        kind: 'danger',
        disabled: choice === '',
        reason: lw('remove.confirm_disabled_until'),
        busy: p.busy,
        busyLabel: lw('remove.busy'),
        onPress: () => {
          if (choice !== '') p.onRemove(choice);
        },
      }}
    >
      <Stack>
        <Text>{lw('remove.runners')}</Text>
        <ChoiceField
          label={lw('remove.choose')}
          value={choice}
          onChange={(v) => setChoice(v as RemoveChoice)}
          choices={(['keep', 'purge'] as const).map((k) => ({
            value: k,
            label:
              k === 'purge' && !known
                ? lw('remove.purge_short')
                : lw(`remove.${k}`, name),
            description: lw(`remove.${k}.detail`),
          }))}
        />
      </Stack>
    </Dialog>
  );
}

// ---------------------------------------------------------------- FleetRows

export type FleetState = 'up_to_date' | 'waiting' | 'needs_you' | 'updating' | 'failed';

export interface FleetRow {
  id: string;
  machine: string;
  version: string;
  state: FleetState;
}

const FLEET_KIND: Record<FleetState, StatusKind> = {
  up_to_date: 'ok',
  waiting: 'waiting',
  needs_you: 'info',
  updating: 'running',
  failed: 'failed',
};

/** One row per machine: the version and a state word with a glyph (UX-58). `action` adds a button to a row. */
export function FleetRows({
  rows,
  action,
}: {
  rows: readonly FleetRow[];
  action?: ((row: FleetRow) => ReactNode) | undefined;
}) {
  const columns: Column<FleetRow>[] = [
    {
      id: 'machine',
      header: lw('fleet.title'),
      cell: (r) => r.machine,
      sort: (r) => r.machine,
    },
    { id: 'version', header: lw('updates.current'), cell: (r) => r.version },
    {
      id: 'state',
      header: lw('updates.state'),
      cell: (r) => (
        <Status kind={FLEET_KIND[r.state] ?? 'off'} word={lw(`fleet.${r.state}`)} />
      ),
    },
  ];
  if (action) columns.push({ id: 'action', header: '', cell: (r) => action(r) });
  return (
    <div data-lifecycle="fleet-rows">
      {rows.length === 0 ? (
        <Pane title={lw('fleet.title')}>
          <Text>{lw('fleet.empty')}</Text>
        </Pane>
      ) : (
        <Table
          caption={lw('fleet.title')}
          title={lw('fleet.title')}
          columns={columns}
          rows={rows}
          rowKey={(r) => r.id}
        />
      )}
    </div>
  );
}
