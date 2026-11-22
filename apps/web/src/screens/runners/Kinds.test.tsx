// OPS28 (runners R5.89 to R5.92, web-console R5.101, R5.102): the kind choice of Add runners with its D94(b)
// default, the keep-dependencies choice, the runner page, and the runner kind of a repository.
import { cleanup, fireEvent, screen, waitFor, within } from '@testing-library/react';
import { afterEach, describe, expect, it } from 'vitest';
import {
  machine,
  renderApp,
  repository,
  runner,
  signedIn,
  type MockApi,
} from '../../testing/mock';
import { PERSISTENT_ELSEWHERE, chooseKind, kindReasons } from './kinds';

afterEach(cleanup);

const plan = {
  id: 'rp1',
  area: 'runners',
  state: 'planned',
  created_at: 'x',
  created_by: 'octo',
  steps: [
    {
      id: 's1',
      action: 'add_runner',
      text: 'Add runner',
      needs_yes: false,
      state: 'planned',
    },
  ],
};
const noDocker = machine({
  backends: [{ name: 'docker', present: true, usable: true, allowed: false }],
});
const list = (items: unknown[]) => ({ items, stale: { stale: false } });

function addApi(machines: unknown[], repos: unknown[]): MockApi {
  return signedIn()
    .on('GET', '/api/v1/machines', { items: machines })
    .on('GET', '/api/v1/github/repositories', list(repos))
    .on('POST', '/api/v1/runner-plans', plan);
}
const radio = (name: RegExp) => screen.findByRole<HTMLInputElement>('radio', { name });
const next = async () => {
  fireEvent.click(await screen.findByRole('button', { name: 'Next' }));
};
const planBody = (api: MockApi) =>
  api.calls.find((c) => c.method === 'POST' && c.path === '/api/v1/runner-plans')?.body;

