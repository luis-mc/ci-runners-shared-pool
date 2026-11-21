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
import { DoctorScreen } from './screens/doctor/Doctor';
import { jobsRoutes } from './screens/jobs/routes';
import { MOVED, MovedTo } from './screens/pool/redirects';
import { PoolLayout } from './screens/pool/PoolLayout';
import { PoolSizePage } from './screens/pool/PoolSizePage';
import { PoolSummary } from './screens/pool/PoolSummary';
import { NotFound } from './screens/Later';
import { ImageDetail } from './screens/images/ImageDetail';
import { RegistryBudgetsPage } from './screens/registry/RegistryBudgetsPage';
import { RegistryImagePage } from './screens/registry/RegistryImagePage';
import { RegistryMirrorPage } from './screens/registry/RegistryMirrorPage';
import { RegistryReplacePage } from './screens/registry/RegistryReplacePage';
import { ImagesAndCaches } from './screens/images/ImagesAndCaches';
import { MachineDetail, MachinesList } from './screens/machines/Machines';
import { AddMachinePage } from './screens/machines/AddMachine';
import { Overview } from './screens/overview/Overview';
import { RepositoriesList, RepositoryDetail } from './screens/repositories/Repositories';
import { TurnOnPoolPage } from './screens/repositories/TurnOnPool';
import { AddRunnersPage } from './screens/runners/AddRunners';
import { RunnersList } from './screens/runners/Runners';
import { RunnerPage } from './screens/runners/RunnerPage';
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
import { UpdateCompactPage, UpdateReviewPage } from './screens/settings/Updates';
import { SetupPage } from './screens/setup/Setup';

// Help is not a first screen: its pictures load with the page, so the first JavaScript stays within its budget (R6.12).
const HelpScreen = lazy(() =>
  import('./screens/help/Help').then((m) => ({ default: m.HelpScreen })),
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
          <Outlet />
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
        <Outlet />
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
              <Route path="/setup" element={<SetupPage />} />
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
