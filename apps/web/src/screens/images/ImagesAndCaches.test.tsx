// R5.25: the images and caches screen. Images are empty in milestone 1; caches show size against cap, and a
// wipe asks first (R5.30, UX-62).
import { act, cleanup, fireEvent, screen, waitFor, within } from '@testing-library/react';
import { afterEach, describe, expect, it } from 'vitest';
import {
  FakeEventSource,
  apiError,
  machine,
  renderApp,
  signedIn,
  viewer,
} from '../../testing/mock';
import { TIMING } from '../../timing';
import type { Schemas } from '../../api/api';

afterEach(cleanup);

const cache = (over: Partial<Schemas['Cache']> = {}): Schemas['Cache'] => ({
  id: 'c1',
  kind: 'tool_cache',
  machine_id: 'm1',
  runner_id: 'r1',
  runner_name: 'crsp-linux-1',
  size_bytes: 3_200_000_000,
  cap_bytes: 10_000_000_000,
  entry_count: 42,
  state: 'ready',
  ...over,
});

const base = () =>
  signedIn()
    .on('GET', '/api/v1/machines', { items: [machine()] })
    .on('GET', '/api/v1/images', { items: [] });

const image = (over: Partial<Schemas['Image']> = {}): Schemas['Image'] => ({
  id: 'i1',
  machine_id: 'm1',
  os: 'linux',
  base: 'ubuntu-24.04',
  base_tools: [
    { name: 'Ubuntu', version: '24.04' },
    { name: 'git' },
    { name: 'curl' },
    { name: 'python3' },
    { name: 'jq' },
    { name: 'zip' },
    { name: 'node', version: '26' },
    { name: 'pnpm' },
  ],
  extra_packages: [{ repository: 'octo/app', packages: ['libssl-dev', 'ffmpeg'] }],
  layers: [
    {
      id: 'build-tools',
      name: 'Build tools',
      enabled: true,
      size_bytes: 900_000_000,
      adds: [{ name: 'gcc' }],
      purpose: 'your jobs compile C',
      needed_by: { count: 0, repositories: [], failing: 0 },
    },
    {
      id: 'browsers',
      name: 'Browsers',
      enabled: false,
      size_bytes: 1_500_000_000,
      adds: [{ name: 'chromium' }, { name: 'firefox' }],
      purpose: 'your tests drive a browser',
      needed_by: { count: 2, repositories: ['octo/app', 'octo/web'], failing: 1 },
    },
  ],
  size_bytes: 4_000_000_000,
  built_at: '2026-11-14T09:00:00Z',
  newest: true,
  state: 'ready',
  ...over,
});

const notBuilt = (): Schemas['Image'] => {
  const { built_at: _built, ...rest } = image();
  return { ...rest, state: 'not_built', size_bytes: 0 };
};

