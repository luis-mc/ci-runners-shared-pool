// Analytics (LOOK56): what could be better. Tabs: Suggestions, Insights, Charts, Run elsewhere. Something that is wrong
// now is in Doctor, not here.
import { Page, PageHeader, Tabs } from '@crsp/ui';
import { Outlet, useLocation, useNavigate } from 'react-router-dom';

export const ANALYTICS_TABS = [
  { id: 'suggestions', label: 'Suggestions', to: '/analytics', lead: 'Changes that would make the pool faster. Ranked by the time they save.' },
  { id: 'insights', label: 'Insights', to: '/analytics/insights', lead: 'Where the time goes, from past runs.' },
  { id: 'charts', label: 'Charts', to: '/analytics/charts', lead: 'Numbers over time.' },
  { id: 'elsewhere', label: 'Run elsewhere', to: '/analytics/elsewhere', lead: 'Jobs that another runner ran, not your pool.' },
] as const;

export function AnalyticsLayout() {
  const navigate = useNavigate();
  const { pathname } = useLocation();
  const segment = pathname.split('/')[2] ?? '';
  const tab =
    ANALYTICS_TABS.find((t) => t.id === segment) ?? ANALYTICS_TABS[0];
  return (
    <Page>
      <PageHeader title="Analytics" lead={tab.lead} />
      <Tabs
        label="Analytics sections"
        tabs={ANALYTICS_TABS}
        current={tab.id}
        onChange={(id) =>
          void navigate(ANALYTICS_TABS.find((t) => t.id === id)?.to ?? '/analytics')
        }
      />
      <Outlet />
    </Page>
  );
}
