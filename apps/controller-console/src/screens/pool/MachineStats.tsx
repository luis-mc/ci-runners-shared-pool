// The parts the machine card and the machine page share (LOOK58c): the data hooks, the health badges, the metric
// facts and the job stats summary. They only show; every word comes from machineHealth.ts.
import {
  Cluster,
  Facts,
  Segmented,
  Stack,
  Status,
  Text,
  TextLink,
  formatAgo,
  useNow,
} from '@crsp/ui';
import { unwrap, useClient, type Schemas } from '../../api/api';
import { useLoad } from '../../api/hooks';
import { useRefreshTick } from '../../api/machines';
import {
  FAILURE_CLASS_WORDS,
  WINDOWS,
  WINDOW_WORDS,
  badgesOf,
  metricsOf,
  statLines,
  type StatsSet,
  type StatsWindow,
} from './machineHealth';

/** Health is reported every minute: ask again on the page timer, keeping the last answer on screen (UX-35). */
const HEALTH_REFRESH_MS = 30_000;

export function useMachineHealth(machineId: string) {
  const client = useClient();
  const tick = useRefreshTick(HEALTH_REFRESH_MS);
  return useLoad(
    () =>
      unwrap(
        client.GET('/api/v1/machines/{machineId}/health', {
          params: { path: { machineId } },
        }),
      ),
    [client, machineId, tick],
  );
}

export function useMachineStats(machineId: string, window: StatsWindow) {
  const client = useClient();
  const tick = useRefreshTick(HEALTH_REFRESH_MS);
  return useLoad(
    () =>
      unwrap(
        client.GET('/api/v1/machines/{machineId}/job-stats', {
          params: { path: { machineId }, query: { window } },
        }),
      ),
    [client, machineId, window, tick],
  );
}

/** The 24 h / 7 days switch, the shared segmented control with no "All" option. */
export function WindowSwitch({
  value,
  onChange,
}: {
  value: StatsWindow;
  onChange: (w: StatsWindow) => void;
}) {
  return (
    <Segmented
      label="Job stats window"
      options={WINDOWS}
      value={value}
      allLabel={null}
      onChange={(w) => {
        if (w) onChange(w);
      }}
    />
  );
}

export function HealthBadges({
  health,
}: {
  health: Schemas['MachineHealth'] | undefined;
}) {
  return (
    <Cluster gap="tight">
      {badgesOf(health).map((b) => (
        <Status key={b.word} kind={b.kind} word={b.word} />
      ))}
    </Cluster>
  );
}

/** The key numbers as a facts list; an unknown value says so and, when the API gave one, why. */
export function MetricFacts({
  health,
  os,
  only,
}: {
  health: Schemas['MachineHealth'] | undefined;
  os: string;
  only?: readonly string[] | undefined;
}) {
  const metrics = metricsOf(health, os).filter((m) => !only || only.includes(m.id));
  return (
    <Facts
      items={metrics.map((m) => ({
        name: m.label,
        value: m.why ? `${m.value}: ${m.why}` : m.value,
      }))}
    />
  );
}

/** The job stats of a machine or a runner: counts, times, busy, the last failure and the top failing jobs. */
export function StatsSummary({
  stats,
  machineId,
  window,
}: {
  stats: StatsSet;
  machineId: string;
  window: StatsWindow;
}) {
  const now = useNow();
  const last = stats.last_failure;
  return (
    <Stack gap="tight">
      <Facts items={statLines(stats).map((l) => ({ name: l.label, value: l.value }))} />
      <Facts
        items={[
          {
            name: 'Last failure',
            value: last ? (
              <>
                <TextLink to={`/pool/jobs/${last.job_id}`}>{last.job}</TextLink> in{' '}
                {last.repository}, {FAILURE_CLASS_WORDS[last.class]}{' '}
                {formatAgo(last.at, now)}
              </>
            ) : (
              `none in ${WINDOW_WORDS[window]}`
            ),
          },
        ]}
      />
      {stats.top_failing.length === 0 ? (
        <Text tone="muted">No job failed in {WINDOW_WORDS[window]}.</Text>
      ) : (
        <Facts
          items={stats.top_failing.map((f) => ({
            name: `${f.repository} ${f.job}`,
            value: (
              <TextLink
                to={`/pool/jobs?repository=${encodeURIComponent(f.repository)}&machine=${encodeURIComponent(machineId)}&q=${encodeURIComponent(f.job)}`}
              >
                {f.failures} {f.failures === 1 ? 'failure' : 'failures'}
              </TextLink>
            ),
          }))}
        />
      )}
    </Stack>
  );
}
