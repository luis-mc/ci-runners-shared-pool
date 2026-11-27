// Settings, Notifications (notifications R5.7 to R5.9, R5.19, R5.29 to R5.31; web-console 4.9): the channels (email,
// webhook, desktop), the events each one gets and the lowest severity. A table of channels; an add or edit form that
// ends with Cancel and Save (UX-24); Send test with its one result line (UX-27) and a busy button (UX-13); a delete
// that asks first and names the channel and its events (UX-62). A secret is write-only: the screen never shows one
// again and says only whether one is set (notifications R6.2).
import {
  Button,
  CheckRow,
  ChoiceField,
  Cluster,
  Empty,
  ErrorSummary,
  LinkButton,
  Message,
  NumberField,
  Section,
  Stack,
  Status,
  Table,
  Text,
  TextField,
  formatDateTime,
  type Column,
} from '@crsp/ui';
import { useState } from 'react';
import { unwrap, useClient, type Schemas } from '../../api/api';
import { useCommand, useLoad, useQuestion } from '../../api/hooks';
import { useSession } from '../../auth/session';
import { Ask, Load } from '../common';
import { useTabPart } from './TabForm';

type Channel = Schemas['NotificationChannel'];
type Kind = Schemas['NotificationKind'];
type EventType = Schemas['NotificationEvent'];
type Severity = Schemas['NotificationSeverity'];

export const EVENT_WORDS: Record<EventType, string> = {
  'machine.offline': 'Machine offline',
  'job.waiting': 'Job waiting',
  'disk.low': 'Disk low',
  'runner.outdated': 'Runner outdated',
  'doctor.finding': 'Doctor finding',
  'update.available': 'Update available',
};
const EVENTS = Object.keys(EVENT_WORDS) as EventType[];
const SEVERITY_WORDS: Record<Severity, string> = {
  info: 'Info',
  warning: 'Warning',
  error: 'Error',
};
const KIND_WORDS: Record<Kind, string> = {
  email: 'Email',
  webhook: 'Webhook',
  desktop: 'Desktop',
};
const MIN_SIGNING_LENGTH = 16;

interface Draft {
  id: string | undefined;
  /** The name the channel had when the form opened: the form's heading does not change while typing. */
  title: string;
  kind: Kind;
  name: string;
  enabled: boolean;
  events: EventType[];
  minSeverity: Severity;
  address: string;
  url: string;
  secret: string;
  smtpHost: string;
  smtpPort: number;
  smtpUser: string;
  smtpFrom: string;
  smtpPassword: string;
  removePassword: boolean;
  /** The channel already has this secret (the screen cannot show it). */
  hadSecret: boolean;
}

const BLANK: Draft = {
  id: undefined,
  title: '',
  kind: 'email',
  name: '',
  enabled: true,
  events: ['machine.offline'],
  minSeverity: 'warning',
  address: '',
  url: '',
  secret: '',
  smtpHost: '',
  smtpPort: 587,
  smtpUser: '',
  smtpFrom: '',
  smtpPassword: '',
  removePassword: false,
  hadSecret: false,
};

function draftOf(c: Channel): Draft {
  return {
    ...BLANK,
    id: c.id,
    title: c.name,
    kind: c.kind,
    name: c.name,
    enabled: c.enabled,
    events: c.events,
    minSeverity: c.min_severity,
    address: c.address ?? '',
    url: c.url ?? '',
    smtpHost: c.smtp?.host ?? '',
    smtpPort: c.smtp?.port ?? 587,
    smtpUser: c.smtp?.username ?? '',
    smtpFrom: c.smtp?.from ?? '',
    hadSecret: c.kind === 'webhook' ? c.secret_set : (c.smtp?.has_password ?? false),
  };
}

/** A secret of 32 random bytes in hex, made in the browser; it is shown once, in the field. */
export function makeSecret(): string {
  const bytes = new Uint8Array(32);
  crypto.getRandomValues(bytes);
  return Array.from(bytes, (b) => b.toString(16).padStart(2, '0')).join('');
}