describe('Add runners: kind (D93, D94(b))', () => {
  it('chooses Persistent while no public repository uses the pool, and says why', async () => {
    const api = addApi(
      [machine()],
      [repository({ visibility: 'private', pool_on: true })],
    );
    renderApp(api, '/pool/runners/add');
    expect((await radio(/^Persistent/)).checked).toBe(true);
    expect((await radio(/^Ephemeral/)).checked).toBe(false);
    expect(
      screen.getByText(
        'Persistent is chosen because no public repository uses the pool.',
      ),
    ).toBeTruthy();
    await next();
    await screen.findByText(/1 persistent runner on build-1/);
    expect(planBody(api)).toMatchObject({ kind: 'persistent', keep_dependencies: true });
  });

  it('chooses Ephemeral while a public repository uses the pool, with the ceiling', async () => {
    const api = addApi(
      [machine()],
      [repository({ visibility: 'public', pool_on: true })],
    );
    renderApp(api, '/pool/runners/add');
    expect((await radio(/^Ephemeral/)).checked).toBe(true);
    expect(
      screen.getByText('Ephemeral is chosen because a public repository uses the pool.'),
    ).toBeTruthy();
    expect(screen.getByLabelText('Most ephemeral runners at once')).toBeTruthy();
    expect(
      screen.queryByRole('checkbox', { name: /Keep downloaded dependencies/ }),
    ).toBeNull();
    await next();
    await screen.findByText(/Up to 1 ephemeral runner at once on build-1/);
    const body = planBody(api) as Record<string, unknown>;
    expect(body.kind).toBe('ephemeral');
    expect('keep_dependencies' in body).toBe(false);
  });

  it('keeps downloaded dependencies by default and sends the choice', async () => {
    const api = addApi([machine()], []);
    renderApp(api, '/pool/runners/add');
    const keep = await screen.findByRole<HTMLInputElement>('checkbox', {
      name: /Keep downloaded dependencies/,
    });
    expect(keep.checked).toBe(true);
    expect(
      screen.getByText(/Temporary files of each job are always removed\./),
    ).toBeTruthy();
    fireEvent.click(keep);
    await next();
    await screen.findByText(/Downloaded dependencies are removed after each job too/);
    expect(planBody(api)).toMatchObject({ kind: 'persistent', keep_dependencies: false });
  });

  it('a pick of the user stands over the default', async () => {
    const api = addApi(
      [machine()],
      [repository({ visibility: 'public', pool_on: true })],
    );
    renderApp(api, '/pool/runners/add');
    fireEvent.click(await radio(/^Persistent/));
    expect((await radio(/^Persistent/)).checked).toBe(true);
    expect(screen.queryByText(/is chosen because/)).toBeNull();
  });

  it('greys Ephemeral without the container backend, with the reason and a link', async () => {
    const api = addApi([noDocker], [repository({ visibility: 'public', pool_on: true })]);
    renderApp(api, '/pool/runners/add');
    const eph = await radio(/^Ephemeral/);
    expect(eph.disabled).toBe(true);
    expect(
      screen.getByText(
        'Ephemeral needs the container backend, which is not allowed on this machine.',
      ),
    ).toBeTruthy();
    // The preselected kind is not offered: the other one stands, with its reason.
    expect((await radio(/^Persistent/)).checked).toBe(true);
    expect(
      screen.getByText('Persistent is chosen because Ephemeral is not offered here.'),
    ).toBeTruthy();
    expect(
      screen
        .getByRole('button', { name: 'Allow it on the machine' })
        .getAttribute('href'),
    ).toBe('/pool/machines/m1');
  });

  it('greys Ephemeral for Windows and for macOS', () => {
    const win = machine({ os: 'windows' });
    const mac = machine({ os: 'macos' });
    expect(kindReasons(win, 'windows').ephemeral).toBe(
      'Ephemeral runners are not available for Windows yet. Windows runners are persistent.',
    );
    expect(kindReasons(mac, 'macos').ephemeral).toBe(
      'Ephemeral runners are not available for macOS yet. macOS runners are persistent.',
    );
    expect(kindReasons(win, 'windows').persistent).toBeUndefined();
    expect(
      chooseKind(mac, 'macos', [{ visibility: 'public', pool_on: true }], undefined).kind,
    ).toBe('persistent');
  });

  it('shows the Windows reason on the page', async () => {
    const api = addApi([machine({ os: 'windows' })], []);
    renderApp(api, '/pool/runners/add');
    fireEvent.click(await radio(/^Windows/));
    expect(
      await screen.findByText(
        'Ephemeral runners are not available for Windows yet. Windows runners are persistent.',
      ),
    ).toBeTruthy();
    expect((await radio(/^Ephemeral/)).disabled).toBe(true);
  });

  it('offers Persistent only where it runs on the machine itself (runners R5.134)', () => {
    const win = machine({ os: 'windows', container_os: 'linux' });
    expect(kindReasons(win, 'linux')).toEqual({
      persistent: PERSISTENT_ELSEWHERE('windows', 'linux'),
      ephemeral: undefined,
    });
    expect(chooseKind(win, 'linux', [], undefined).kind).toBe('ephemeral');
    const off = machine({ os: 'windows', container_os: 'linux', backends: [] });
    expect(kindReasons(off, 'linux').persistent).toBe(PERSISTENT_ELSEWHERE('windows', 'linux'));
    expect(chooseKind(off, 'linux', [], undefined).none).toBe(true);
    expect(kindReasons(machine({ os: 'linux' }), 'linux').persistent).toBeUndefined();
  });
});

