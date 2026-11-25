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
import { later } from './screens/lazy';
import { jobsRoutes } from './screens/jobs/routes';
import { SetupLayout } from './screens/setup/SetupLayout';
import { SizePage } from './screens/setup/SizePage';
import { SetupSummary } from './screens/setup/SetupSummary';
import { NotFound } from './screens/Later';
import { ImagesAndCaches } from './screens/images/ImagesAndCaches';
import { MachineDetail, MachinesList } from './screens/machines/Machines';
import { Overview } from './screens/overview/Overview';
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

// Screens that are not the first page load on their own, so the first JavaScript stays within its budget (R6.12).
const DoctorScreen = later(
  () => import('./screens/doctor/Doctor').then((m) => m.DoctorScreen),
  { heading: 'Doctor' },
);
const RegistryBudgetsPage = later(
  () =>
    import('./screens/registry/RegistryBudgetsPage').then((m) => m.RegistryBudgetsPage),
  { heading: 'Registry budgets' },
);
const RegistryImagePage = later(
  () => import('./screens/registry/RegistryImagePage').then((m) => m.RegistryImagePage),
  { heading: 'Image' },
);
const RegistryMirrorPage = later(
  () => import('./screens/registry/RegistryMirrorPage').then((m) => m.RegistryMirrorPage),
  { heading: 'Mirror of upstream images' },
);
const RegistryReplacePage = later(
  () =>
    import('./screens/registry/RegistryReplacePage').then((m) => m.RegistryReplacePage),
  { heading: 'Replace an image' },
);
const ImageDetail = later(
  () => import('./screens/images/ImageDetail').then((m) => m.ImageDetail),
  { heading: 'Image' },
);
const RepositoriesList = later(() =>
  import('./screens/repositories/Repositories').then((m) => m.RepositoriesList),
);
const RepositoryDetail = later(
  () => import('./screens/repositories/Repositories').then((m) => m.RepositoryDetail),
  { heading: 'Repository' },
);
const AddMachinePage = later(
  () => import('./screens/machines/AddMachine').then((m) => m.AddMachinePage),
  { heading: 'Add a machine' },
);
const GetReadyPage = later(
  () => import('./screens/dependencies/GetReady').then((m) => m.GetReadyPage),
  { heading: 'Get this machine ready' },
);
const MachineRestartPage = later(
  () => import('./screens/machines/MachineRestart').then((m) => m.MachineRestartPage),
  { heading: 'Restart the service' },
);
const ConfigApplyPage = later(
  () => import('./screens/settings/ConfigAsCode').then((m) => m.ConfigApplyPage),
  { heading: 'Apply a config file' },
);
const TurnOnPoolPage = later(
  () => import('./screens/repositories/TurnOnPool').then((m) => m.TurnOnPoolPage),
  { heading: 'Turn on the pool' },
);
const AddRunnersPage = later(
  () => import('./screens/runners/AddRunners').then((m) => m.AddRunnersPage),
  { heading: 'Add runners' },
);
const RunnerPage = later(
  () => import('./screens/runners/RunnerPage').then((m) => m.RunnerPage),
  { heading: 'Runner' },
);
const SetupEntry = lazy(() =>
  import('./screens/setup/Setup').then((m) => ({ default: m.SetupEntry })),
);
const UpdateCompactPage = later(
  () => import('./screens/settings/Updates').then((m) => m.UpdateCompactPage),
  { heading: 'Update', compact: true },
);
const UpdateReviewPage = later(
  () => import('./screens/settings/Updates').then((m) => m.UpdateReviewPage),
  {
    heading: (params) =>
      params.get('kind') === 'restart' ? 'Restart the service' : 'Update',
  },
);

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
          <Suspense fallback={<Loading what="Loading the page" />}>
            <Suspense fallback={<Loading what="Loading the page" />}>
              <Outlet />
            </Suspense>
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
                    <SetupEntry />
                  </Suspense>
                }
              />
              <Route element={<CompactGate />}>
                <Route path="update" element={<UpdateCompactPage />} />
              </Route>
              <Route element={<Gate factory={eventSource} />}>
                <Route index element={<Overview />} />
                <Route path="setup" element={<SetupLayout />}>
                  <Route path="summary" element={<SetupSummary />} />
                  <Route path="machines" element={<MachinesList />} />
                  <Route path="runners" element={<RunnersList />} />
                  <Route path="images" element={<ImagesAndCaches />} />
                  <Route path="size" element={<SizePage />} />
                  <Route path="pool" element={<RepositoriesList />} />
                </Route>
                <Route path="config/apply" element={<ConfigApplyPage />} />
                <Route path="setup/pool/turn-on" element={<TurnOnPoolPage />} />
                <Route path="setup/pool/:owner/:name" element={<RepositoryDetail />} />
                <Route path="setup/machines/add" element={<AddMachinePage />} />
                <Route path="setup/machines/:id" element={<MachineDetail />} />
                <Route
                  path="setup/machines/:id/restart"
                  element={<MachineRestartPage />}
                />
                <Route path="setup/machines/:id/ready" element={<GetReadyPage />} />
                <Route path="setup/runners/add" element={<AddRunnersPage />} />
                <Route path="setup/runners/:id" element={<RunnerPage />} />
                <Route path="setup/images/:imageId" element={<ImageDetail />} />
                <Route
                  path="setup/images/registry/budgets"
                  element={<RegistryBudgetsPage />}
                />
                <Route
                  path="setup/images/registry/mirror"
                  element={<RegistryMirrorPage />}
                />
                <Route path="setup/images/registry/:id" element={<RegistryImagePage />} />
                <Route
                  path="setup/images/registry/:id/replace"
                  element={<RegistryReplacePage />}
                />
                {jobsRoutes}
                <Route path="doctor" element={<DoctorScreen />} />
                <Route
                  path="help/*"
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
