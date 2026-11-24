// Reads of the insights API for the screens, through the generated client. The sample data in mock.ts only feeds the test server and the
// hooks that are not wired yet (a finding, its plan, sizing, tests, guardrails, savings). Every hook has the shape of the Jobs hooks.
import { useLoad, type Loaded } from '../../api/hooks';
import { unwrap, useClient, type Schemas } from '../../api/api';
import * as mock from './mock';

type Hook<T> = { result: Loaded<T>; reload: () => void };
type S = Schemas;

export function useFindings(
  repositoryId: number | undefined,
): Hook<S['InsightFindingList']> {
  const client = useClient();
  return useLoad(
    () =>
      unwrap(
        client.GET('/api/v1/insights/findings', {
          params: {
            query: repositoryId === undefined ? {} : { repository_id: repositoryId },
          },
        }),
      ),
    [client, repositoryId],
  );
}

export const useFinding = (id: number): Hook<S['InsightFinding']> =>
  useLoad(() => {
    const f = mock.MOCK_FINDINGS.find((x) => x.id === id);
    return f
      ? Promise.resolve(f)
      : Promise.reject(new Error('This finding is not there any more.'));
  }, [id]);

export function useInsightSummary(): Hook<S['InsightSummary']> {
  const client = useClient();
  return useLoad(() => unwrap(client.GET('/api/v1/insights/summary')), [client]);
}

export function useDigests(): Hook<S['InsightDigestList']> {
  const client = useClient();
  return useLoad(() => unwrap(client.GET('/api/v1/insights/digests')), [client]);
}

export function useWaits(
  days: 7 | 14 | 30,
  group: 'repository' | 'job_key' | 'day',
): Hook<S['WaitSplit']> {
  const client = useClient();
  return useLoad(
    () =>
      unwrap(
        client.GET('/api/v1/insights/waits', { params: { query: { days, group } } }),
      ),
    [client, days, group],
  );
}

export function useTimeline(hours: number): Hook<S['PoolTimeline']> {
  const client = useClient();
  return useLoad(() => {
    const to = new Date();
    const from = new Date(to.getTime() - hours * 3_600_000);
    return unwrap(
      client.GET('/api/v1/insights/timeline', {
        params: { query: { from: from.toISOString(), to: to.toISOString() } },
      }),
    );
  }, [client, hours]);
}

export const usePoolSizing = (days: 7 | 14 | 30): Hook<S['PoolSizing']> =>
  useLoad(() => Promise.resolve(mock.mockSizing(days)), [days]);

export const useTests = (view: string): Hook<S['TestInsightList']> =>
  useLoad(() => Promise.resolve(mock.mockTests(view)), [view]);

export const useGuardrails = (): Hook<S['GuardrailStatusList']> =>
  useLoad(() => Promise.resolve(mock.mockGuardrails()), []);

export const useRunSavings = (runId: number): Hook<S['RunSavings']> =>
  useLoad(() => Promise.resolve(mock.mockSavings()), [runId]);

/** CPU and memory of one job, with its measured peaks (OPS66): the real client, not the mock. */
export function useJobResources(jobId: number): Hook<S['JobResources']> {
  const client = useClient();
  return useLoad(
    () =>
      unwrap(
        client.GET('/api/v1/jobs/{jobId}/resources', { params: { path: { jobId } } }),
      ),
    [jobId],
  );
}

export const useWorkflowChecks = (repositoryId: number): Hook<S['WorkflowCheckList']> =>
  useLoad(() => Promise.resolve(mock.mockChecks()), [repositoryId]);

/** The plan of a finding's setup change (nothing changes until it is started). */
export const planFor = (findingId: number): Promise<S['InsightPlan']> =>
  Promise.resolve(mock.mockPlan(findingId));

/** Starts the plan; the mocked answer is the plan done, as the real one ends. */
export const startPlan = (plan: S['InsightPlan']): Promise<S['InsightPlan']> =>
  Promise.resolve({
    ...plan,
    state: 'done',
    results: plan.changes.map((c) => ({
      target: c.target,
      field: c.field,
      state: 'done' as const,
    })),
    finished_at: new Date().toISOString(),
  });

/**
 * Hides a finding for 7 days (owner decision D17); it comes back if it is still true. Mocked like the reads until
 * CIX21 joins: the join swaps the body for `POST /api/v1/insights/findings/{id}/dismiss` (admin).
 */
export const dismissFinding = (findingId: number): Promise<S['InsightFinding']> => {
  const f = mock.MOCK_FINDINGS.find((x) => x.id === findingId);
  if (!f) return Promise.reject(new Error('This finding is not there any more.'));
  return Promise.resolve({
    ...f,
    dismissed_until: new Date(Date.now() + 7 * 24 * 3600 * 1000).toISOString(),
  });
};
