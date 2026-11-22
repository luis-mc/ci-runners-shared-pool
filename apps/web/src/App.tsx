// The route table of the console (web-console section 7). Every signed-in screen sits in the shell (R5.3).
import {
  ArrowNavigation,
  Button,
  ClockProvider,
  Failed,
  Loading,
  Page,
  PageHeader,
} from '@crsp/ui';
import type { ApiClient } from '@crsp/contract';
import { lazy, Suspense } from 'react';
import { Navigate, Outlet, Route, Routes, useLocation } from 'react-router-dom';
import { ApiProvider } from './api/api';
import { SignInPage, NoAccess } from './auth/SignIn';
import { SessionProvider, useSession } from './auth/session';
import { LiveProvider } from './live/LiveProvider';
import type { EventSourceFactory } from './live/store';
import { AppShell } from './shell/Shell';
import { FaultBoundary } from './screens/common';
import { jobsRoutes } from './screens/jobs/routes';
import { MOVED, MovedTo } from './screens/pool/redirects';
import { PoolLayout } from './screens/pool/PoolLayout';
import { PoolSizePage } from './screens/pool/PoolSizePage';
import { PoolSummary } from './screens/pool/PoolSummary';
import { NotFound } from './screens/Later';
import { ImagesAndCaches } from './screens/images/ImagesAndCaches';
import { MachineDetail, MachinesList } from './screens/machines/Machines';
import { Overview } from './screens/overview/Overview';
import { RepositoriesList, RepositoryDetail } from './screens/repositories/Repositories';
import { RunnersList } from './screens/runners/Runners';
import {
  AccessTab,
  AuditTab,
  ConsoleTab,
  UpdatesTab,
  DataTab,
  RepositoriesTab,
  OldSettingsAddress,
  SettingsLayout,
  ThemeApplier,
} from './screens/settings/Settings';

// Help is not a first screen: its pictures load with the page, so the first JavaScript stays within its budget (R6.12).
const HelpScreen = lazy(() =>
  import('./screens/help/Help').then((m) => ({ default: m.HelpScreen })),
);

// Screens that are not the first page load on their own, so the first JavaScript stays within its budget (R6.12).
const DoctorScreen = lazy(() =>
  import('./screens/doctor/Doctor').then((m) => ({ default: m.DoctorScreen })),
);
const RegistryBudgetsPage = lazy(() =>
  import('./screens/registry/RegistryBudgetsPage').then((m) => ({
    default: m.RegistryBudgetsPage,
  })),
);
const RegistryImagePage = lazy(() =>
  import('./screens/registry/RegistryImagePage').then((m) => ({
    default: m.RegistryImagePage,
  })),
);
const RegistryMirrorPage = lazy(() =>
  import('./screens/registry/RegistryMirrorPage').then((m) => ({
    default: m.RegistryMirrorPage,
  })),
);
const RegistryReplacePage = lazy(() =>
  import('./screens/registry/RegistryReplacePage').then((m) => ({
    default: m.RegistryReplacePage,
  })),
);
const ImageDetail = lazy(() =>
  import('./screens/images/ImageDetail').then((m) => ({ default: m.ImageDetail })),
);
const AddMachinePage = lazy(() =>
  import('./screens/machines/AddMachine').then((m) => ({ default: m.AddMachinePage })),
);
const TurnOnPoolPage = lazy(() =>
  import('./screens/repositories/TurnOnPool').then((m) => ({
    default: m.TurnOnPoolPage,
  })),
);
const AddRunnersPage = lazy(() =>
  import('./screens/runners/AddRunners').then((m) => ({ default: m.AddRunnersPage })),
);
const RunnerPage = lazy(() =>
  import('./screens/runners/RunnerPage').then((m) => ({ default: m.RunnerPage })),
);
const SetupPage = lazy(() =>
  import('./screens/setup/Setup').then((m) => ({ default: m.SetupPage })),
);
const UpdateCompactPage = lazy(() =>
  import('./screens/settings/Updates').then((m) => ({ default: m.UpdateCompactPage })),
);
const UpdateReviewPage = lazy(() =>
  import('./screens/settings/Updates').then((m) => ({ default: m.UpdateReviewPage })),
);

/** No valid session: every path but /sign-in and /setup goes to /sign-in and keeps the path (R5.8). */
function Gate({ factory }: { factory: EventSourceFactory | undefined }) {
  const session = useSession();
  const location = useLocation();
  const s = session.state;
  if (s.status === 'loading')
    return (
      <main>
        <Page>
          <PageHeader title="CI Runners Shared Pool" />
          <Loading what="Loading your session" />
        </Page>
      </main>
    );
  if (s.status === 'signed-out')
    return (
      <Navigate
        to="/sign-in"
        replace
        state={{ from: location.pathname + location.search }}
      />
    );
  if (s.status === 'no-access')
    return <NoAccess login={s.login} onSignOut={() => void session.signOut()} />;
  if (s.status === 'failed')
    return (
      <main>
        <Page>
          <PageHeader title="CI Runners Shared Pool" />
          <Failed
            what="Could not load your session."
            why={`${s.message} Try again.`}
            action={<Button onPress={session.refresh}>Try again</Button>}
          />
        </Page>
      </main>
    );
  return (
    <LiveProvider {...(factory ? { factory } : {})}>
      <ThemeApplier />
      <AppShell>
        <FaultBoundary key={location.pathname}>
          <Suspense fallback={<Loading what="Loading the page" />}>
            <Outlet />
          </Suspense>
        </FaultBoundary>
      </AppShell>
    </LiveProvider>
  );
}

