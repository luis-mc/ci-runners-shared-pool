// The routes of Pool > Jobs, the run and job pages, and the Analytics tabs (web-console R5.117, LOOK56). Old addresses
// (/jobs/..., /repositories, /shared-pool/...) are gone: there are no redirects.
import { ClockProvider } from '@crsp/ui';
import { Outlet, Route } from 'react-router-dom';
import { later } from '../lazy';
import { JobsTab } from './JobsHome';
import { ElsewhereTab } from './Elsewhere';
import { JobPage } from './JobPage';
import { RunPage } from './RunPage';
import { AnalyticsLayout } from '../analytics/AnalyticsLayout';
import { Compare } from '../analytics/Compare';
import { Savings } from '../analytics/Savings';
import { PoolSection } from '../pool/PoolSection';
import { DoNext } from '../insights/DoNext';
import { InsightsLayout } from '../insights/InsightsLayout';

const PoolOverview = later(() =>
  import('../pool/PoolOverview').then((m) => m.PoolOverview),
);
const PoolLive = later(() => import('../pool/PoolLive').then((m) => m.PoolLive));
const PoolMachines = later(() =>
  import('../pool/PoolMachines').then((m) => m.PoolMachines),
);
const PoolMachineDetail = later(
  () => import('../pool/PoolMachineDetail').then((m) => m.PoolMachineDetail),
  { heading: 'Machine' },
);
const QueuePlan = later(() => import('../pool/QueuePlan').then((m) => m.QueuePlan));
const Insights = later(() => import('../analytics/Insights').then((m) => m.Insights));
const ChartsTab = later(() => import('./Charts').then((m) => m.ChartsTab));
const Tests = later(() => import('../insights/Tests').then((m) => m.Tests));
const Guardrails = later(() =>
  import('../insights/Guardrails').then((m) => m.Guardrails),
);
const FindingPage = later(
  () => import('../insights/Finding').then((m) => m.FindingPage),
  { heading: 'Finding' },
);
const ActionWizard = later(
  () => import('../insights/ActionWizard').then((m) => m.ActionWizard),
  { heading: 'Review the change' },
);

/** The run and job pages count their timers every second (R5.150). */
function EverySecond() {
  return (
    <ClockProvider tickMs={1000}>
      <Outlet />
    </ClockProvider>
  );
}

export const jobsRoutes = (
  <>
    <Route path="pool" element={<PoolSection />}>
      <Route index element={<PoolOverview />} />
      <Route path="live" element={<PoolLive />} />
      <Route path="queue" element={<QueuePlan />} />
      <Route path="jobs" element={<JobsTab />} />
      <Route path="machines" element={<PoolMachines />} />
    </Route>
    <Route element={<EverySecond />}>
      <Route path="pool/runs/:runId" element={<RunPage />} />
      <Route path="pool/jobs/:id" element={<JobPage />} />
      <Route path="pool/machines/:machineId" element={<PoolMachineDetail />} />
    </Route>
    <Route path="analytics" element={<AnalyticsLayout />}>
      <Route element={<InsightsLayout />}>
        <Route index element={<DoNext />} />
        <Route path="tests" element={<Tests />} />
        <Route path="guardrails" element={<Guardrails />} />
      </Route>
      <Route path="insights" element={<Insights />} />
      <Route path="savings" element={<Savings />} />
      <Route path="compare" element={<Compare />} />
      <Route path="charts" element={<ChartsTab />} />
      <Route path="elsewhere" element={<ElsewhereTab />} />
    </Route>
    <Route path="analytics/finding/:id" element={<FindingPage />} />
    <Route path="analytics/finding/:id/change" element={<ActionWizard />} />
  </>
);
