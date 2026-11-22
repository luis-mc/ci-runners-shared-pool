// Settings (web-console 4.9, R5.27, R5.28): Users and roles, Appearance, Retention, Backups. Settings hold
// configuration only (UX-52). Each sub-page is a form that ends with Cancel and Save (UX-24), or a switch or
// one-choice list saved at once (UX-22).
import {
  Button,
  ChoiceField,
  Cluster,
  Dialog,
  Empty,
  ErrorSummary,
  Facts,
  FieldGrid,
  FileField,
  FormActions,
  Loading,
  Message,
  NumberField,
  Page,
  PageHeader,
  ListToolbar,
  SearchBar,
  Section,
  Stack,
  Status,
  Table,
  THEMES,
  Tabs,
  TextField,
  TimePicker,
  applyThemeName,
  formatDateTime,
  formatSize,
  rememberThemeMode,
  savedThemeName,
  type Column,
} from '@crsp/ui';
import { lazy, Suspense, useEffect, useState, type ReactNode } from 'react';
import { Navigate, Outlet, useLocation, useNavigate } from 'react-router-dom';
import { unwrap, useClient, type Schemas } from '../../api/api';
import { useCommand, useLoad, useQuestion } from '../../api/hooks';
import { useSession } from '../../auth/session';
import { Ask, Load } from '../common';
import { TabForm, useTabPart } from './TabForm';
import { SECTIONS, settingsPath } from './sections';
import { AuditLogSettings, ConfigExportSettings } from './AuditLog';
import { InsightsUpdateSettings } from './InsightsUpdate';
import { BudgetsSettings } from './Budgets';
import { NotificationsSettings } from './Notifications';
import { PublicRepositoriesSettings } from './PublicRepositoriesSettings';
import { NetworkSettings } from './ListenAddress';
const UpdatesSettings = lazy(() =>
  import('./UpdatesLifecycle').then((m) => ({ default: m.UpdatesSettings })),
);
import { WebhooksSettings } from './Webhooks';

type User = Schemas['User'];

// Six tabs (owner decision D8; Updates after Console, LOOK43). Each old section is a pane inside its tab, in this order; `SECTIONS` says where
// an old address now lives, so a link or bookmark to /settings/<old> lands on the right tab and section.
const TABS = [
  { id: 'access', label: 'Access' },
  { id: 'repositories', label: 'Repositories' },
  { id: 'console', label: 'Console' },
  { id: 'updates', label: 'Updates' },
  { id: 'data', label: 'Data' },
  { id: 'audit', label: 'Audit log' },
];

/** An old Settings address: goes to its tab and scrolls to its section. */
export function OldSettingsAddress({ old }: { old: string }) {
  return <Navigate to={settingsPath(old)} replace />;
}

/** The anchor of one section inside its tab (no look of its own). */
function At({ id, children }: { id: string; children: ReactNode }) {
  return <div id={id}>{children}</div>;
}

export function SettingsLayout() {
  const navigate = useNavigate();
  const { pathname, hash } = useLocation();
  // An old address shows its new tab at once, before the redirect has run.
  const segment = pathname.split('/')[2] ?? '';
  const current = TABS.some((t) => t.id === segment)
    ? segment
    : (SECTIONS[segment]?.tab ?? 'access');
  // Scroll to the section named by the address, once its tab is on screen.
  useEffect(() => {
    if (!hash) return;
    document.getElementById(hash.slice(1))?.scrollIntoView?.();
  }, [pathname, hash]);
  return (
    <Page>
      <PageHeader title="Settings" lead="Configuration only." />
      <Tabs
        label="Settings"
        tabs={TABS}
        current={current}
        onChange={(id) => void navigate(`/settings/${id}`)}
      />
      <Outlet />
    </Page>
  );
}

export function AccessTab() {
  return (
    <TabForm>
      <At id="users">
        <UsersSettings />
      </At>
      <At id="github-app">
        <AppKeySettings />
      </At>
      <At id="webhooks">
        <WebhooksSettings />
      </At>
    </TabForm>
  );
}