/** The compact pages the tray opens in a small window: a session, the theme, and no shell (TRAY4). */
function CompactGate() {
  const session = useSession();
  const location = useLocation();
  const s = session.state;
  if (s.status === 'loading')
    return (
      <main>
        <Page frame="compact">
          <PageHeader title="CI Runners Shared Pool" />
          <Loading what="Loading your session" />
        </Page>
      </main>
    );
  if (s.status === 'signed-out')
    return (
      <Navigate
        to="/sign-in"
        replace
        state={{ from: location.pathname + location.search }}
      />
    );
  if (s.status === 'no-access')
    return <NoAccess login={s.login} onSignOut={() => void session.signOut()} />;
  if (s.status === 'failed')
    return (
      <main>
        <Page frame="compact">
          <PageHeader title="CI Runners Shared Pool" />
          <Failed
            what="Could not load your session."
            why={`${s.message} Try again.`}
            action={<Button onPress={session.refresh}>Try again</Button>}
          />
        </Page>
      </main>
    );
  return (
    <>
      <ThemeApplier />
      <FaultBoundary key={location.pathname}>
        <Suspense fallback={<Loading what="Loading the page" />}>
          <Outlet />
        </Suspense>
      </FaultBoundary>
    </>
  );
}

export function App({
  client,
  eventSource,
}: {
  client?: ApiClient;
  /** A stream factory for tests; the browser's EventSource is used when it is not given. */
  eventSource?: EventSourceFactory;
}) {
  return (
    <ApiProvider {...(client ? { client } : {})}>
      <ClockProvider>
        <SessionProvider>
          <ArrowNavigation>
            <Routes>
              <Route path="/sign-in" element={<SignInPage />} />
              <Route
                path="/setup"
                element={
                  <Suspense fallback={<Loading what="Loading the page" />}>
                    <SetupPage />
                  </Suspense>
                }
              />
              <Route element={<CompactGate />}>
                <Route path="update" element={<UpdateCompactPage />} />
              </Route>
              <Route element={<Gate factory={eventSource} />}>
                <Route index element={<Overview />} />
                <Route path="repositories" element={<RepositoriesList />} />
                <Route path="repositories/turn-on" element={<TurnOnPoolPage />} />
                <Route path="repositories/:owner/:name" element={<RepositoryDetail />} />
                <Route path="pool" element={<PoolLayout />}>
                  <Route index element={<PoolSummary />} />
                  <Route path="machines" element={<MachinesList />} />
                  <Route path="runners" element={<RunnersList />} />
                  <Route path="images" element={<ImagesAndCaches />} />
                  <Route path="size" element={<PoolSizePage />} />
                </Route>
                <Route path="pool/machines/add" element={<AddMachinePage />} />
                <Route path="pool/machines/:id" element={<MachineDetail />} />
                <Route path="pool/runners/add" element={<AddRunnersPage />} />
                <Route path="pool/runners/:id" element={<RunnerPage />} />
                <Route path="pool/images/:imageId" element={<ImageDetail />} />
                <Route
                  path="pool/images/registry/budgets"
                  element={<RegistryBudgetsPage />}
                />
                <Route
                  path="pool/images/registry/mirror"
                  element={<RegistryMirrorPage />}
                />
                <Route path="pool/images/registry/:id" element={<RegistryImagePage />} />
                <Route
                  path="pool/images/registry/:id/replace"
                  element={<RegistryReplacePage />}
                />
                {jobsRoutes}
                {MOVED.map(([from, to]) => (
                  <Route key={from} path={from} element={<MovedTo to={to} />} />
                ))}
                <Route path="doctor" element={<DoctorScreen />} />
                <Route
                  path="help"
                  element={
                    <Suspense
                      fallback={
                        <Page>
                          <PageHeader title="Help" />
                          <Loading what="Loading Help" />
                        </Page>
                      }
                    >
                      <HelpScreen />
                    </Suspense>
                  }
                />
                <Route path="settings/updates/review" element={<UpdateReviewPage />} />
                <Route path="settings" element={<SettingsLayout />}>
                  <Route index element={<Navigate to="access" replace />} />
                  <Route path="access" element={<AccessTab />} />
                  <Route path="repositories" element={<RepositoriesTab />} />
                  <Route path="console" element={<ConsoleTab />} />
                  <Route path="updates" element={<UpdatesTab />} />
                  <Route path="data" element={<DataTab />} />
                  <Route path="audit" element={<AuditTab />} />
                  <Route path="users" element={<OldSettingsAddress old="users" />} />
                  <Route path="app" element={<OldSettingsAddress old="app" />} />
                  <Route path="budgets" element={<OldSettingsAddress old="budgets" />} />
                  <Route
                    path="appearance"
                    element={<OldSettingsAddress old="appearance" />}
                  />
                  <Route
                    path="notifications"
                    element={<OldSettingsAddress old="notifications" />}
                  />
                  <Route
                    path="retention"
                    element={<OldSettingsAddress old="retention" />}
                  />
                  <Route path="backups" element={<OldSettingsAddress old="backups" />} />
                  <Route path="export" element={<OldSettingsAddress old="export" />} />
                </Route>
                <Route path="*" element={<NotFound />} />
              </Route>
            </Routes>
          </ArrowNavigation>
        </SessionProvider>
      </ClockProvider>
    </ApiProvider>
  );
}
