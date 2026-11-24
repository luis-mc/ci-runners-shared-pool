// Analytics > Insights (LOOK56, UX-75): four numbers with their change and trend, where the time goes, the busiest
// repositories, the failed jobs, then the waits and the runner timeline. Cards only show and link (UX-52).
import {
  BarList,
  Card,
  CardGrid,
  Sparkline,
  Stack,
  StatGrid,
  StatTile,
  Text,
  TextLink,
  formatDuration,
  type BarItem,
} from '@crsp/ui';
import { unwrap, useClient } from '../../api/api';
import { useLoad } from '../../api/hooks';
import { RunnerTimelinePane, WaitsPane } from './InsightsPanes';
import { Load } from '../common';
import { useGroups, useJobList, useStats } from '../jobs/api';
import { groupLink } from '../jobs/Groups';
import { passChange, passRate, periods, waitChange } from '../jobs/overviewStats';

function usePoolBusy(): { used: number; slots: number } | undefined {
  const client = useClient();
  const r = useLoad(() => unwrap(client.GET('/api/v1/machines')), []);
  if (r.result.state !== 'ready') return undefined;
  const on = r.result.data.items.filter((m) => m.online);
  const slots = on.reduce((n, m) => n + (m.settings?.job_slots ?? 0), 0);
  return slots > 0
    ? { used: on.reduce((n, m) => n + m.slots_used, 0), slots }
    : undefined;
}

export function Insights() {
  const stats = useStats(14);
  const waiting = useJobList({ state: 'queued', limit: 1 });
  const pool = usePoolBusy();
  const groups = useGroups('repository');
  return (
    <Stack gap="section">
      <Load result={stats.result} what="the overview" reload={stats.reload}>
        {(s) => {
          const { last, prev } = periods(s.days);
          const wait = waitChange(last, prev);
          const pass = passChange(last, prev);
          const rate = passRate(last);
          const queued =
            waiting.result.state === 'ready' ? waiting.result.data.total : undefined;
          return (
            <>
              <StatGrid>
                <StatTile
                  label="Waiting now"
                  {...(queued === undefined ? {} : { value: String(queued) })}
                  unit={queued === 1 ? 'job' : 'jobs'}
                  to="/pool/jobs?state=queued"
                  linkName="Open the jobs that wait now"
                />
                <StatTile
                  label="Wait slow end (p90)"
                  {...(last.waitP90 === undefined
                    ? {}
                    : { value: formatDuration(Math.round(last.waitP90)) })}
                  {...(wait ? { change: wait.change, changeText: wait.text } : {})}
                  trend={last.waitSeries}
                  trendUnit="seconds"
                  to="/analytics/charts"
                  linkName="Open the charts of the waits"
                />
                <StatTile
                  label="Jobs that passed"
                  {...(rate === undefined ? {} : { value: `${Math.round(rate)}%` })}
                  {...(pass ? { change: pass.change, changeText: pass.text } : {})}
                  trend={last.passSeries}
                  trendUnit="percent"
                  to="/pool/jobs"
                  linkName="Open the finished jobs"
                />
                <StatTile
                  label="Pool busy"
                  {...(pool
                    ? {
                        value: `${Math.round((pool.used / pool.slots) * 100)}%`,
                        changeText: `${pool.used} of ${pool.slots} slots in use`,
                      }
                    : {})}
                  to="/setup/machines"
                  linkName="Open the machines"
                />
              </StatGrid>
              <CardGrid>
                <Card title="Where the time goes" to="/analytics/charts">
                  <Text>
                    {last.waitP90 === undefined
                      ? 'No finished jobs in the last 7 days.'
                      : `The slow end of the wait was ${formatDuration(Math.round(last.waitP90))} over the last 7 days.`}
                  </Text>
                  <Sparkline
                    label="Slow-end wait by day"
                    values={last.waitSeries}
                    unit="seconds"
                  />
                </Card>
                <Card title="Busiest repositories" to="/pool/jobs?group=repository">
                  <Load
                    result={groups.result}
                    what="the repositories"
                    reload={groups.reload}
                  >
                    {(g) => (
                      <BarList
                        label="Busiest repositories"
                        items={g.items
                          .map((r): BarItem => ({
                            key: r.name,
                            label: r.name,
                            value: r.done + r.running + r.queued,
                            text: `${r.done + r.running + r.queued} jobs`,
                            linkName: `Open the jobs of ${r.name}`,
                            to: groupLink('repository', r),
                          }))
                          .sort((a, b) => b.value - a.value)
                          .slice(0, 5)}
                      />
                    )}
                  </Load>
                </Card>
                <Card title="Failed jobs" to="/pool/jobs?state=done">
                  <Text>
                    {last.jobs === 0
                      ? 'No finished jobs in the last 7 days.'
                      : `${last.failed} of ${last.jobs} jobs failed in the last 7 days.`}
                  </Text>
                </Card>
              </CardGrid>
              <Text tone="muted">
                Each number is compared with the 7 days before.{' '}
                <TextLink to="/analytics/charts">Open the charts</TextLink> for longer
                ranges.
              </Text>
              <WaitsPane />
              <RunnerTimelinePane />
            </>
          );
        }}
      </Load>
    </Stack>
  );
}
