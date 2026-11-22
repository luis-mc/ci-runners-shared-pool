// The shell of every signed-in screen (web-console 4.0, R5.3): header, top menu, banner slot, activity strip,
// command palette, shortcut list and the session-ended dialog.
import {
  ActivityStrip,
  BannerSlot,
  Dialog,
  Frame,
  Header,
  Message,
  ArrowNavigation,
  SearchList,
  KeyHints,
  LinkButton,
  ShortcutTable,
  Status,
  TextLink,
  TopMenu,
  TopMenuButton,
  UserMenu,
  formatAgo,
  useNow,
  type ActivityJob,
  type NavItem,
} from '@crsp/ui';
import { useEffect, useLayoutEffect, useMemo, useState, type ReactNode } from 'react';
import { Outlet, useLocation, useNavigate } from 'react-router-dom';
import { unwrap, useClient, type Schemas } from '../api/api';
import { signInUrl, useSession } from '../auth/session';
import { useMachines, useRefreshTick } from '../api/machines';
import { useUpdateBanner } from './UpdateBannerHost';
import { useLiveState, useVersion } from '../live/LiveProvider';
import { wipingLabel } from '../screens/images/labels';
import { allRepositories } from '../screens/repositories/plans';
import { RunnerProblemBanner, runnerProblems } from './RunnerProblems';
import { KEY_HINTS, SHORTCUTS, handleKey, isMac } from './shortcuts';

/** The first first-run step that is still to do, in the words of the setup page; none when all are done. */
export function firstOpenStep(setup: Schemas['SetupState']): string | undefined {
  if (setup.steps.connect_github === 'todo') return 'connect GitHub';
  if (setup.steps.first_machine === 'todo') return 'add the first machine';
  if (setup.steps.first_repository === 'todo') return 'turn on the first repository';
  return undefined;
}

/** The first repository counts as done when any repository has the pool on, whatever the setup answer says. */
export function withFirstRepository(
  setup: Schemas['SetupState'],
  anyPoolOn: boolean,
): Schemas['SetupState'] {
  if (!anyPoolOn || setup.steps.first_repository === 'done') return setup;
  return { ...setup, steps: { ...setup.steps, first_repository: 'done' } };
}

/** True on a phone-width screen (below 601 px, the design system's phone break). No matchMedia: wide. */
export function useNarrow(): boolean {
  const query = '(max-width: 600px)';
  const [narrow, setNarrow] = useState(
    () => typeof window.matchMedia === 'function' && window.matchMedia(query).matches,
  );
  useEffect(() => {
    if (typeof window.matchMedia !== 'function') return;
    const m = window.matchMedia(query);
    const on = () => setNarrow(m.matches);
    on();
    m.addEventListener('change', on);
    return () => m.removeEventListener('change', on);
  }, []);
  return narrow;
}

/** The areas: a row on a wide screen; on a phone one Menu button that opens the list (UX-15, UX-16). */
function AreaMenu() {
  const narrow = useNarrow();
  const { pathname } = useLocation();
  const [open, setOpen] = useState(false);
  useEffect(() => setOpen(false), [pathname]);
  if (!narrow) return <TopMenu items={AREAS} />;
  const here = AREAS.find((a) => (a.end ? pathname === a.to : pathname.startsWith(a.to)));
  return (
    <>
      <TopMenuButton
        open={open}
        here={here?.label ?? 'Areas'}
        onToggle={() => setOpen((o) => !o)}
      />
      {open ? <TopMenu items={AREAS} /> : null}
    </>
  );
}

export const AREAS: readonly NavItem[] = [
  { to: '/', label: 'Overview', end: true },
  { to: '/pool', label: 'Shared Pool' },
  { to: '/repositories', label: 'Repositories' },
  { to: '/jobs', label: 'Jobs' },
  { to: '/doctor', label: 'Doctor' },
  { to: '/settings', label: 'Settings' },
  { to: '/help', label: 'Help' },
];

interface Command {
  id: string;
  label: string;
  hint?: string;
  to: string;
}

