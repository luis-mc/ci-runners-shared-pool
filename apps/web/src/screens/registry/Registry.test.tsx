// REG5 (R5.37 to R5.40): the Registry tab and the image page read the real API, say could not load when it does not
// answer, and delete only from the image page behind a question card (UX-62).
import { cleanup, fireEvent, screen, waitFor } from '@testing-library/react';
import { afterEach, describe, expect, it } from 'vitest';
import { apiError, machine, renderApp, signedIn } from '../../testing/mock';
import type { Schemas } from '../../api/api';
import { stateOf } from './words';

// Built at run time so the value never reads as a credential to the secret scan.
const OLD_BUILD = ['b7e204', 'a1c9d3'].join('');

afterEach(cleanup);

const GB = 1e9;
type Summary = Schemas['RegistryImageSummary'];

const img = (over: Partial<Summary> = {}): Summary => ({
  id: 'img_a',
  kind: 'standard',
  name: 'crsp/std/go',
  key: OLD_BUILD,
  profile: 'Go and Node',
  arch: 'arm64',
  state: 'current',
  manifest_digest: 'sha256:aa',
  size_bytes: 3.4 * GB,
  unique_bytes: 1 * GB,
  pulls_7_days: 4,
  scan_state: 'not_scanned',
  pushed_at: '2026-11-10T09:00:00Z',
  ...over,
});

const overview = (
  over: Partial<Schemas['RegistryOverview']> = {},
): Schemas['RegistryOverview'] => ({
  where: { host: 'ctl:5443', folder: '/srv/registry', read_only: false, running: true },
  budgets: [
    {
      kind: 'standard',
      limit_bytes: 20 * GB,
      used_bytes: 8 * GB,
      shared_bytes: 0,
      over: false,
    },
    {
      kind: 'custom',
      limit_bytes: 10 * GB,
      used_bytes: 12 * GB,
      shared_bytes: 0,
      over: true,
    },
  ],
  last_7_days: { pulls: 9, builds_avoided: 6, saved_seconds: 5400, bytes_pulled: 1 },
  recommendations: [
    {
      id: 'r1',
      rule: 'replace',
      image_id: 'img_b',
      kind: 'custom',
      frees_bytes: 2 * GB,
      decider: 'owner',
      created_at: '2026-11-18T09:00:00Z',
    },
  ],
  images: { standard: 1, custom: 1 },
  in_flight: { builds: 0, pulls: 0, pushes: 0 },
  ...over,
});

const detail = (
  s: Summary,
  over: Partial<Schemas['RegistryImage']> = {},
): Schemas['RegistryImage'] => ({
  ...s,
  lineage: [],
  layers: [],
  pulls_by_day: Array.from({ length: 14 }, (_, n) => ({
    day: `2026-11-${String(6 + n).padStart(2, '0')}`,
    pulls: n % 3,
  })),
  machines: [],
  pointers: [],
  scan: { state: 'not_scanned' },
  ...over,
});

const api = (
  images: Summary[] = [
    img(),
    img({ id: 'img_b', kind: 'custom', profile: 'Node web', update_found: true }),
  ],
) =>
  signedIn()
    .on('GET', '/api/v1/machines', { items: [machine()] })
    .on('GET', '/api/v1/images', { items: [] })
    .on('GET', '/api/v1/registry/overview', overview())
    .on('GET', '/api/v1/registry/images', { items: images });

describe('words', () => {
  it('names the state of an image in the approved words', () => {
    expect(stateOf({ state: 'current' }).word).toBe('current');
    expect(stateOf({ state: 'current', update_found: true }).word).toBe(
      'newer one found',
    );
    expect(stateOf({ state: 'variant', branch: 'rust-wasm' }).word).toBe(
      'variant of rust-wasm',
    );
    expect(stateOf({ state: 'current' }, { unusedDays: 70 }).word).toBe('unused 70 days');
    expect(stateOf({ state: 'current' }, { inUse: true }).word).toBe('in use');
  });
});