export function problemsOf(d: Draft): string[] {
  const out: string[] = [];
  const editing = d.id !== undefined;
  if (!d.name.trim()) out.push('Write a name for the channel.');
  if (d.events.length === 0) out.push('Pick at least one event.');
  if (d.kind === 'email') {
    if (!/^[^\s@<>,;"]+@[^\s@<>,;"]+\.[^\s@<>,;"]+$/.test(d.address.trim()))
      out.push('Write the address as an email address, for example name@example.com.');
    if (!d.smtpHost.trim())
      out.push('Write the mail server, for example smtp.example.com.');
    if (!/^[^\s@<>,;"]+@[^\s@<>,;"]+\.[^\s@<>,;"]+$/.test(d.smtpFrom.trim()))
      out.push('Write the sender as an email address, for example crsp@example.com.');
    if (d.smtpPassword && !d.smtpUser.trim()) out.push('A password needs a user name.');
  }
  if (d.kind === 'webhook') {
    if (!/^https:\/\/[^\s/]+/i.test(d.url.trim()))
      out.push('Write the webhook address as an https URL.');
    if (!editing && d.secret.length < MIN_SIGNING_LENGTH)
      out.push(`Write a signing secret of at least ${MIN_SIGNING_LENGTH} characters.`);
    if (editing && d.secret && d.secret.length < MIN_SIGNING_LENGTH)
      out.push(`A new signing secret needs at least ${MIN_SIGNING_LENGTH} characters.`);
  }
  return out;
}

function createBody(d: Draft): Schemas['NotificationChannelInput'] {
  const base = {
    kind: d.kind,
    name: d.name.trim(),
    enabled: d.enabled,
    events: d.events,
    min_severity: d.minSeverity,
  };
  if (d.kind === 'email')
    return {
      ...base,
      address: d.address.trim(),
      smtp: {
        host: d.smtpHost.trim(),
        port: d.smtpPort,
        from: d.smtpFrom.trim(),
        ...(d.smtpUser.trim() ? { username: d.smtpUser.trim() } : {}),
        ...(d.smtpPassword ? { password: d.smtpPassword } : {}),
      },
    };
  if (d.kind === 'webhook') return { ...base, url: d.url.trim(), secret: d.secret };
  return base;
}

function updateBody(d: Draft): Schemas['NotificationChannelUpdate'] {
  const base = {
    name: d.name.trim(),
    enabled: d.enabled,
    events: d.events,
    min_severity: d.minSeverity,
  };
  if (d.kind === 'email')
    return {
      ...base,
      address: d.address.trim(),
      smtp: {
        host: d.smtpHost.trim(),
        port: d.smtpPort,
        from: d.smtpFrom.trim(),
        username: d.smtpUser.trim(),
        // A password left empty stays; Remove the password sends an empty one.
        ...(d.removePassword
          ? { password: '' }
          : d.smtpPassword
            ? { password: d.smtpPassword }
            : {}),
      },
    };
  if (d.kind === 'webhook')
    return { ...base, url: d.url.trim(), ...(d.secret ? { secret: d.secret } : {}) };
  return base;
}

function sendsTo(c: Channel): string {
  if (c.kind === 'email') return c.address ?? '';
  if (c.kind === 'webhook') {
    try {
      return new URL(c.url ?? '').host;
    } catch {
      return 'Not set';
    }
  }
  return 'This user';
}

export function NotificationsSettings() {
  const client = useClient();
  const session = useSession();
  const { result, reload } = useLoad(
    () => unwrap(client.GET('/api/v1/notification-channels')),
    [],
  );
  const [draft, setDraft] = useState<Draft | undefined>(undefined);
  // What the open form started from: a field is "Changed" when it differs from this.
  const [base, setBase] = useState<Draft>(BLANK);
  const open = (d: Draft) => {
    setBase(d);
    setDraft(d);
  };
  const differs = (k: keyof Draft) =>
    draft !== undefined && JSON.stringify(draft[k]) !== JSON.stringify(base[k]);
  const [errors, setErrors] = useState<string[]>([]);
  const [saved, setSaved] = useState<string | undefined>(undefined);
  const [target, setTarget] = useState<Channel | undefined>(undefined);
  const [testing, setTesting] = useState<string | undefined>(undefined);
  const [testLine, setTestLine] = useState<
    { ok: boolean; name: string; text: string } | undefined
  >(undefined);

  const set = (patch: Partial<Draft>) => setDraft((d) => (d ? { ...d, ...patch } : d));

  const save = useCommand(async () => {
    if (!draft) return false;
    const problems = problemsOf(draft);
    setErrors(problems);
    if (problems.length) return false;
    if (draft.id) {
      await unwrap(
        client.PATCH('/api/v1/notification-channels/{channelId}', {
          params: { path: { channelId: draft.id } },
          body: updateBody(draft),
        }),
      );
    } else {
      await unwrap(
        client.POST('/api/v1/notification-channels', { body: createBody(draft) }),
      );
    }
    setSaved(`Channel ${draft.name.trim()} saved.`);
    setDraft(undefined);
    reload();
    return true;
  });
  useTabPart('notifications', {
    changes: draft ? 1 : 0,
    save: async () => (await save.run()) === true,
    reset: () => {
      setDraft(undefined);
      setErrors([]);
      save.clear();
    },
  });

  const test = useCommand(async (c: Channel) => {
    setTesting(c.id);
    setTestLine(undefined);
    try {
      const r = await unwrap(
        client.POST('/api/v1/notification-channels/{channelId}/test', {
          params: { path: { channelId: c.id } },
        }),
      );
      setTestLine({ ok: r.ok, name: c.name, text: r.message ?? '' });
      reload();
    } finally {
      setTesting(undefined);
    }
  });

  const remove = useQuestion(
    async () => {
      if (!target) return;
      await unwrap(
        client.DELETE('/api/v1/notification-channels/{channelId}', {
          params: { path: { channelId: target.id }, query: { confirm: true } },
        }),
      );
    },
    () => {
      setSaved(`Channel ${target?.name ?? ''} deleted.`);
      setTarget(undefined);
      reload();
    },
  );

  const cannotWebhook = session.whyNot('admin');
  const allowed = (c: Channel) => c.kind !== 'webhook' || session.can('admin');

  const columns: Column<Channel>[] = [
    { id: 'name', header: 'Name', cell: (c) => c.name, sort: (c) => c.name },
    { id: 'kind', header: 'Kind', cell: (c) => KIND_WORDS[c.kind], sort: (c) => c.kind },
    { id: 'to', header: 'Sends to', cell: sendsTo },
    {
      id: 'events',
      header: 'Events',
      cell: (c) => c.events.map((e) => EVENT_WORDS[e]).join(', '),
    },
    {
      id: 'severity',
      header: 'Lowest severity',
      cell: (c) => SEVERITY_WORDS[c.min_severity],
    },
    {
      id: 'state',
      header: 'State',
      cell: (c) =>
        c.enabled ? <Status kind="ok" word="On" /> : <Status kind="off" word="Off" />,
      sort: (c) => (c.enabled ? 0 : 1),
    },
    {
      id: 'last',
      header: 'Last test',
      cell: (c) =>
        c.last_test_at ? (
          <>
            <Status
              kind={c.last_test_ok ? 'ok' : 'failed'}
              word={c.last_test_ok ? 'Delivered' : 'Failed'}
            />{' '}
            {formatDateTime(c.last_test_at)}
          </>
        ) : (
          'Not tested'
        ),
    },
    {
      id: 'do',
      header: 'Actions',
      cell: (c) => (
        <Cluster>
          <Button
            onPress={() => {
              setSaved(undefined);
              setErrors([]);
              save.clear();
              open(draftOf(c));
            }}
            disabled={!allowed(c)}
            reason={allowed(c) ? undefined : cannotWebhook}
          >
            Edit
          </Button>
          <Button
            onPress={() => void test.run(c)}
            busy={testing === c.id}
            busyLabel="Sending"
            disabled={!allowed(c) || (testing !== undefined && testing !== c.id)}
            reason={
              !allowed(c)
                ? cannotWebhook
                : testing !== undefined && testing !== c.id
                  ? 'Another test is running.'
                  : undefined
            }
          >
            Send test
          </Button>
          <Button
            kind="danger"
            onPress={() => {
              setSaved(undefined);
              setTarget(c);
              remove.ask([
                `Channel ${c.name} stops carrying ${c.events.map((e) => EVENT_WORDS[e]).join(', ')}.`,
              ]);
            }}
            disabled={!allowed(c)}
            reason={allowed(c) ? undefined : cannotWebhook}
          >
            Delete
          </Button>
        </Cluster>
      ),
    },
  ];

  const kindChoices = [
    {
      value: 'email',
      label: 'Email',
      description: 'Sent through a mail server you name.',
    },
    {
      value: 'webhook',
      label: 'Webhook',
      description: 'A signed message to a link, for the whole pool.',
      ...(cannotWebhook ? { disabledReason: cannotWebhook } : {}),
    },
    {
      value: 'desktop',
      label: 'Desktop',
      description: 'A notice from the tray on your computer.',
    },
  ];

  return (
    <Stack>
      <Section title="Notification channels">
        <Load result={result} what="the notification channels" reload={reload}>
          {(data) =>
            data.items.length === 0 ? (
              <Empty title="No channels yet. Add a channel to be told when a machine, job or disk needs you." />
            ) : (
              <Table
                caption="Notification channels"
                columns={columns}
                rows={data.items}
                rowKey={(c) => c.id}
              />
            )
          }
        </Load>
        <Cluster>
          <Text>What the pool sent, and what became of each message.</Text>
          <LinkButton to="/notices">See the notices</LinkButton>
        </Cluster>
        {testing ? <Text>Sending a test. Waiting for the answer.</Text> : null}
        {testLine ? (
          <Message tone={testLine.ok ? 'done' : 'problem'}>
            {testLine.ok ? '' : 'Test failed. '}
            {testLine.name}: {testLine.text}
          </Message>
        ) : null}
        {test.error ? (
          <Message tone="problem">Could not send the test. {test.error}</Message>
        ) : null}
        {saved ? <Message tone="done">{saved}</Message> : null}
        {draft ? null : (
          <Cluster>
            <Button
              onPress={() => {
                setSaved(undefined);
                setErrors([]);
                save.clear();
                open({ ...BLANK });
              }}
            >
              Add channel
            </Button>
          </Cluster>
        )}
      </Section>
      {draft ? (
        <Section title={draft.id ? `Edit ${draft.title}` : 'Add a channel'}>
          <div>
            <Stack>
              <ErrorSummary errors={errors} />
              {draft.id ? null : (
                <ChoiceField
                  label="Kind"
                  value={draft.kind}
                  changed={differs('kind')}
                  onChange={(v) => set({ kind: v as Kind })}
                  choices={kindChoices}
                />
              )}
              <TextField
                label="Name"
                value={draft.name}
                changed={differs('name')}
                onChange={(name) => set({ name })}
              />
              {draft.kind === 'email' ? (
                <>
                  <TextField
                    label="Address"
                    help="The mailbox the notices go to."
                    value={draft.address}
                    changed={differs('address')}
                    onChange={(address) => set({ address })}
                  />
                  <TextField
                    label="Mail server"
                    help="A host name, for example smtp.example.com. The password goes only over an encrypted link."
                    value={draft.smtpHost}
                    changed={differs('smtpHost')}
                    onChange={(smtpHost) => set({ smtpHost })}
                  />
                  <NumberField
                    label="Mail server port"
                    value={draft.smtpPort}
                    changed={differs('smtpPort')}
                    min={1}
                    max={65535}
                    onChange={(smtpPort) => set({ smtpPort })}
                  />
                  <TextField
                    label="User name"
                    optional
                    value={draft.smtpUser}
                    changed={differs('smtpUser')}
                    onChange={(smtpUser) => set({ smtpUser })}
                  />
                  <TextField
                    label="Password"
                    optional
                    secret
                    help={
                      draft.hadSecret
                        ? 'A password is set. Leave this empty to keep it.'
                        : 'No password is set. It is kept in the secret store and never shown again.'
                    }
                    value={draft.smtpPassword}
                    changed={differs('smtpPassword')}
                    onChange={(smtpPassword) => set({ smtpPassword })}
                  />
                  {draft.hadSecret ? (
                    <CheckRow
                      label="Remove the password"
                      checked={draft.removePassword}
                      changed={differs('removePassword')}
                      onChange={(removePassword) => set({ removePassword })}
                    />
                  ) : null}
                  <TextField
                    label="Sender"
                    help="The address the notices come from."
                    value={draft.smtpFrom}
                    changed={differs('smtpFrom')}
                    onChange={(smtpFrom) => set({ smtpFrom })}
                  />
                </>
              ) : null}
              {draft.kind === 'webhook' ? (
                <>
                  <TextField
                    label="Web address"
                    help="An https address. Redirects are not followed."
                    value={draft.url}
                    changed={differs('url')}
                    onChange={(url) => set({ url })}
                  />
                  <TextField
                    label="Signing secret"
                    mono
                    optional={draft.id !== undefined}
                    help={
                      draft.id
                        ? 'A secret is set. Leave this empty to keep it, or write a new one.'
                        : `At least ${MIN_SIGNING_LENGTH} characters. It is shown here once, then kept only in the secret store.`
                    }
                    value={draft.secret}
                    changed={differs('secret')}
                    onChange={(secret) => set({ secret })}
                  />
                  <Cluster>
                    <Button onPress={() => set({ secret: makeSecret() })}>
                      Make a secret
                    </Button>
                  </Cluster>
                </>
              ) : null}
              {draft.kind === 'desktop' ? (
                <Text tone="muted">
                  A desktop notice comes from the tray of your signed-in computer.
                </Text>
              ) : null}
              <Stack>
                <Text>Events</Text>
                {EVENTS.map((e) => (
                  <CheckRow
                    key={e}
                    label={EVENT_WORDS[e]}
                    checked={draft.events.includes(e)}
                    changed={base.events.includes(e) !== draft.events.includes(e)}
                    onChange={(on) =>
                      set({
                        events: EVENTS.filter((x) =>
                          x === e ? on : draft.events.includes(x),
                        ),
                      })
                    }
                  />
                ))}
              </Stack>
              <ChoiceField
                label="Lowest severity"
                help="Doctor findings below this are not sent."
                value={draft.minSeverity}
                changed={differs('minSeverity')}
                onChange={(v) => set({ minSeverity: v as Severity })}
                choices={(Object.keys(SEVERITY_WORDS) as Severity[]).map((s) => ({
                  value: s,
                  label: SEVERITY_WORDS[s],
                }))}
              />
              <CheckRow
                label="On"
                checked={draft.enabled}
                changed={differs('enabled')}
                onChange={(enabled) => set({ enabled })}
              />
              {save.error ? (
                <Message tone="problem">Could not save the channel. {save.error}</Message>
              ) : null}
            </Stack>
          </div>
        </Section>
      ) : null}
      <Ask
        title={`Delete ${target?.name ?? 'the channel'}?`}
        action="Delete"
        busyLabel="Deleting"
        q={remove.question}
        busy={remove.busy}
        error={remove.error}
        onCancel={() => {
          remove.cancel();
          setTarget(undefined);
        }}
        onConfirm={() => void remove.confirm()}
      />
    </Stack>
  );
}