/** Pool status as a word, a glyph and a colour (UX-58). */
function PoolStatus() {
  // The same list the Machines page reads (UX-19): one source of truth.
  const { result, reconnecting } = useMachines();
  const machines =
    result.state === 'loading' ? undefined : (result.data?.items ?? undefined);
  // A restart is its own state, not "no machines" (CORE-3, UX-6, UX-29).
  if (reconnecting) return <Status kind="waiting" word="reconnecting" />;
  if (!machines) return <Status kind="waiting" word="reading" />;
  if (machines.length === 0) return <Status kind="waiting" word="no machines" />;
  const online = machines.filter((m) => m.online && m.state === 'active').length;
  if (online === 0) return <Status kind="failed" word="machines offline" />;
  return (
    <Status
      kind="ok"
      word={`${online} of ${machines.length} ${machines.length === 1 ? 'machine' : 'machines'} online`}
    />
  );
}

function LiveState() {
  const live = useLiveState();
  const now = useNow();
  if (live.status === 'live') return <Status kind="ok" word="live updates" />;
  if (live.status === 'connecting') return <Status kind="waiting" word="connecting" />;
  return (
    <Status
      kind="waiting"
      word={`updates paused${live.lastEventAt ? `, data from ${formatAgo(live.lastEventAt, now)}` : ''}`}
    />
  );
}

function Palette({ onClose }: { onClose: () => void }) {
  const session = useSession();
  const client = useClient();
  const navigate = useNavigate();
  const live = useLiveState();
  const [found, setFound] = useState<Command[]>([]);

  useEffect(() => {
    let on = true;
    void Promise.allSettled([
      unwrap(client.GET('/api/v1/runners')),
      unwrap(
        client.GET('/api/v1/github/repositories', { params: { query: { limit: 100 } } }),
      ),
    ]).then(([runners, repos]) => {
      if (!on) return;
      const out: Command[] = [];
      if (runners.status === 'fulfilled')
        for (const r of runners.value.items)
          out.push({
            id: `runner:${r.id}`,
            label: r.name,
            hint: 'runner',
            to: `/pool/runners`,
          });
      if (repos.status === 'fulfilled')
        for (const r of repos.value.items)
          out.push({
            id: `repo:${r.id}`,
            label: r.full_name,
            hint: 'repository',
            to: `/repositories/${r.owner}/${r.name}`,
          });
      setFound(out);
    });
    return () => {
      on = false;
    };
  }, [client]);

  const all = useMemo(() => {
    const items: Command[] = AREAS.map((a) => ({
      id: `area:${a.to}`,
      label: a.label,
      hint: 'area',
      to: a.to,
    }));
    for (const m of live.overview?.machines ?? [])
      items.push({
        id: `machine:${m.id}`,
        label: m.name,
        hint: 'machine',
        to: `/pool/machines/${m.id}`,
      });
    items.push(...found);
    if (session.can('admin')) {
      items.push(
        {
          id: 'cmd:add-machine',
          label: 'Add a machine',
          hint: 'command',
          to: '/pool/machines/add',
        },
        {
          id: 'cmd:add-runners',
          label: 'Add runners',
          hint: 'command',
          to: '/pool/runners/add',
        },
        {
          id: 'cmd:pool-on',
          label: 'Turn on the pool',
          hint: 'command',
          to: '/repositories',
        },
      );
    }
    if (session.can('owner'))
      items.push({
        id: 'cmd:users',
        label: 'Add a user',
        hint: 'command',
        to: '/settings/access#users',
      });
    return items;
  }, [live.overview, found, session]);

  const go = (id: string) => {
    const hit = all.find((i) => i.id === id);
    if (hit) {
      onClose();
      void navigate(hit.to);
    }
  };

  // Focus starts in the search box: the user opened the palette to type (UX-35, UX-57).
  return (
    <Dialog title="Commands" onClose={onClose} initialFocus="first">
      <SearchList
        items={all}
        onPick={go}
        label="Search areas, items and commands"
        empty="Nothing matches. Clear the search to see every area."
        autoFocus
      />
    </Dialog>
  );
}

