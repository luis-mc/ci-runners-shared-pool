// Reads the Jobs API through the generated client: the lists, one run, one job, the groups, the chart data and
// the files. A list that holds running work asks again every few seconds (a refresh keeps the data on screen, UX-35).
import type { paths } from '@crsp/contract';
import { useEffect, useMemo, useState } from 'react';
import { unwrap, useClient, type Schemas } from '../../api/api';
import { useLoad, type Loaded } from '../../api/hooks';
import { useLiveState, useVersion } from '../../live/LiveProvider';
import { allRepositories } from '../repositories/plans';

export type JobsQuery = NonNullable<paths['/api/v1/jobs']['get']['parameters']['query']>;
export type RunsQuery = NonNullable<paths['/api/v1/runs']['get']['parameters']['query']>;
export type GroupBy = NonNullable<
  NonNullable<paths['/api/v1/jobs/groups']['get']['parameters']['query']>['by']
>;

/** Milliseconds between refreshes of a list that holds running jobs. */
export const POLL_MS = 5000;

/**
 * A counter that goes up every `ms` while the event stream is not live, so a screen asks again every 5 seconds
 * when the stream has dropped (R5.150). While the stream is live it stays still: the events reload the lists.
 */
export function useTick(ms = POLL_MS): number {
  const [n, setN] = useState(0);
  const status = useLiveState().status;
  useEffect(() => {
    if (status === 'live') return;
    const t = setInterval(() => setN((x) => x + 1), ms);
    return () => clearInterval(t);
  }, [ms, status]);
  return n;
}

type Hook<T> = { result: Loaded<T>; reload: () => void };

/** A page of jobs. `poll` asks again on every tick. */
export function useJobList(
  query: JobsQuery,
  poll = false,
  off = false,
): Hook<Schemas['JobPage']> {
  const client = useClient();
  const v = useVersion('jobs');
  const tick = useTick();
  return useLoad(
    () =>
      off
        ? new Promise<Schemas['JobPage']>(() => undefined)
        : unwrap(client.GET('/api/v1/jobs', { params: { query } })),
    [JSON.stringify(query), v, poll ? tick : 0, off],
  );
}

/** A page of runs (one row each, without their jobs). */
export function useRunList(
  query: RunsQuery,
  poll = false,
  off = false,
): Hook<Schemas['RunPage']> {
  const client = useClient();
  const v = useVersion('jobs');
  const tick = useTick();
  return useLoad(
    () =>
      off
        ? new Promise<Schemas['RunPage']>(() => undefined)
        : unwrap(client.GET('/api/v1/runs', { params: { query } })),
    [JSON.stringify(query), v, poll ? tick : 0, off],
  );
}

/** One run attempt with its jobs and graph. */
export function useRun(
  runId: number,
  attempt: number | undefined,
  poll = true,
): Hook<Schemas['RunItem']> {
  const client = useClient();
  const v = useVersion('jobs');
  const tick = useTick();
  return useLoad(
    () =>
      unwrap(
        client.GET('/api/v1/runs/{runId}', {
          params: { path: { runId }, query: attempt ? { attempt } : {} },
        }),
      ),
    [runId, attempt, v, poll ? tick : 0],
  );
}

/** One job with its steps, history and attempts. */
export function useJobDetail(jobId: number): Hook<Schemas['JobDetail']> {
  const client = useClient();
  const v = useVersion('jobs');
  const tick = useTick();
  return useLoad(
    () => unwrap(client.GET('/api/v1/jobs/{jobId}', { params: { path: { jobId } } })),
    [jobId, v, tick],
  );
}

export function useGroups(by: GroupBy): Hook<Schemas['JobGroupList']> {
  const client = useClient();
  const v = useVersion('jobs');
  const tick = useTick();
  return useLoad(
    () => unwrap(client.GET('/api/v1/jobs/groups', { params: { query: { by } } })),
    [by, v, tick],
  );
}

export function useStats(days: 7 | 14 | 30): Hook<Schemas['JobStats']> {
  const client = useClient();
  const v = useVersion('jobs');
  return useLoad(
    () => unwrap(client.GET('/api/v1/jobs/stats', { params: { query: { days } } })),
    [days, v],
  );
}

/** What the filter in the address names: a repository by its full name, a machine by its id, a runner by name. */
export interface Filter {
  repository?: string;
  machine?: string;
  runner?: string;
  runner_kind?: string;
  backend?: string;
  job_type?: string;
  branch?: string;
}

export const FILTER_KEYS = [
  'repository',
  'machine',
  'runner',
  'runner_kind',
  'backend',
  'job_type',
  'branch',
] as const;

/**
 * The filter of the address as a query of the API. The API takes a repository by its id and a machine by its id,
 * and the address names the repository, so the repository list is read once to find the id. `ready` is false until
 * a named repository is found; `unknown` is true when the repository is not on the list (nothing can match).
 */
export function useResolvedFilter(filter: Filter): {
  query: JobsQuery;
  ready: boolean;
  unknown: boolean;
  machineName: string | undefined;
} {
  const client = useClient();
  const needsRepo = Boolean(filter.repository);
  const repos = useLoad(
    () =>
      needsRepo
        ? allRepositories(client, {})
        : Promise.resolve({ items: [] as Schemas['Repository'][] }),
    [needsRepo],
  );
  const needsMachine = Boolean(filter.machine);
  const machines = useLoad(
    () =>
      needsMachine
        ? unwrap(client.GET('/api/v1/machines'))
        : Promise.resolve({ items: [] as Schemas['Machine'][] }),
    [needsMachine],
  );
  return useMemo(() => {
    const query: JobsQuery = {};
    if (filter.machine) query.machine_id = filter.machine;
    if (filter.runner) query.runner = filter.runner;
    if (filter.runner_kind)
      query.runner_kind = filter.runner_kind as NonNullable<JobsQuery['runner_kind']>;
    if (filter.backend) query.backend = filter.backend;
    if (filter.job_type) query.job_type = filter.job_type;
    if (filter.branch) query.branch = filter.branch;
    let ready = true;
    let unknown = false;
    if (filter.repository) {
      if (repos.result.state === 'ready') {
        const repo = repos.result.data.items.find(
          (r) => r.full_name === filter.repository,
        );
        if (repo) query.repository_id = repo.id;
        else unknown = true;
      } else ready = repos.result.state === 'failed';
    }
    const machineName =
      filter.machine && machines.result.state === 'ready'
        ? machines.result.data.items.find((m) => m.id === filter.machine)?.name
        : undefined;
    return { query, ready, unknown, machineName };
  }, [filter, repos.result, machines.result]);
}
