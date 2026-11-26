// TEMPORARY (CRSP338): demo data and a stateful fake controller so the six Pool setup options run in the real
// console without a controller. Loaded only when the dev server starts with VITE_CRSP338_DEMO=1; never part of the build.
import { createApiClient } from '@crsp/contract';
import { machine, repository, runner, signedIn } from '../../../testing/mock';
import type { Schemas } from '../../../api/api';

type Repo = Schemas['Repository'];
type Plan = Schemas['Plan'];
type Step = Schemas['PlanStep'];
interface Stored {
  plan: Plan;
  changes: { id: number; pool: 'on' | 'off' }[];
  at?: number;
}

const LINUX = 'CRSP_RUNS_ON_LINUX';
function make(owner: string, name: string, over: Partial<Repo> = {}): Repo {
  return repository({
    id: Math.abs([...`${owner}/${name}`].reduce((h, c) => (h * 31 + c.charCodeAt(0)) | 0, 7)),
    full_name: `${owner}/${name}`,
    owner,
    name,
    ...over,
  });
}
const okVars = (): Repo['variables'] => [
  { name: LINUX, status: 'ok', value: '["self-hosted","crsp","linux"]' },
];

export function demoRepos(): Repo[] {
  return [
    make('luis-mc', 'moobable', { pool_on: true, variables: okVars() }),
    make('luis-mc', 'api-gateway', { pool_on: true, variables: okVars() }),
    make('luis-mc', 'web-shop', {
      pool_on: true,
      variables: [{ name: LINUX, status: 'missing' }],
    }),
    make('luis-mc', 'docs-site'),
    make('luis-mc', 'mobile-app'),
    make('luis-mc', 'infra'),
    make('luis-mc', 'blog', { visibility: 'public' }),
    make('luis-mc', 'dotfiles', { visibility: 'public' }),
    make('acme-labs', 'builder', { pool_on: true, variables: okVars() }),
    make('acme-labs', 'tools'),
    make('acme-labs', 'payments', { user_is_github_admin: false }),
  ];
}

function stepsFor(r: Repo, to: 'on' | 'off', repair: boolean): Step[] {
  const t = r.full_name;
  const mk = (i: number, action: Step['action'], text: string, needs = false): Step => ({
    id: `${t}#${i}`,
    action,
    text,
    target: t,
    needs_yes: needs,
    state: 'planned',
  });
  if (repair)
    return [mk(1, 'write_variable', `Write variable ${LINUX} of ${t} = ["self-hosted","crsp","linux"]`)];
  if (to === 'on')
    return [
      mk(1, 'write_variable', `Write variable ${LINUX} of ${t} = ["self-hosted","crsp","linux"]`),
      mk(2, 'write_variable', `Write variable CRSP_RUNS_ON_WINDOWS of ${t} = ["self-hosted","crsp","windows"]`),
      mk(3, 'write_variable', `Write variable CRSP_RUNS_ON_MACOS of ${t} = ["self-hosted","crsp","macos"]`),
      mk(4, 'add_runner', `Add runner group crsp-pool to ${t}`),
      mk(5, 'pool_on', `Switch the pool on for ${t}`),
    ];
  return [
    mk(1, 'pool_off', `Switch the pool off for ${t}`),
    mk(2, 'delete_variable', `Delete variable ${LINUX} of ${t}`),
    mk(3, 'remove_old_runner', `Remove the runners of ${t}`),
  ];
}

export function demoApi(search: string): { client: ReturnType<typeof createApiClient> } {
  const q = new URLSearchParams(search);
  const stepMs = Number(q.get('stepms') ?? 450);
  const hold = q.has('hold') ? Number(q.get('hold')) : undefined;
  const repos = demoRepos();
  const plans = new Map<string, Stored>();
  let n = 0;

  const view = (p: Stored): Plan => {
    if (p.at === undefined) return p.plan;
    const total = p.plan.steps.length;
    const raw = Math.floor((Date.now() - p.at) / stepMs);
    const done = Math.min(total, hold === undefined ? raw : Math.min(raw, hold));
    if (done >= total) {
      for (const c of p.changes) {
        const r = repos.find((x) => x.id === c.id);
        if (r) {
          r.pool_on = c.pool === 'on';
          r.variables = c.pool === 'on' ? okVars() : [];
        }
      }
      return {
        ...p.plan,
        state: 'done',
        steps: p.plan.steps.map((s) => ({ ...s, state: 'done' as const })),
      };
    }
    const steps = p.plan.steps.map((s, i) => ({
      ...s,
      state: (i < done ? 'done' : i === done ? 'running' : 'planned') as Step['state'],
    }));
    return { ...p.plan, state: 'applying', steps };
  };

  const api = signedIn()
    .on('GET', '/api/v1/machines', { items: [machine(), machine({ id: 'm2', name: 'build-2' })] })
    .on('GET', '/api/v1/jobs/groups', { items: [] })
    .on('GET', '/api/v1/github/org-pools', { items: [] })
    .on('GET', '/api/v1/github/repositories', (call) => {
      const pool = call.query.get('pool');
      const items = repos.filter((r) => !pool || (pool === 'on') === r.pool_on);
      return { items, stale: { stale: false } };
    })
    .on('POST', '/api/v1/repository-plans', (call) => {
      const body = call.body as { changes: { repository_id: number; pool: 'on' | 'off' }[] };
      const id = `plan-${(n += 1)}`;
      const steps = body.changes.flatMap((c) => {
        const r = repos.find((x) => x.id === c.repository_id);
        const repair = Boolean(r?.pool_on && c.pool === 'on');
        return r ? stepsFor(r, c.pool, repair) : [];
      });
      const plan: Plan = {
        id,
        area: 'repositories',
        state: 'planned',
        created_at: new Date().toISOString(),
        created_by: 'octo',
        steps,
      };
      plans.set(id, {
        plan,
        changes: body.changes.map((c) => ({ id: c.repository_id, pool: c.pool })),
      });
      return plan;
    })
    .on('POST', '/api/v1/repository-plans/:planId/apply', (_call, params) => {
      const p = plans.get(params['planId'] ?? '');
      if (!p) return new Response(null, { status: 404 });
      p.at = Date.now();
      return view(p);
    })
    .on('POST', '/api/v1/repository-plans/:planId/stop', () => undefined)
    .on('GET', '/api/v1/repository-plans/:planId', (_call, params) => {
      const p = plans.get(params['planId'] ?? '');
      return p ? view(p) : new Response(null, { status: 404 });
    });
  const client = createApiClient('http://demo.local', api.fetch as typeof fetch);
  return { client };
}
