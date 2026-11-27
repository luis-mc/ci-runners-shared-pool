// Repository plans (github-integration R5.20): plan the change, show it, apply it, and read the stored result.
import type { ApiClient } from '@crsp/contract';
import { TIMING } from '../../timing';
import { unwrap, type Schemas } from '../../api/api';

export type Plan = Schemas['Plan'];
export type Repository = Schemas['Repository'];

/** Every repository of the installations: pages are read until the controller has no more. */
export async function allRepositories(
  client: ApiClient,
  query: { pool?: 'on' | 'off'; q?: string } = {},
): Promise<{ items: Repository[]; stale: Schemas['Staleness'] }> {
  const items: Repository[] = [];
  let cursor: string | undefined;
  let stale: Schemas['Staleness'] = { stale: false };
  for (let page = 0; page < 50; page += 1) {
    const res = await unwrap(
      client.GET('/api/v1/github/repositories', {
        params: { query: { ...query, limit: 100, ...(cursor ? { cursor } : {}) } },
      }),
    );
    items.push(...res.items);
    stale = res.stale;
    cursor = res.next_cursor;
    if (!cursor) break;
  }
  return { items, stale };
}

export function needsYes(plan: Plan): string[] {
  return plan.steps.filter((s) => s.needs_yes).map((s) => s.id);
}

export function planProgress(plan: Plan): {
  done: number;
  total: number;
  current?: string;
} {
  const total = plan.steps.length;
  const done = plan.steps.filter(
    (s) => s.state === 'done' || s.state === 'skipped',
  ).length;
  const current = plan.steps.find(
    (s) => s.state === 'running' || s.state === 'waiting',
  )?.text;
  return { done, total, ...(current ? { current } : {}) };
}

/** Plan and apply one change, then read the plan until it ends. */
export async function applyChanges(
  client: ApiClient,
  changes: { repository_id: number; pool: 'on' | 'off' }[],
  poll: () => Promise<void> = () => new Promise((r) => setTimeout(r, TIMING.planPollMs)),
): Promise<Plan> {
  const planned = await unwrap(
    client.POST('/api/v1/repository-plans', { body: { changes } }),
  );
  let plan = await unwrap(
    client.POST('/api/v1/repository-plans/{planId}/apply', {
      params: { path: { planId: planned.id } },
      body: { approved_steps: needsYes(planned) },
    }),
  );
  for (
    let i = 0;
    i < 600 && (plan.state === 'applying' || plan.state === 'planned');
    i += 1
  ) {
    await poll();
    plan = await unwrap(
      client.GET('/api/v1/repository-plans/{planId}', {
        params: { path: { planId: plan.id } },
      }),
    );
  }
  return plan;
}