describe('Images', () => {
  it('shows loading, then the empty state in the words of the design system', async () => {
    renderApp(base(), '/setup/images');
    expect(await screen.findByText('Loading images')).toBeTruthy();
    expect(
      await screen.findByText(
        'No images yet. A machine shows one here once it can run container jobs.',
      ),
    ).toBeTruthy();
    expect(screen.getByRole('region', { name: 'Images' })).toBeTruthy();
    expect(screen.getAllByRole('button', { name: 'Add runners' }).length).toBe(1);
    expect(
      screen.getByRole('tab', { name: 'Images' }).getAttribute('aria-selected'),
    ).toBe('true');
  });

  it('says could not load, why and what next', async () => {
    renderApp(
      base().on('GET', '/api/v1/images', () =>
        apiError(503, 'unavailable', 'The database is busy.'),
      ),
      '/setup/images',
    );
    expect(await screen.findByText(/Could not load images\./)).toBeTruthy();
    expect(screen.getByText(/The database is busy\. Try again\./)).toBeTruthy();
    expect(screen.getByRole('button', { name: 'Try again' })).toBeTruthy();
  });

  const needy = (): Schemas['Image'] => {
    const i = image();
    return {
      ...i,
      layers: [
        ...i.layers.map((l) => ({
          ...l,
          needed_by: { count: 0, repositories: [], failing: 0 },
        })),
        {
          id: 'llvm',
          name: 'LLVM',
          enabled: false,
          size_bytes: 0,
          adds: [{ name: 'clang-cl' }],
          purpose: 'for Windows builds from Linux',
          needed_by: { count: 1, repositories: ['octo/app'], failing: 0 },
        },
      ],
    };
  };
  const detailApi = (img: Schemas['Image']) =>
    base()
      .on('GET', '/api/v1/images', { items: [img] })
      .on('GET', '/api/v1/images/:id', img)
      .on('PUT', '/api/v1/images/:id/layers', img);

  it('lists one row per image with one status line and no tables', async () => {
    const ok = {
      ...image(),
      id: 'i2',
      machine_id: 'm2',
      layers: image().layers.map((l) => ({
        ...l,
        needed_by: { count: 0, repositories: [], failing: 0 },
      })),
    };
    const nb = { ...notBuilt(), id: 'i3', machine_id: 'm3', layers: ok.layers };
    renderApp(
      base()
        .on('GET', '/api/v1/machines', {
          items: [
            machine(),
            machine({ id: 'm2', name: 'BuildBox' }),
            machine({ id: 'm3', name: 'OldLaptop' }),
          ],
        })
        .on('GET', '/api/v1/images', { items: [needy(), ok, nb] }),
      '/setup/images',
    );
    expect(await screen.findByText(/Each machine has one Linux image\./)).toBeTruthy();
    expect(screen.getByText('Missing LLVM · needed by octo/app')).toBeTruthy();
    expect(screen.getAllByText('1 extra layer ›').length).toBe(3);
    expect(screen.getByText('Has what its repositories need')).toBeTruthy();
    expect(screen.getByText('Not built yet')).toBeTruthy();
    expect(screen.queryByRole('table')).toBeNull();
    expect(screen.getByText('build-1, Linux')).toBeTruthy();
  });

  it('opens the detail from a row', async () => {
    renderApp(detailApi(image()), '/setup/images');
    fireEvent.click(await screen.findByRole('button', { name: /build-1, Linux/ }));
    expect(
      await screen.findByRole('heading', { level: 1, name: 'build-1' }),
    ).toBeTruthy();
    expect(screen.getByRole('link', { name: '‹ Images' })).toBeTruthy();
    expect(screen.getByText(/used by octo\/app, octo\/web/)).toBeTruthy();
  });

  it('ticks a layer, saves and rebuilds', async () => {
    const api = detailApi(image());
    renderApp(api, '/setup/images/i1');
    const save = await screen.findByRole<HTMLButtonElement>('button', {
      name: 'Save and rebuild',
    });
    const cancel = screen.getByRole<HTMLButtonElement>('button', { name: 'Cancel' });
    expect(save.disabled).toBe(true);
    expect(cancel.disabled).toBe(true);
    expect(screen.queryByText(/Rebuilds after running jobs finish/)).toBeNull();
    fireEvent.click(screen.getByRole('checkbox', { name: /Browsers/ }));
    expect(save.disabled).toBe(false);
    expect(screen.getByText(/Rebuilds after running jobs finish\./)).toBeTruthy();
    fireEvent.click(save);
    await waitFor(() => expect(api.count('PUT', '/api/v1/images/i1/layers')).toBe(1));
    expect(
      (api.calls.find((c) => c.method === 'PUT')?.body as { enabled_layers: string[] })
        .enabled_layers,
    ).toEqual(['build-tools', 'browsers']);
  });

  it('Cancel drops the change', async () => {
    renderApp(detailApi(image()), '/setup/images/i1');
    const box = await screen.findByRole<HTMLInputElement>('checkbox', {
      name: /Browsers/,
    });
    fireEvent.click(box);
    expect(box.checked).toBe(true);
    fireEvent.click(screen.getByRole('button', { name: 'Cancel' }));
    expect(box.checked).toBe(false);
  });

  it('says what is missing and Add it ticks it without saving', async () => {
    const api = detailApi(needy());
    renderApp(api, '/setup/images/i1');
    expect(await screen.findByText('octo/app needs LLVM')).toBeTruthy();
    const llvm = screen.getByRole<HTMLInputElement>('checkbox', { name: /LLVM/ });
    expect(llvm.checked).toBe(false);
    expect(screen.getByText('needed by 1').getAttribute('data-tone')).toBe('needs-you');
    fireEvent.click(screen.getByRole('button', { name: 'Add it' }));
    expect(llvm.checked).toBe(true);
    expect(screen.queryByText('octo/app needs LLVM')).toBeNull();
    expect(api.count('PUT', '/api/v1/images/i1/layers')).toBe(0);
    expect(
      screen.getByRole<HTMLButtonElement>('button', { name: 'Save and rebuild' })
        .disabled,
    ).toBe(false);
  });

  it('shows a not built image with Save, and saves the layers', async () => {
    const api = detailApi(notBuilt());
    renderApp(api, '/setup/images/i1');
    expect(
      await screen.findByText(
        'Not built yet. Choose layers now; it is built when its first runner starts.',
      ),
    ).toBeTruthy();
    expect(screen.queryByRole('button', { name: 'Save and rebuild' })).toBeNull();
    fireEvent.click(screen.getByRole('checkbox', { name: /Browsers/ }));
    expect(screen.queryByText(/Rebuilds after running jobs finish/)).toBeNull();
    fireEvent.click(screen.getByRole('button', { name: 'Save' }));
    await waitFor(() => expect(api.count('PUT', '/api/v1/images/i1/layers')).toBe(1));
  });

  it('folds what is inside: base tools and extra packages per repository', async () => {
    renderApp(detailApi(image()), '/setup/images/i1');
    expect(await screen.findByText('What is inside this image')).toBeTruthy();
    expect(screen.getByText(/Always included: Ubuntu 24\.04 · git/)).toBeTruthy();
    expect(screen.getByText('octo/app: libssl-dev, ffmpeg')).toBeTruthy();
  });

  it('asks before a rebuild and names the machine and the jobs it waits for (UX-62)', async () => {
    const api = detailApi(image()).on('POST', '/api/v1/images/:id/rebuild', (call) =>
      call.query.get('confirm')
        ? { id: 'c9', machine_id: 'm1', kind: 'image_rebuild', state: 'waiting' }
        : apiError(409, 'needs_confirmation', 'Needs a yes.', [
            { kind: 'machine', id: 'm1', text: 'Machine build-1 drains' },
            { kind: 'job', id: '9', text: 'octo/app e2e' },
          ]),
    );
    renderApp(api, '/setup/images/i1');
    fireEvent.click(await screen.findByRole('button', { name: 'Rebuild' }));
    const dialog = await screen.findByRole('dialog', {
      name: 'Rebuild the image of build-1, Linux?',
    });
    expect(within(dialog).getByText('Machine build-1 drains')).toBeTruthy();
    expect(within(dialog).getByText('octo/app e2e')).toBeTruthy();
    expect(api.calls.filter((c) => c.query.get('confirm')).length).toBe(0);
    fireEvent.click(within(dialog).getByRole('button', { name: 'Rebuild' }));
    await waitFor(() =>
      expect(api.calls.filter((c) => c.query.get('confirm') === 'true').length).toBe(1),
    );
  });

  it('shows a rebuilding image as busy', async () => {
    renderApp(detailApi(image({ state: 'rebuilding' })), '/setup/images/i1');
    expect(
      await screen.findByRole('progressbar', { name: /Rebuilding build-1/ }),
    ).toBeTruthy();
    expect(
      screen.getByRole<HTMLButtonElement>('button', { name: 'Rebuilding' }).disabled,
    ).toBe(true);
  });
});