export function RepositoriesTab() {
  return (
    <TabForm>
      <At id="public-repositories">
        <PublicRepositoriesSettings />
      </At>
      <At id="budgets">
        <BudgetsSettings />
      </At>
    </TabForm>
  );
}

export function ConsoleTab() {
  const { hash } = useLocation();
  // The Updates pane used to be the last section of this tab: its old anchor goes to its own tab.
  if (hash === '#updates') return <Navigate to="/settings/updates" replace />;
  return (
    <TabForm>
      <At id="appearance">
        <AppearanceSettings />
      </At>
      <At id="notifications">
        <NotificationsSettings />
      </At>
      <At id="network">
        <NetworkSettings />
      </At>
    </TabForm>
  );
}

/** No Save bar here: the Updates card saves each change at once (UX-22). */
export function UpdatesTab() {
  return (
    <At id="updates">
      <Suspense fallback={<Loading what="Loading the update status" />}>
        <UpdatesSettings />
      </Suspense>
    </At>
  );
}

export function DataTab() {
  return (
    <TabForm>
      <At id="retention">
        <RetentionSettings />
      </At>
      <At id="insights-update">
        <InsightsUpdateSettings />
      </At>
      <At id="backups">
        <BackupsSettings />
      </At>
      <At id="config-export">
        <ConfigExportSettings />
      </At>
    </TabForm>
  );
}

export function AuditTab() {
  return <AuditLogSettings />;
}