describe('Runner page (runners R5.89, R5.90)', () => {
  const page = (over: Parameters<typeof runner>[0] = {}) =>
    signedIn()
      .on(
        'GET',
        '/api/v1/runners/r1',
        runner({ dependency_cache_bytes: 2147483648, ...over }),
      )
      .on('PATCH', '/api/v1/runners/r1/settings', runner())
      .on('POST', '/api/v1/runners/r1/clear-cache', runner());

  it('lists the kind and the setting in the runners list', async () => {
    const api = signedIn().on('GET', '/api/v1/runners', {
      items: [
        runner({ keep_dependencies: false }),
        runner({ id: 'r2', name: 'eph-1', kind: 'ephemeral', keeps_state: false }),
      ],
    });
    renderApp(api, '/pool/runners');
    const table = await screen.findByRole('table', { name: /^Runners on/ });
    // The Isolation cell is one line; the kind and what it keeps are in its hover text (UX-72).
    expect(
      table.querySelector('[title*="Persistent. Keeps dependencies: no"]'),
    ).toBeTruthy();
    expect(table.querySelector('[title*="Ephemeral"]')).toBeTruthy();
  });

  it('shows gated: offline by design, the weekly wake-up and why jobs wait (runners R5.140)', async () => {
    const api = page().on('GET', '/api/v1/listener-gate', {
      enforce: false,
      wake_every_days: 7,
      runners: [
        {
          runner_name: 'crsp-linux-1',
          machine_id: 'm1',
          os: 'linux',
          text: 'gated: offline by design',
          listener_on: false,
          next_wake: '2026-11-29T10:00:00Z',
          queue_text: 'waiting for room on M1: needs 6 CPU, 5 free',
        },
      ],
    });
    renderApp(api, '/pool/runners/r1');
    expect(await screen.findByText('gated: offline by design')).toBeTruthy();
    expect(screen.getByText('Weekly wake-up')).toBeTruthy();
    expect(screen.getByText('waiting for room on M1: needs 6 CPU, 5 free')).toBeTruthy();
    expect(screen.getByText(/The gate is in observe mode/)).toBeTruthy();
  });

  it('shows no Listener section for a runner the gate does not hold', async () => {
    const api = page().on('GET', '/api/v1/listener-gate', {
      enforce: true,
      wake_every_days: 7,
      runners: [],
    });
    renderApp(api, '/pool/runners/r1');
    await screen.findByText('Dependency cache size');
    expect(screen.queryByText('Weekly wake-up')).toBeNull();
  });

  it('saves the keep-dependencies setting with Save', async () => {
    const api = page({ keep_dependencies: true });
    renderApp(api, '/pool/runners/r1');
    const box = await screen.findByRole<HTMLInputElement>('checkbox', {
      name: /Keep downloaded dependencies/,
    });
    expect(box.checked).toBe(true);
    expect(screen.getByText('Dependency cache size')).toBeTruthy();
    expect(screen.getByText('2.1 GB')).toBeTruthy();
    fireEvent.click(box);
    expect(api.count('PATCH', '/api/v1/runners/r1/settings')).toBe(0);
    fireEvent.click(screen.getByRole('button', { name: 'Save' }));
    await waitFor(() =>
      expect(api.count('PATCH', '/api/v1/runners/r1/settings')).toBe(1),
    );
    expect(api.calls.find((c) => c.method === 'PATCH')?.body).toEqual({
      keep_dependencies: false,
    });
  });

  it('clears the cache now, and says it waits for a running job', async () => {
    const api = page({ state: 'busy' });
    renderApp(api, '/pool/runners/r1');
    expect(
      await screen.findByText('A job is running. Clearing waits for it to finish.'),
    ).toBeTruthy();
    fireEvent.click(screen.getByRole('button', { name: 'Clear now' }));
    await waitFor(() =>
      expect(api.count('POST', '/api/v1/runners/r1/clear-cache')).toBe(1),
    );
  });

  it('shows why a runner is not clean', async () => {
    const api = page({ unclean_reason: 'a process of the last job is still running' });
    renderApp(api, '/pool/runners/r1');
    expect(
      await screen.findByText(
        'Not clean: a process of the last job is still running. It takes no new job until it is clean.',
      ),
    ).toBeTruthy();
  });

  it('has no dependency setting for an ephemeral runner', async () => {
    const api = page({ kind: 'ephemeral', keeps_state: false });
    renderApp(api, '/pool/runners/r1');
    await screen.findByRole('heading', { level: 1, name: 'crsp-linux-1' });
    expect(screen.queryByRole('checkbox', { name: /Keep downloaded/ })).toBeNull();
    expect(screen.queryByRole('button', { name: 'Clear now' })).toBeNull();
  });
});

