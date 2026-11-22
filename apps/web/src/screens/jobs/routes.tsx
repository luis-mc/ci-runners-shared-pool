// The routes of Pool > Jobs, the run and job pages, and the Analytics tabs (web-console R5.117, LOOK56). Old addresses
// (/jobs/..., /repositories, /shared-pool/...) are gone: there are no redirects.
import { Route } from 'react-router-dom';
import { ChartsTab } from './Charts';
import { ElsewhereTab, JobsTab } from './JobsHome';
import { JobPage } from './JobPage';
import { RunPage } from './RunPage';
import { AnalyticsLayout } from '../analytics/AnalyticsLayout';
import { Insights } from '../analytics/Insights';
import { PoolSection } from '../pool/PoolSection';
import { PoolLive } from '../pool/PoolLive';
import { PoolMachines } from '../pool/PoolMachines';
import { PoolOverview } from '../pool/PoolOverview';
import { QueuePlan } from '../pool/QueuePlan';
import { ActionWizard } from '../insights/ActionWizard';
import { DoNext } from '../insights/DoNext';
import { FindingPage } from '../insights/Finding';
import { Guardrails } from '../insights/Guardrails';
import { InsightsLayout } from '../insights/InsightsLayout';
import { Tests } from '../insights/Tests';

export const jobsRoutes = (
  <>
    <Route path="pool" element={<PoolSection />}>
      <Route index element={<PoolOverview />} />
      <Route path="live" element={<PoolLive />} />
      <Route path="queue" element={<QueuePlan />} />
      <Route path="jobs" element={<JobsTab />} />
      <Route path="machines" element={<PoolMachines />} />
    </Route>
    <Route path="pool/runs/:runId" element={<RunPage />} />
    <Route path="pool/jobs/:id" element={<JobPage />} />
    <Route path="analytics" element={<AnalyticsLayout />}>
      <Route element={<InsightsLayout />}>
        <Route index element={<DoNext />} />
        <Route path="tests" element={<Tests />} />
        <Route path="guardrails" element={<Guardrails />} />
      </Route>
      <Route path="insights" element={<Insights />} />
      <Route path="charts" element={<ChartsTab />} />
      <Route path="elsewhere" element={<ElsewhereTab />} />
    </Route>
    <Route path="analytics/finding/:id" element={<FindingPage />} />
    <Route path="analytics/finding/:id/change" element={<ActionWizard />} />
  </>
);
