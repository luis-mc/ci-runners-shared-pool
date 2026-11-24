// Pool (LOOK56): what runs, what waits and why. One row of tabs (Overview, Live, Queue Analysis, Jobs, Machines) and
// the four counts above Overview and Live (LOOK70). Nothing is configured here (Setup does that). Timers of
// the section count every second in the browser (R5.150); when the stream drops the screens poll and the shell banner
// says "Live updates paused" until it is back.
import { ClockProvider, Page, PageHeader, StatGrid, StatTile, Tabs } from '@crsp/ui';
import { Outlet, useLocation, useNavigate } from 'react-router-dom';
import { useLiveState } from '../../live/LiveProvider';
import { poolCounts } from './poolWords';

export const POOL_TABS = [
  { id: 'overview', label: 'Overview', to: '/pool' },
  { id: 'live', label: 'Live', to: '/pool/live' },
  { id: 'queue', label: 'Queue Analysis', to: '/pool/queue' },
  { id: 'jobs', label: 'Jobs', to: '/pool/jobs' },
  { id: 'machines', label: 'Machines', to: '/pool/machines' },
] as const;

const WITH_COUNTS = new Set(['overview', 'live']);

/** The four counts of the pool: Running, Starting, Queued, No runner fits. */
export function PoolCountTiles({
  counts,
}: {
  counts: ReturnType<typeof poolCounts> | undefined;
}) {
  return (
    <StatGrid>
      <StatTile
        label="Running"
        {...(counts ? { value: String(counts.running) } : {})}
        to="/pool/jobs?state=running"
        linkName="Open the running jobs"
      />
      <StatTile
        label="Starting"
        {...(counts ? { value: String(counts.starting) } : {})}
        to="/pool/jobs?state=running"
        linkName="Open the jobs that are starting"
      />
      <StatTile
        label="Queued"
        {...(counts ? { value: String(counts.queued) } : {})}
        to="/pool/queue"
        linkName="Open the queue plan"
      />
      <StatTile
        label="No runner fits"
        {...(counts ? { value: String(counts.noRunner) } : {})}
        to="/pool/queue"
        linkName="Open the jobs that no runner fits"
      />
    </StatGrid>
  );
}

export function PoolSection() {
  const navigate = useNavigate();
  const { pathname } = useLocation();
  const live = useLiveState();
  const segment = pathname.split('/')[2] ?? '';
  const current =
    POOL_TABS.find((t) => t.id === (segment || 'overview'))?.id ?? 'overview';
  const counts = live.overview ? poolCounts(live.overview) : undefined;
  return (
    <ClockProvider tickMs={1000}>
      <Page>
        <PageHeader
          title="Pool"
          lead="What runs, what waits and why. Nothing to configure here."
        />
        <Tabs
          label="Pool sections"
          tabs={POOL_TABS}
          current={current}
          onChange={(id) =>
            void navigate(POOL_TABS.find((t) => t.id === id)?.to ?? '/pool')
          }
        />
        {WITH_COUNTS.has(current) ? <PoolCountTiles counts={counts} /> : null}
        <Outlet />
      </Page>
    </ClockProvider>
  );
}