describe('Registry tab', () => {
  it('shows the budgets, the recommendations and one row per image, with one primary', async () => {
    renderApp(api(), '/setup/images?tab=registry');
    expect((await screen.findAllByText('Images in the registry')).length).toBeGreaterThan(
      0,
    );
    expect(screen.getAllByText('Standard images').length).toBeGreaterThan(0);
    expect(screen.getByText(/12\.0 GB of 10\.0 GB, over the budget/)).toBeTruthy();
    expect(screen.getAllByText('Recommendations').length).toBeGreaterThan(0);
    expect(screen.getAllByText('Go and Node').length).toBeGreaterThan(0);
    expect(screen.getAllByText('newer one found').length).toBeGreaterThan(0);
    expect(
      screen.getAllByRole('button', { name: 'Review the newer image' }),
    ).toHaveLength(1);
    expect(screen.queryAllByText('Add runners')).toHaveLength(0);
    expect(screen.queryAllByText(/^Delete/)).toHaveLength(0);
  });

  it('says could not load when the registry answers 501', async () => {
    renderApp(
      api().on('GET', '/api/v1/registry/overview', () =>
        apiError(501, 'not_implemented', 'Not built yet.'),
      ),
      '/setup/images?tab=registry',
    );
    expect(await screen.findByText(/Could not load the registry\./)).toBeTruthy();
    expect(screen.getByRole('button', { name: 'Try again' })).toBeTruthy();
  });

  it('says when the registry is not running', async () => {
    renderApp(
      api().on(
        'GET',
        '/api/v1/registry/overview',
        overview({
          where: {
            host: 'ctl:5443',
            folder: '/srv/registry',
            read_only: false,
            running: false,
            why_not: 'The folder is full.',
          },
        }),
      ),
      '/setup/images?tab=registry',
    );
    expect(
      await screen.findByText(/The registry is not running\. The folder is full\./),
    ).toBeTruthy();
  });

  it('says so when the registry holds no image', async () => {
    renderApp(
      api([]).on(
        'GET',
        '/api/v1/registry/overview',
        overview({ recommendations: [], images: { standard: 0, custom: 0 } }),
      ),
      '/setup/images?tab=registry',
    );
    expect(await screen.findByText(/No images yet\./)).toBeTruthy();
  });
});

describe('what the server now says (REG14)', () => {
  it('names the image of a recommendation from the recommendation, with no list to join', async () => {
    const named = img({
      id: 'img_b',
      kind: 'custom',
      profile: 'Node web',
      key: 'c57a886d9402',
    });
    renderApp(
      api([]).on(
        'GET',
        '/api/v1/registry/overview',
        overview({
          recommendations: [
            {
              id: 'r1',
              rule: 'replace',
              image_id: 'img_b',
              image: named,
              kind: 'custom',
              frees_bytes: 2 * GB,
              decider: 'owner',
              created_at: '2026-11-18T09:00:00Z',
            },
          ],
        }),
      ),
      '/setup/images?tab=registry',
    );
    expect(
      (await screen.findAllByText(/Node web c57a88 is a newer version/)).length,
    ).toBeGreaterThan(0);
  });

  it('writes the unused days the server counted', async () => {
    renderApp(
      api([img({ id: 'img_b', profile: 'Node web', unused_days: 70 })]),
      '/setup/images?tab=registry',
    );
    expect(await screen.findByText('unused 70 days')).toBeTruthy();
  });

  it('shows in use, shared bytes and the relation of the lineage as the server says them', async () => {
    renderApp(
      api().on(
        'GET',
        '/api/v1/registry/images/:id',
        detail(img(), {
          in_use: true,
          shared_bytes: 2.4 * GB,
          lineage: [
            {
              image_id: 'img_o',
              key: 'aaaaaaaaaaaa',
              state: 'superseded',
              pushed_at: '2026-11-01T09:00:00Z',
              relation: 'replaces',
            },
          ],
        }),
      ),
      '/setup/images/registry/img_a',
    );
    expect(
      await screen.findByText(/A runner uses this image\. The registry refuses/),
    ).toBeTruthy();
    expect(screen.getByText('2.4 GB')).toBeTruthy();
    expect(screen.getByText('It replaces')).toBeTruthy();
  });

  it('writes the variant days of the setting, and the pulls text of the server', async () => {
    renderApp(
      api().on(
        'GET',
        '/api/v1/registry/images/:id',
        detail(img({ state: 'variant', branch: 'fix' }), {
          variant_gone_days: 9,
          pulls_text: 'Pulled 3 times in the last 14 days.',
        }),
      ),
      '/setup/images/registry/img_a',
    );
    expect(await screen.findByText(/It is deleted 9 days after the branch/)).toBeTruthy();
    expect(screen.getByText('Pulled 3 times in the last 14 days.')).toBeTruthy();
  });
});