export function UsersSettings() {
  const client = useClient();
  const session = useSession();
  const owner = session.can('owner');
  const me =
    session.state.status === 'ready' ? session.state.session.user.login : 'account';
  const { result, reload } = useLoad(
    () =>
      owner
        ? unwrap(client.GET('/api/v1/users'))
        : Promise.resolve({ items: [] as User[] }),
    [owner],
  );
  const [login, setLogin] = useState('');
  const [role, setRole] = useState<'admin' | 'viewer'>('viewer');
  const [errors, setErrors] = useState<string[]>([]);
  const [find, setFind] = useState('');
  const [editing, setEditing] = useState<User | undefined>(undefined);
  const [newRole, setNewRole] = useState<Schemas['Role']>('viewer');
  const [target, setTarget] = useState<User | undefined>(undefined);

  const add = useCommand(async () => {
    const problems: string[] = [];
    if (!login.trim()) problems.push('Write the GitHub login to add.');
    setErrors(problems);
    if (problems.length) return false;
    await unwrap(client.POST('/api/v1/users', { body: { login: login.trim(), role } }));
    setLogin('');
    setRole('viewer');
    reload();
    return true;
  });
  // The "Add a user" form is a part of the Access tab: its fields count as changes, and Save in the bar adds.
  useTabPart('users', {
    changes: (login !== '' ? 1 : 0) + (role !== 'viewer' ? 1 : 0),
    save: async () => (await add.run()) === true,
    reset: () => {
      setLogin('');
      setRole('viewer');
      setErrors([]);
      add.clear();
    },
    blocked: owner ? undefined : session.whyNot('owner'),
  });
  const change = useCommand(async () => {
    if (!editing) return;
    await unwrap(
      client.PATCH('/api/v1/users/{userId}', {
        params: { path: { userId: editing.id } },
        body: { role: newRole },
      }),
    );
    setEditing(undefined);
    reload();
  });
  const remove = useQuestion(
    async () => {
      if (!target) return;
      await unwrap(
        client.DELETE('/api/v1/users/{userId}', {
          params: { path: { userId: target.id } },
        }),
      );
    },
    () => {
      setTarget(undefined);
      reload();
    },
  );

  const columns: Column<User>[] = [
    { id: 'login', header: 'GitHub login', cell: (u) => u.login, sort: (u) => u.login },
    { id: 'role', header: 'Role', cell: (u) => u.role, sort: (u) => u.role },
    { id: 'by', header: 'Added by', cell: (u) => u.added_by ?? 'the first sign-in' },
    {
      id: 'at',
      header: 'Added at',
      cell: (u) => formatDateTime(u.added_at),
      sort: (u) => u.added_at,
    },
    {
      id: 'last',
      header: 'Last sign-in',
      cell: (u) => (u.last_sign_in_at ? formatDateTime(u.last_sign_in_at) : 'never'),
    },
    {
      id: 'do',
      header: 'Actions',
      cell: (u) =>
        u.role === 'owner' ? (
          'The owner'
        ) : (
          <Cluster>
            <Button
              onPress={() => {
                setEditing(u);
                setNewRole(u.role);
              }}
            >
              Edit
            </Button>
            <Button
              kind="danger"
              onPress={() => {
                setTarget(u);
                remove.ask([`User ${u.login} can no longer sign in.`]);
              }}
            >
              Remove
            </Button>
          </Cluster>
        ),
    },
  ];

  if (!owner)
    return (
      <Message tone="needs-you">
        Only the owner can see and change users. Ask the owner to add or change one.
      </Message>
    );

  return (
    <Stack>
      <Section
        title="Users and roles"
        hint={`Who may use this console. Your own GitHub sign-in is in the ${me} menu at the top right.`}
      >
        <Load result={result} what="users" reload={reload}>
          {(data) => {
            const rows = data.items.filter((u) =>
              u.login.toLowerCase().includes(find.trim().toLowerCase()),
            );
            return (
              <Stack>
                <ListToolbar
                  label="Users"
                  search={
                    <SearchBar value={find} onChange={setFind} label="Search users" />
                  }
                  count={`${rows.length} ${rows.length === 1 ? 'user' : 'users'}`}
                />
                <Table
                  caption="Users"
                  columns={columns}
                  oneLine
                  rows={rows}
                  rowKey={(u) => String(u.id)}
                />
                {data.items.length <= 1 ? (
                  <Empty title="No other users yet. Add a GitHub login to let someone in." />
                ) : null}
              </Stack>
            );
          }}
        </Load>
      </Section>
      <Section title="Add a user">
        <div>
          <Stack>
            <ErrorSummary errors={errors} />
            <TextField
              label="GitHub login"
              value={login}
              onChange={setLogin}
              changed={login !== ''}
            />
            <ChoiceField
              label="Role"
              value={role}
              changed={role !== 'viewer'}
              onChange={(v) => setRole(v as 'admin' | 'viewer')}
              choices={[
                {
                  value: 'viewer',
                  label: 'Viewer',
                  description: 'Reads everything, changes nothing.',
                },
                {
                  value: 'admin',
                  label: 'Admin',
                  description: 'Changes repositories, machines and runners.',
                },
              ]}
            />
            {add.error ? (
              <Message tone="problem">Could not add the user. {add.error}</Message>
            ) : null}
          </Stack>
        </div>
      </Section>
      {editing ? (
        <Dialog
          title={`Edit ${editing.login}`}
          onClose={() => setEditing(undefined)}
          primary={{
            label: 'Save',
            onPress: () => void change.run(),
            busy: change.busy,
            busyLabel: 'Saving',
          }}
        >
          <ChoiceField
            label="Role"
            value={newRole}
            onChange={(v) => setNewRole(v as Schemas['Role'])}
            choices={[
              { value: 'viewer', label: 'Viewer' },
              { value: 'admin', label: 'Admin' },
            ]}
          />
          {change.error ? (
            <Message tone="problem">Could not save. {change.error}</Message>
          ) : null}
        </Dialog>
      ) : null}
      <Ask
        title={`Remove ${target?.login ?? 'the user'}?`}
        action="Remove"
        busyLabel="Removing"
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

export function applyTheme(theme: Schemas['Theme']): void {
  const root = document.documentElement;
  if (theme === 'system') root.removeAttribute('data-theme');
  else root.setAttribute('data-theme', theme);
  rememberThemeMode(theme);
}

/** Loads the user's theme once signed in and applies it (UX-60). */
export function ThemeApplier() {
  const client = useClient();
  useEffect(() => {
    applyThemeName(savedThemeName());
    let on = true;
    unwrap(client.GET('/api/v1/me/preferences'))
      .then((p) => on && applyTheme(p.theme))
      .catch(() => undefined);
    return () => {
      on = false;
    };
  }, [client]);
  return null;
}

export function AppearanceSettings() {
  const client = useClient();
  const { result, reload } = useLoad(
    () => unwrap(client.GET('/api/v1/me/preferences')),
    [],
  );
  const [themeName, setThemeName] = useState(savedThemeName);
  const save = useCommand(async (theme: Schemas['Theme']) => {
    const p = await unwrap(client.PUT('/api/v1/me/preferences', { body: { theme } }));
    applyTheme(p.theme);
    reload();
  });
  return (
    <Section title="Appearance">
      <Load result={result} what="your appearance" reload={reload}>
        {(p) => (
          <Stack>
            <ChoiceField
              label="Theme"
              help="The colours of the console. The choice is saved at once."
              value={themeName}
              onChange={(v) => {
                setThemeName(v);
                applyThemeName(v);
              }}
              choices={THEMES.map((t) => ({
                value: t.id,
                label: t.name,
                preview: t.id,
              }))}
            />
            <ChoiceField
              label="Mode"
              help="System follows your device. The choice is saved at once."
              value={p.theme}
              onChange={(v) => void save.run(v as Schemas['Theme'])}
              choices={[
                { value: 'system', label: 'System' },
                { value: 'dark', label: 'Dark' },
                { value: 'light', label: 'Light' },
              ]}
            />
            {save.busy ? (
              <Message tone="note" dismissible={false}>
                Saving the mode.
              </Message>
            ) : null}
            {save.error ? (
              <Message tone="problem">Could not save the mode. {save.error}</Message>
            ) : null}
          </Stack>
        )}
      </Load>
    </Section>
  );
}

export function RetentionSettings() {
  const client = useClient();
  const session = useSession();
  const { result, reload } = useLoad(() => unwrap(client.GET('/api/v1/settings')), []);
  return (
    <Section title="Retention">
      <Load result={result} what="retention" reload={reload}>
        {(s) => (
          <RetentionForm
            s={s}
            onSaved={reload}
            canEdit={session.can('owner')}
            why={session.whyNot('owner')}
          />
        )}
      </Load>
    </Section>
  );
}

function RetentionForm({
  s,
  onSaved,
  canEdit,
  why,
}: {
  s: Schemas['Settings'];
  onSaved: () => void;
  canEdit: boolean;
  why: string | undefined;
}) {
  const client = useClient();
  const [jobs, setJobs] = useState(s.retention_job_history_days ?? 90);
  const [logs, setLogs] = useState(s.retention_logs_days ?? 14);
  const [mib, setMib] = useState(s.retention_logs_mib_per_machine ?? 500);
  const [audit, setAudit] = useState(s.retention_audit_days ?? 365);
  const [raw, setRaw] = useState(s.retention_load_raw_hours ?? 48);
  const [errors, setErrors] = useState<string[]>([]);
  const [saved, setSaved] = useState(false);
  const was = {
    jobs: s.retention_job_history_days ?? 90,
    logs: s.retention_logs_days ?? 14,
    mib: s.retention_logs_mib_per_machine ?? 500,
    audit: s.retention_audit_days ?? 365,
    raw: s.retention_load_raw_hours ?? 48,
  };
  const changes = [
    jobs !== was.jobs,
    logs !== was.logs,
    mib !== was.mib,
    audit !== was.audit,
    raw !== was.raw,
  ].filter(Boolean).length;
  const save = useCommand(async () => {
    const p: string[] = [];
    for (const [n, v] of [
      ['Job history', jobs],
      ['Logs', logs],
      ['Audit log', audit],
    ] as const)
      if (v < 1) p.push(`${n} must be kept at least 1 day.`);
    setErrors(p);
    setSaved(false);
    if (p.length) return false;
    await unwrap(
      client.PATCH('/api/v1/settings', {
        body: {
          retention_job_history_days: jobs,
          retention_logs_days: logs,
          retention_logs_mib_per_machine: mib,
          retention_audit_days: audit,
          retention_load_raw_hours: raw,
        },
      }),
    );
    setSaved(true);
    onSaved();
    return true;
  });
  useTabPart('retention', {
    changes,
    save: async () => (await save.run()) === true,
    reset: () => {
      setJobs(was.jobs);
      setLogs(was.logs);
      setMib(was.mib);
      setAudit(was.audit);
      setRaw(was.raw);
      setErrors([]);
      save.clear();
    },
    blocked: canEdit ? undefined : (why ?? 'Only the owner can change this.'),
  });
  return (
    <div>
      <Stack>
        <ErrorSummary errors={errors} />
        <FieldGrid>
          <NumberField
            label="Job history"
            value={jobs}
            changed={jobs !== was.jobs}
            unit=" days"
            min={1}
            max={3650}
            onChange={setJobs}
          />
          <NumberField
            label="Logs"
            value={logs}
            changed={logs !== was.logs}
            unit=" days"
            min={1}
            max={3650}
            onChange={setLogs}
          />
          <NumberField
            label="Logs per machine"
            value={mib}
            changed={mib !== was.mib}
            unit=" MiB"
            min={1}
            max={100000}
            onChange={setMib}
          />
          <NumberField
            label="Audit log"
            value={audit}
            changed={audit !== was.audit}
            unit=" days"
            min={1}
            max={3650}
            onChange={setAudit}
          />
          <NumberField
            label="Raw load samples"
            value={raw}
            changed={raw !== was.raw}
            unit=" h"
            min={1}
            max={720}
            onChange={setRaw}
          />
        </FieldGrid>
        {saved ? <Message tone="done">Retention saved.</Message> : null}
        {save.error ? (
          <Message tone="problem">Could not save. {save.error}</Message>
        ) : null}
      </Stack>
    </div>
  );
}

export function BackupsSettings() {
  const client = useClient();
  const session = useSession();
  const { result, reload } = useLoad(() => unwrap(client.GET('/api/v1/backups')), []);
  const settings = useLoad(() => unwrap(client.GET('/api/v1/settings')), []);
  const admin = session.can('admin');
  const run = useCommand(async () => {
    await unwrap(client.POST('/api/v1/backups'));
    reload();
  });
  const [time, setTime] = useState<string | null>(null);
  const [keep, setKeep] = useState<number | undefined>(undefined);
  const saved = settings.result.state === 'ready' ? settings.result.data : undefined;
  const shownTime = time ?? saved?.backup_time_local ?? '03:00';
  const shownKeep = keep ?? saved?.backup_keep_days ?? 14;
  const savedTime = saved?.backup_time_local ?? '03:00';
  const savedKeep = saved?.backup_keep_days ?? 14;
  const save = useCommand(async () => {
    await unwrap(
      client.PATCH('/api/v1/settings', {
        body: { backup_time_local: shownTime, backup_keep_days: shownKeep },
      }),
    );
    setTime(null);
    setKeep(undefined);
    settings.reload();
    return true;
  });
  useTabPart('backups', {
    changes: (shownTime !== savedTime ? 1 : 0) + (shownKeep !== savedKeep ? 1 : 0),
    save: async () => (await save.run()) === true,
    reset: () => {
      setTime(null);
      setKeep(undefined);
      save.clear();
    },
    blocked: session.can('owner')
      ? undefined
      : (session.whyNot('owner') ?? 'Only the owner can change this.'),
  });
  const cols: Column<Schemas['Backup']>[] = [
    { id: 'file', header: 'File', cell: (b) => b.file },
    {
      id: 'at',
      header: 'Made',
      cell: (b) => formatDateTime(b.created_at),
      sort: (b) => b.created_at,
    },
    {
      id: 'size',
      header: 'Size',
      cell: (b) => (b.size_bytes ? formatSize(b.size_bytes) : 'not known'),
    },
    {
      id: 'state',
      header: 'State',
      cell: (b) => (
        <Status
          kind={b.state === 'good' ? 'ok' : b.state === 'failed' ? 'failed' : 'running'}
          word={b.state === 'good' ? 'ok' : b.state}
        />
      ),
    },
  ];
  return (
    <Stack>
      <Section title="Backups">
        <Cluster>
          <Button
            onPress={() => void run.run()}
            busy={run.busy}
            busyLabel="Backing up"
            disabled={!admin}
            reason={session.whyNot('admin')}
          >
            Back up now
          </Button>
        </Cluster>
        {run.error ? (
          <Message tone="problem">Could not back up. {run.error}</Message>
        ) : null}
        <Load result={result} what="backups" reload={reload}>
          {(d) => (
            <Stack>
              {d.items.length === 0 ? (
                <Empty title="No backups yet. Back up now to make the first one." />
              ) : (
                <Table
                  caption="Backups"
                  columns={cols}
                  rows={d.items}
                  rowKey={(b) => b.id}
                />
              )}
              {d.last_drill ? (
                <Facts
                  items={[
                    {
                      name: 'Last restore drill',
                      value: JSON.stringify(d.last_drill).length ? 'ran' : '',
                    },
                  ]}
                />
              ) : null}
            </Stack>
          )}
        </Load>
      </Section>
      <Section title="Schedule">
        <div>
          <Stack>
            <TimePicker
              label="Backup time"
              value={shownTime}
              onChange={setTime}
              hourCycle="h23"
            />
            <NumberField
              label="Keep backups"
              value={shownKeep}
              changed={shownKeep !== savedKeep}
              unit=" days"
              min={1}
              max={3650}
              onChange={setKeep}
            />
            {save.error ? (
              <Message tone="problem">Could not save. {save.error}</Message>
            ) : null}
          </Stack>
        </div>
      </Section>
    </Stack>
  );
}

// GitHub App key (github-integration R5.7): the owner chooses the new private key file; the controller checks it
// with GitHub and keeps the old key when GitHub refuses it. The file is read here and sent once; the screen never
// shows it and never keeps it after the answer.
const MAX_KEY_FILE = 16384;

export function AppKeySettings() {
  const client = useClient();
  const session = useSession();
  const [file, setFile] = useState<File | null>(null);
  const [mistake, setMistake] = useState<string | undefined>(undefined);
  const [done, setDone] = useState(false);
  const [picked, setPicked] = useState(0);
  const replace = useCommand(async () => {
    setDone(false);
    if (!file || file.size === 0 || file.size > MAX_KEY_FILE) {
      setMistake('Choose the private key file from GitHub.');
      return;
    }
    setMistake(undefined);
    const pem = await file.text();
    await unwrap(client.PUT('/api/v1/app/private-key', { body: { pem } }));
    setFile(null);
    setPicked((n) => n + 1);
    setDone(true);
  });
  return (
    <Section title="GitHub App key">
      <div>
        <Stack>
          <FileField
            key={picked}
            label="New private key file"
            help="The file you download from the App's page at GitHub. crsp checks it with GitHub first and keeps the old key if GitHub refuses it."
            accept=".pem"
            error={mistake}
            disabled={!session.can('owner')}
            onChange={(f) => {
              setFile(f);
              setDone(false);
              setMistake(undefined);
            }}
          />
          {done ? <Message tone="done">The new key is in use.</Message> : null}
          {replace.error ? (
            <Message tone="problem">Could not replace the key. {replace.error}</Message>
          ) : null}
          <FormActions>
            <Button
              onPress={() => void replace.run()}
              busy={replace.busy}
              busyLabel="Replacing"
              disabled={!session.can('owner')}
              reason={session.whyNot('owner')}
            >
              Replace key
            </Button>
          </FormActions>
        </Stack>
      </div>
    </Section>
  );
}