describe('Repositories: runner kind (github-integration R5.60)', () => {
  const pub = repository({
    visibility: 'public',
    pool_on: true,
    ephemeral_capacity: false,
    policy: {
      ...repository().policy,
      runner_kinds: 'ephemeral_only',
      runner_kinds_set: false,
    },
  });
  const apiFor = (r = pub) =>
    signedIn()
      .on('GET', '/api/v1/github/repositories', list([r]))
      .on('GET', '/api/v1/repositories/:id', r)
      .on('PATCH', '/api/v1/repositories/:id/policy', r);

  it('shows the kind with (default) and the capacity line in the list', async () => {
    renderApp(apiFor({ ...pub, visibility: 'private' }), '/repositories');
    expect(await screen.findByText('Ephemeral only (default)')).toBeTruthy();
    expect(
      screen.getByText('No ephemeral runner capacity: jobs would wait.'),
    ).toBeTruthy();
  });

  it('shows a chosen kind without (default)', async () => {
    const r = repository({
      visibility: 'private',
      policy: {
        ...repository().policy,
        runner_kinds: 'ephemeral_only',
        runner_kinds_set: true,
      },
    });
    renderApp(apiFor(r), '/repositories');
    expect(await screen.findByText('Ephemeral only')).toBeTruthy();
  });

  it('links to Add runners from the repository page when there is no capacity', async () => {
    renderApp(apiFor(), '/repositories/octo/app');
    const note = await screen.findByText(
      'No ephemeral runner capacity: jobs would wait.',
    );
    expect(
      within(note.closest('[role], div') as HTMLElement)
        .getByRole('button', { name: 'Add runners' })
        .getAttribute('href'),
    ).toBe('/pool/runners/add');
  });

  it('asks before a public repository takes Any runner, then saves it', async () => {
    const api = apiFor();
    renderApp(api, '/repositories/octo/app');
    fireEvent.click(await screen.findByRole('radio', { name: /^Any runner/ }));
    expect(
      screen.getAllByText(/Anyone who can open a pull request may reach it/).length,
    ).toBe(1);
    fireEvent.click(screen.getByRole('button', { name: 'Save' }));
    const dialog = await screen.findByRole('dialog', {
      name: 'Let public jobs use persistent runners?',
    });
    expect(api.count('PATCH', '/api/v1/repositories/7/policy')).toBe(0);
    fireEvent.click(within(dialog).getByRole('button', { name: 'Allow' }));
    await waitFor(() =>
      expect(api.count('PATCH', '/api/v1/repositories/7/policy')).toBe(1),
    );
    expect(api.calls.find((c) => c.method === 'PATCH')?.body).toMatchObject({
      runner_kinds: 'any_runner',
    });
  });

  it('uses the default again with one choice', async () => {
    const chosen = repository({
      visibility: 'private',
      policy: {
        ...repository().policy,
        runner_kinds: 'ephemeral_only',
        runner_kinds_set: true,
      },
    });
    const api = apiFor(chosen);
    renderApp(api, '/repositories/octo/app');
    fireEvent.click(
      await screen.findByRole('radio', { name: /^Use the default \(Any runner\)/ }),
    );
    fireEvent.click(screen.getByRole('button', { name: 'Save' }));
    await waitFor(() =>
      expect(api.count('PATCH', '/api/v1/repositories/7/policy')).toBe(1),
    );
    expect(api.calls.find((c) => c.method === 'PATCH')?.body).toMatchObject({
      runner_kinds: 'default',
    });
  });
});