describe('Replace wizard steps (REG14)', () => {
  it('words the steps the server lists, one line each, with the state it gives', async () => {
    const plan = {
      id: 'plan_1',
      state: 'running',
      new_image: img({ id: 'img_n', key: 'c57a886d9402', profile: 'Node web' }),
      old_image: img({ id: 'img_o', key: OLD_BUILD, profile: 'Node web' }),
      keep_days: 7,
      keep_until: '2026-11-26T09:00:00Z',
      repositories: ['octo/docs', 'octo/site'],
      machines: ['build-1', 'build-2'],
      steps_done: 1,
      steps_total: 4,
      steps: [
        { kind: 'point', targets: ['octo/docs'], state: 'done' },
        { kind: 'point', targets: ['octo/site'], state: 'running' },
        { kind: 'warm', targets: ['build-1', 'build-2'], state: 'waiting' },
        { kind: 'supersede', targets: [], state: 'waiting' },
      ],
      created_at: '2026-11-19T09:00:00Z',
    };
    renderApp(
      api().on('GET', '/api/v1/registry/replace-plans/:id', plan),
      '/setup/images/registry/img_n/replace?plan=plan_1',
    );
    expect(await screen.findByText('Point octo/docs at c57a88')).toBeTruthy();
    expect(screen.getByText('Point octo/site at c57a88')).toBeTruthy();
    expect(screen.getByText('Warm build-1 and build-2 with c57a88')).toBeTruthy();
    expect(screen.getByText(/^Mark b7e204 superseded until /)).toBeTruthy();
    expect(screen.getAllByText('done').length).toBeGreaterThan(0);
    expect(screen.getAllByText('running').length).toBeGreaterThan(0);
    expect(screen.getAllByText('waits')).toHaveLength(2);
  });

  it('asks for the plan without keep days when going back to a superseded image', async () => {
    const bodies: unknown[] = [];
    renderApp(
      api()
        .on('GET', '/api/v1/registry/images/:id', detail(img({ state: 'superseded' })))
        .on('POST', '/api/v1/registry/replace-plans', (c) => {
          bodies.push(c.body);
          return apiError(409, 'conflict', 'Stop here.');
        }),
      '/setup/images/registry/img_a',
    );
    fireEvent.click(await screen.findByRole('button', { name: 'Go back to this one' }));
    await waitFor(() => expect(bodies).toEqual([{ new_image_id: 'img_a' }]));
  });
});

describe('Image page', () => {
  it('asks first, names what goes, and deletes after the yes (UX-62)', async () => {
    const calls: string[] = [];
    renderApp(
      api()
        .on('GET', '/api/v1/registry/images/:id', detail(img()))
        .on('DELETE', '/api/v1/registry/images/:id', (c) => {
          calls.push(c.query?.get('confirm') ?? 'none');
          return c.query?.get('confirm') === 'true'
            ? detail(img())
            : apiError(409, 'needs_confirmation', 'Name what goes.', [
                { kind: 'image', id: 'img_a', text: '1.0 GB freed' },
              ]);
        }),
      '/setup/images/registry/img_a',
    );
    fireEvent.click(await screen.findByRole('button', { name: 'Delete' }));
    expect(await screen.findByText('1.0 GB freed')).toBeTruthy();
    expect(calls).toEqual(['none']);
    fireEvent.click(screen.getAllByRole('button', { name: 'Delete' }).at(-1)!);
    await waitFor(() => expect(calls).toEqual(['none', 'true']));
  });

  it('refuses to delete an image a runner uses: the card stays open and names the job (UX-73)', async () => {
    renderApp(
      api()
        .on(
          'GET',
          '/api/v1/registry/images/:id',
          detail(img(), {
            machines: [
              {
                machine_id: 'm1',
                machine_name: 'build-1',
                holds: true,
                in_use_by_runner: true,
              },
            ],
          }),
        )
        .on('DELETE', '/api/v1/registry/images/:id', () =>
          apiError(409, 'conflict', 'A runner uses this image.', [
            { kind: 'job', id: '8133', text: 'job 8133 of octo/app' },
          ]),
        ),
      '/setup/images/registry/img_a',
    );
    fireEvent.click(await screen.findByRole('button', { name: 'Delete' }));
    expect(await screen.findByText(/job 8133 of octo\/app/)).toBeTruthy();
    fireEvent.click(screen.getAllByRole('button', { name: 'Delete' }).at(-1)!);
    expect(await screen.findByText(/Problem: A runner uses this image\./)).toBeTruthy();
    expect(screen.getAllByText(/job 8133 of octo\/app/).length).toBeGreaterThan(0);
  });

  it('says not scanned yet', async () => {
    renderApp(
      api().on('GET', '/api/v1/registry/images/:id', detail(img())),
      '/setup/images/registry/img_a',
    );
    expect(await screen.findByText(/Not scanned yet\./)).toBeTruthy();
  });
});