describe('Caches', () => {
  const withCaches = (items: Schemas['Cache'][]) =>
    base().on('GET', '/api/v1/caches', { items });

  it('says what is empty and offers Add runners to an admin', async () => {
    renderApp(withCaches([]), '/setup/images?tab=caches');
    expect(
      await screen.findByText(
        'No caches yet. Caches show here when a runner has been added.',
      ),
    ).toBeTruthy();
    expect(screen.getAllByRole('button', { name: 'Add runners' }).length).toBe(1);
    expect(screen.getByRole('region', { name: 'Caches' })).toBeTruthy();
    expect(screen.getByRole('heading', { level: 1 })).toBeTruthy();
  });

  it('says could not load, why and what next', async () => {
    renderApp(
      base().on('GET', '/api/v1/caches', () =>
        apiError(503, 'unavailable', 'The database is busy.'),
      ),
      '/setup/images?tab=caches',
    );
    expect(await screen.findByText(/Could not load caches\./)).toBeTruthy();
  });

  it('opens from /caches and groups by machine with size against cap', async () => {
    renderApp(
      withCaches([
        cache(),
        cache({
          id: 'c2',
          kind: 'rust_cache',
          size_bytes: 12_000_000_000,
          last_wiped_at: '2026-11-13T10:00:00Z',
        }),
      ]),
      '/setup/images?tab=caches',
    );
    const section = await screen.findByRole('region', { name: 'build-1' });
    const tool = within(section).getByRole('region', {
      name: 'Tool cache of crsp-linux-1',
    });
    expect(within(tool).getByText('3.2 GB of 10.0 GB (32%)')).toBeTruthy();
    expect(within(tool).getByText('never')).toBeTruthy();
    const rust = within(section).getByRole('region', {
      name: 'Rust build cache of crsp-linux-1',
    });
    expect(
      within(rust).getByText('12.0 GB of 10.0 GB (120%), over the cap'),
    ).toBeTruthy();
    expect(within(rust).getByText(/2026-11-13 10:00/)).toBeTruthy();
  });

  it('shows Wipe disabled with its reason to a viewer (R5.13)', async () => {
    const api = signedIn(viewer)
      .on('GET', '/api/v1/machines', { items: [machine()] })
      .on('GET', '/api/v1/caches', { items: [cache()] });
    renderApp(api, '/setup/images?tab=caches');
    const wipe = await screen.findByRole<HTMLButtonElement>('button', { name: 'Wipe' });
    expect(wipe.disabled).toBe(true);
    expect(screen.getByText('Only an admin can do this.')).toBeTruthy();
  });

  const wipeApi = () =>
    withCaches([cache()]).on('POST', '/api/v1/caches/:id/wipe', (call) =>
      call.query.get('confirm')
        ? cache({ state: 'wiping', waiting_for: ['octo/app e2e'] })
        : apiError(409, 'needs_confirmation', 'Needs a yes.', [
            {
              kind: 'cache',
              id: 'c1',
              text: 'Tool cache of crsp-linux-1, 3.2 GB',
            },
            { kind: 'job', id: '9', text: 'octo/app e2e' },
          ]),
    );

  it('asks first, names the cache, its size and the jobs, and does nothing before yes (UX-62)', async () => {
    const api = wipeApi();
    renderApp(api, '/setup/images?tab=caches');
    fireEvent.click(await screen.findByRole('button', { name: 'Wipe' }));
    const dialog = await screen.findByRole('dialog', {
      name: 'Wipe Tool cache of crsp-linux-1?',
    });
    expect(within(dialog).getByText('Tool cache of crsp-linux-1, 3.2 GB')).toBeTruthy();
    expect(within(dialog).getByText('octo/app e2e')).toBeTruthy();
    expect(api.calls.filter((c) => c.query.get('confirm')).length).toBe(0);
    fireEvent.click(within(dialog).getByRole('button', { name: 'Cancel' }));
    await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull());
    expect(api.calls.filter((c) => c.query.get('confirm')).length).toBe(0);
  });

  it('wipes on yes and shows the wiping row and the strip entry naming what it waits for', async () => {
    const api = wipeApi();
    renderApp(api, '/setup/images?tab=caches');
    fireEvent.click(await screen.findByRole('button', { name: 'Wipe' }));
    const dialog = await screen.findByRole('dialog');
    fireEvent.click(within(dialog).getByRole('button', { name: 'Wipe' }));
    await waitFor(() =>
      expect(api.calls.filter((c) => c.query.get('confirm') === 'true').length).toBe(1),
    );
    const strip = await screen.findByRole('region', { name: 'Running work' });
    expect(
      within(strip).getAllByText(
        'Wiping Tool cache of crsp-linux-1, waiting for octo/app e2e',
      ).length,
    ).toBeGreaterThan(0);
  });

  it('shows a wiping cache until the server says ready (live event), then clears the strip', async () => {
    let state: Schemas['Cache'] = cache({
      state: 'wiping',
      waiting_for: ['octo/app e2e'],
    });
    const api = base().on('GET', '/api/v1/caches', () => ({ items: [state] }));
    renderApp(api, '/setup/images?tab=caches');
    const busy = await screen.findByRole<HTMLButtonElement>('button', { name: 'Wiping' });
    expect(busy.disabled).toBe(true);
    expect(screen.getByText('Waiting for octo/app e2e.')).toBeTruthy();
    expect(await screen.findByRole('region', { name: 'Running work' })).toBeTruthy();
    state = cache({ last_wiped_at: '2026-11-14T10:05:00Z', size_bytes: 0 });
    act(() =>
      FakeEventSource.latest().emit({
        id: '5',
        type: 'cache.state',
        at: '2026-11-14T10:05:00Z',
        cache: state,
      }),
    );
    expect(await screen.findByRole('button', { name: 'Wipe' })).toBeTruthy();
    await waitFor(() =>
      expect(screen.queryByRole('region', { name: 'Running work' })).toBeNull(),
    );
  });

  it('polls while a wipe runs, so the row ends without an event', async () => {
    const old = TIMING.cachePollMs;
    TIMING.cachePollMs = 20;
    try {
      let state: Schemas['Cache'] = cache({ state: 'wiping' });
      const api = base().on('GET', '/api/v1/caches', () => ({ items: [state] }));
      renderApp(api, '/setup/images?tab=caches', { stream: false });
      await screen.findByRole('button', { name: 'Wiping' });
      state = cache();
      expect(await screen.findByRole('button', { name: 'Wipe' })).toBeTruthy();
    } finally {
      TIMING.cachePollMs = old;
    }
  });

  it('says why the last wipe failed', async () => {
    renderApp(
      withCaches([cache({ error: 'The folder is locked.' })]),
      '/setup/images?tab=caches',
    );
    expect(
      await screen.findByText(
        'Could not wipe Tool cache of crsp-linux-1. The folder is locked.',
      ),
    ).toBeTruthy();
  });

  it('keeps the card open and says why when the server refuses the wipe', async () => {
    const api = withCaches([cache()]).on('POST', '/api/v1/caches/:id/wipe', (call) =>
      call.query.get('confirm')
        ? apiError(503, 'unavailable', 'The machine is offline.')
        : apiError(409, 'needs_confirmation', 'Needs a yes.', []),
    );
    renderApp(api, '/setup/images?tab=caches');
    fireEvent.click(await screen.findByRole('button', { name: 'Wipe' }));
    const dialog = await screen.findByRole('dialog');
    expect(within(dialog).getByText('Tool cache of crsp-linux-1, 3.2 GB')).toBeTruthy();
    fireEvent.click(within(dialog).getByRole('button', { name: 'Wipe' }));
    expect(
      await within(dialog).findByText(/Problem: The machine is offline\./),
    ).toBeTruthy();
  });

  it('switches tabs', async () => {
    renderApp(withCaches([cache()]), '/setup/images');
    fireEvent.click(await screen.findByRole('tab', { name: 'Caches' }));
    expect(await screen.findByRole('region', { name: 'build-1' })).toBeTruthy();
  });
});