export function AppShell({ children }: { children?: ReactNode }) {
  const session = useSession();
  const live = useLiveState();
  const navigate = useNavigate();
  const location = useLocation();
  const [palette, setPalette] = useState(false);
  const [help, setHelp] = useState(false);
  const [signingOut, setSigningOut] = useState(false);
  const [signingInAgain, setSigningInAgain] = useState(false);
  const [setup, setSetup] = useState<Schemas['SetupState'] | undefined>(undefined);
  const client = useClient();
  // The machines list is the one the header reads (UX-19); a runner service that did not start shows on it.
  const machineList = useMachines();
  // The first-run steps change when a machine joins or a repository's pool turns on.
  const machinesVersion = useVersion('machines');
  const repositoriesVersion = useVersion('repositories');
  // The banner is read again on a timer and when the tab is shown, so it clears itself after the last step.
  const refresh = useRefreshTick();
  // The Overview says in one line while a repository runs on GitHub-hosted runners (github-integration R5.44).
  const [hosted, setHosted] = useState(0);
  const onOverview = location.pathname === '/';
  useEffect(() => {
    if (!onOverview) return;
    let on = true;
    const read = () =>
      allRepositories(client, { pool: 'on' })
        .then(
          (r) =>
            on && setHosted(r.items.filter((x) => x.routing?.where === 'hosted').length),
        )
        .catch(() => undefined);
    void read();
    const t = setInterval(() => void read(), 30_000);
    return () => {
      on = false;
      clearInterval(t);
    };
  }, [client, onOverview]);
  useEffect(() => {
    let on = true;
    unwrap(client.GET('/api/v1/setup'))
      .then(async (s) => {
        if (s.steps.first_repository !== 'todo') return s;
        // Ask the repository list too: a repository with the pool on is the first repository.
        const on = await unwrap(
          client.GET('/api/v1/github/repositories', {
            params: { query: { pool: 'on', limit: 1 } },
          }),
        )
          .then((r) => r.items.length > 0)
          .catch(() => false);
        return withFirstRepository(s, on);
      })
      .then((s) => on && setSetup(s))
      .catch(() => undefined);
    return () => {
      on = false;
    };
  }, [client, machinesVersion, repositoriesVersion, location.pathname, refresh]);

  // Layout effect: the listener is on before the first paint, so a key pressed
  // the moment the page shows is never lost to a deferred passive effect.
  useLayoutEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      handleKey(e, {
        openPalette: () => setPalette(true),
        openHelp: () => setHelp(true),
      });
    };
    document.addEventListener('keydown', onKey);
    return () => document.removeEventListener('keydown', onKey);
  }, []);

  if (session.state.status !== 'ready') return null;
  const user = session.state.session.user;

  const jobs: ActivityJob[] = Object.values(live.progress)
    .map((ev): ActivityJob | undefined => {
      if (ev.change && ev.change.state !== 'done' && ev.change.state !== 'cancelled') {
        const waiting = ev.change.waiting_for.length;
        return {
          id: `change:${ev.change.id}`,
          label: `${ev.change.kind === 'drain' ? 'Stopping new jobs on' : 'Working on'} a machine, waiting for ${waiting} ${waiting === 1 ? 'job' : 'jobs'}`,
        };
      }
      if (ev.cache && ev.cache.state === 'wiping') {
        return { id: `cache:${ev.cache.id}`, label: wipingLabel(ev.cache) };
      }
      if (ev.plan && (ev.plan.state === 'applying' || ev.plan.state === 'planned')) {
        const done = ev.plan.steps.filter((s) => s.state === 'done').length;
        return {
          id: `plan:${ev.plan.id}`,
          label: `Applying a plan, ${done} of ${ev.plan.steps.length}`,
          value: done,
          max: ev.plan.steps.length,
        };
      }
      return undefined;
    })
    .filter((j): j is ActivityJob => j !== undefined);

  const banners: ReactNode[] = [];
  const updateBanner = useUpdateBanner();
  if (updateBanner) banners.push(updateBanner);
  const open = setup ? firstOpenStep(setup) : undefined;
  // No first-run banner while the service restarts: its machines are not back yet, they are not missing.
  if (open && !machineList.reconnecting)
    banners.push(
      <Message
        key="first-run"
        tone="needs-you"
        action={<LinkButton to="/setup">Open the setup page</LinkButton>}
      >
        {`First run is not finished. Next: ${open}.`}
      </Message>,
    );
  if (onOverview && hosted > 0)
    banners.push(
      <Message
        key="hosted-fallback"
        tone="needs-you"
        action={<TextLink to="/repositories">Open Repositories</TextLink>}
      >
        {hosted === 1
          ? '1 repository runs on GitHub-hosted runners because no crsp machine is up.'
          : `${hosted} repositories run on GitHub-hosted runners because no crsp machine is up.`}
      </Message>,
    );
  const machineItems =
    machineList.result.state === 'loading' ? [] : (machineList.result.data?.items ?? []);
  for (const { machine, problem } of runnerProblems(machineItems))
    banners.push(
      <RunnerProblemBanner
        key={`runner-${machine.id}-${problem.service}`}
        machine={machine}
        problem={problem}
        onStarted={machineList.reload}
      />,
    );
  if (live.status === 'paused')
    banners.push(
      <Message key="live" tone="note" dismissible={false}>
        Live updates paused. The data on screen is the last that arrived. The console
        reconnects by itself.
      </Message>,
    );

  return (
    <>
      <ArrowNavigation>
        <Frame
          header={
            <Header
              product="CI Runners Shared Pool"
              status={<PoolStatus />}
              live={<LiveState />}
              paletteHint={isMac() ? 'Cmd+K' : 'Ctrl+K'}
              onPalette={() => setPalette(true)}
              menu={
                <UserMenu
                  login={user.login}
                  role={user.role}
                  busy={signingOut}
                  githubSignIn={
                    session.state.session.github_signin.state === 'active'
                      ? {
                          state: 'active',
                          expiresAt: session.state.session.github_signin.expires_at,
                        }
                      : { state: 'ended' }
                  }
                  signInAgainBusy={signingInAgain}
                  onSignInAgain={() => {
                    // Sign out first so GitHub is really asked, then come back to this page.
                    setSigningInAgain(true);
                    const back = location.pathname + location.search;
                    void session.signOut().finally(() => {
                      window.location.assign(signInUrl(back));
                    });
                  }}
                  onSignOut={() => {
                    setSigningOut(true);
                    void session.signOut().finally(() => {
                      setSigningOut(false);
                      void navigate('/sign-in');
                    });
                  }}
                />
              }
            />
          }
          menu={<AreaMenu />}
          banner={<BannerSlot>{banners.length ? banners : undefined}</BannerSlot>}
          strip={<ActivityStrip jobs={jobs} />}
          hints={<KeyHints items={KEY_HINTS} />}
        >
          {children ?? <Outlet />}
        </Frame>
      </ArrowNavigation>
      {palette ? <Palette onClose={() => setPalette(false)} /> : null}
      {help ? (
        <Dialog title="Shortcuts" onClose={() => setHelp(false)}>
          <ShortcutTable items={SHORTCUTS} />
        </Dialog>
      ) : null}
      {session.ended ? (
        <Dialog
          title="Session ended"
          onClose={() => undefined}
          cancelLabel="Stay on this page"
          primary={{
            label: 'Sign in with GitHub',
            onPress: () => {
              window.location.assign(signInUrl(location.pathname + location.search));
            },
          }}
        >
          <p className="ds-text">
            Your session ended. Sign in again to go on. This page and what you typed stay
            as they are.
          </p>
          <TextLink to="/sign-in">Open the sign-in page</TextLink>
        </Dialog>
      ) : null}
    </>
  );
}
