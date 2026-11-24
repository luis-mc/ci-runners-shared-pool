// Setup (LOOK56, LOOK69): where the pool is configured. One title and lead, then one row of tabs above each list
// page, like Pool; the list pages carry no h1 of their own. Add and detail pages sit
// under /setup too but show no tabs: they have a breadcrumb back. Nothing that runs is shown here (see Pool).
import { Page, PageHeader, Tabs } from '@crsp/ui';
import { Outlet, useLocation, useNavigate } from 'react-router-dom';

export const SETUP_TABS = [
  { id: 'summary', label: 'Summary', to: '/setup/summary' },
  { id: 'machines', label: 'Machines', to: '/setup/machines' },
  { id: 'runners', label: 'Runners', to: '/setup/runners' },
  { id: 'images', label: 'Images and caches', to: '/setup/images' },
  { id: 'size', label: 'Size and backends', to: '/setup/size' },
  { id: 'pool', label: 'Pool setup', to: '/setup/pool' },
] as const;

export function SetupLayout() {
  const navigate = useNavigate();
  const { pathname } = useLocation();
  const segment = pathname.split('/')[2] ?? '';
  const current = SETUP_TABS.find((t) => t.id === segment)?.id ?? 'summary';
  return (
    <>
      <Page>
        <PageHeader title="Setup" lead="Machines, runners and sizes. Change things here." />
        <Tabs
          label="Setup sections"
          tabs={SETUP_TABS}
          current={current}
          onChange={(id) =>
            void navigate(SETUP_TABS.find((t) => t.id === id)?.to ?? '/setup/summary')
          }
        />
      </Page>
      <Outlet />
    </>
  );
}
