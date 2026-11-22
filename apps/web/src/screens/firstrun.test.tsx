// CRSP124: the first run in the console. Each test names the finding it covers.
import { cleanup, fireEvent, screen, waitFor, within } from '@testing-library/react';
import { afterEach, describe, expect, it } from 'vitest';
import { defaultLabels } from './runners/AddRunners';
import { firstOpenStep } from '../shell/Shell';
import { machine, renderApp, repository, runner, signedIn } from '../testing/mock';

afterEach(cleanup);

const page = (items: unknown[]) => ({ items, stale: { stale: false } });
const org = {
  id: 2,
  account_login: 'acme',
  account_type: 'Organization',
  suspended: false,
  repository_selection: 'all',
};

describe('default labels (finding 12)', () => {
  const repo = repository({
    variables: [
      {
        name: 'RUNS_ON_LINUX',
        status: 'ok',
        value: '["self-hosted","linux","crsp-home"]',
      },
    ],
  });
  it('follow the repository variable of the system, pool label included', () => {
    expect(defaultLabels(repo, 'linux')).toBe('self-hosted, linux, crsp-home');
  });
  it('fall back to the policy labels, then to self-hosted', () => {
    expect(defaultLabels(repo, 'windows')).toBe('self-hosted');
    expect(defaultLabels(undefined, 'linux')).toBe('self-hosted');
  });
});

describe('the banner names the next step (finding 3)', () => {
  it('says which step is open, and none when all are done', () => {
    const base = {
      has_app: true,
      has_owner: true,
      setup_link_redeemed: true,
    };
    expect(
      firstOpenStep({
        ...base,
        steps: {
          connect_github: 'done',
          first_machine: 'done',
          first_repository: 'todo',
        },
      }),
    ).toBe('turn on the first repository');
    expect(
      firstOpenStep({
        ...base,
        steps: {
          connect_github: 'done',
          first_machine: 'done',
          first_repository: 'done',
        },
      }),
    ).toBeUndefined();
  });
});

describe('the header and the machine list agree (finding 1)', () => {
  it('counts the machines online, never "no machines", while one is up', async () => {
    renderApp(signedIn(), '/');
    expect(await screen.findByText('1 of 1 machine online')).toBeTruthy();
    expect(screen.queryByText('no machines')).toBeNull();
  });
});

describe('Add runners (findings 10, 11, 12)', () => {
  const api = (installs: unknown[], repos: unknown[]) =>
    signedIn()
      .on('GET', '/api/v1/github/installations', page(installs))
      .on('GET', '/api/v1/github/repositories', page(repos));

  it('asks System, Machine, Number and Labels, and no repository (D31)', async () => {
    renderApp(api([org], [repository({ pool_on: true })]), '/pool/runners/add');
    await screen.findByRole('group', { name: 'Add runners' });
    await screen.findByRole('textbox', { name: /Labels/ });
    expect(screen.getByText('System')).toBeTruthy();
    expect(screen.getByText('Number of runners')).toBeTruthy();
    for (const name of ['Repository', 'Organization', 'Scope'])
      expect(screen.queryByText(name)).toBeNull();
    expect(screen.queryByRole('radio', { name: 'octo/app' })).toBeNull();
    expect(screen.queryByRole('radio', { name: 'An organization' })).toBeNull();
    expect(
      screen.getByText('Runners serve every repository that uses the pool.'),
    ).toBeTruthy();
  });

  it('plans the runners with no scope', async () => {
    const plan = {
      id: 'rp1',
      area: 'runners',
      state: 'planned',
      steps: [],
      created_at: '2026-11-18T00:00:00Z',
    };
    const a = api([], [repository({ pool_on: true })]).on(
      'POST',
      '/api/v1/runner-plans',
      plan,
    );
    renderApp(a, '/pool/runners/add');
    await screen.findByRole('textbox', { name: /Labels/ });
    fireEvent.click(screen.getByRole('button', { name: 'Next' }));
    await waitFor(() => expect(a.count('POST', '/api/v1/runner-plans')).toBe(1));
    const body = a.calls.find(
      (c) => c.method === 'POST' && c.path === '/api/v1/runner-plans',
    )?.body as Record<string, unknown>;
    expect(body).toBeDefined();
    expect('scope' in body).toBe(false);
    expect(body.count).toBe(1);
  });

  it('fills the labels the first pool repository asks for', async () => {
    const repo = repository({
      pool_on: true,
      variables: [
        { name: 'RUNS_ON_LINUX', status: 'ok', value: '["self-hosted","crsp-home"]' },
      ],
    });
    renderApp(api([], [repo]), '/pool/runners/add');
    await waitFor(() =>
      expect(
        screen.getByRole<HTMLInputElement>('textbox', { name: /Labels/ }).value,
      ).toBe('self-hosted, crsp-home'),
    );
  });

  it('keeps labels the user wrote', async () => {
    renderApp(api([], [repository({ pool_on: true })]), '/pool/runners/add');
    const labels = await screen.findByRole<HTMLInputElement>('textbox', {
      name: /Labels/,
    });
    fireEvent.change(labels, { target: { value: 'mine' } });
    await waitFor(() => expect(labels.value).toBe('mine'));
  });
});

describe('Turn on the pool (findings 11, 14)', () => {
  it('ticks the only repository', async () => {
    const api = signedIn().on('GET', '/api/v1/github/repositories', page([repository()]));
    renderApp(api, '/repositories/turn-on');
    const box = await screen.findByRole<HTMLInputElement>('checkbox', {
      name: 'Select octo/app',
    });
    expect(box.checked).toBe(true);
  });

  it('gives an action when no repository is free: install the App', async () => {
    const api = signedIn().on('GET', '/api/v1/github/repositories', page([]));
    renderApp(api, '/repositories/turn-on');
    expect(await screen.findByRole('button', { name: 'Add a repository' })).toBeTruthy();
    expect(screen.getByRole('button', { name: 'Open Repositories' })).toBeTruthy();
  });
});

describe('Repositories after GitHub installed the App (finding 14)', () => {
  it('tells it from setup_action, reads the list and cleans the address', async () => {
    const api = signedIn().on('GET', '/api/v1/github/repositories', page([repository()]));
    renderApp(api, '/repositories?installation_id=1&setup_action=install');
    expect(await screen.findByText(/GitHub saved the App installation/)).toBeTruthy();
    expect(await screen.findByRole('table', { name: 'octo repositories' })).toBeTruthy();
  });
});

describe('Runners explain themselves (findings 5, 6)', () => {
  it('says nothing about light runners, even for an old runner that carries the label (OPS49)', async () => {
    const api = signedIn().on('GET', '/api/v1/runners', {
      items: [runner({ name: 'crsp-light-1', labels: ['self-hosted', 'crsp-light'] })],
    });
    renderApp(api, '/pool/runners');
    await screen.findByRole('table', { name: /^Runners on/ });
    expect(screen.queryByText(/light runners/)).toBeNull();
  });

  it('gives the next action for a failed runner', async () => {
    const api = signedIn().on('GET', '/api/v1/runners', {
      items: [runner({ state: 'failed', failed_reason: 'The folder is left behind.' })],
    });
    renderApp(api, '/pool/runners');
    expect(await screen.findByText(/1 runner failed\./)).toBeTruthy();
    expect(screen.getByText('The folder is left behind.')).toBeTruthy();
    expect(screen.getByRole('button', { name: 'Remove' })).toBeTruthy();
  });
});

describe('the machine page (findings 7, 8, 9)', () => {
  const m = machine({
    backends: [
      { name: 'docker', present: true, usable: true, allowed: false, version: '27' },
      {
        name: 'firecracker',
        present: false,
        usable: false,
        allowed: false,
        reason: 'no /dev/kvm',
      },
    ],
    settings: { ...machine().settings, allowed_backends: [] },
  });

  it('writes a backend reason once', async () => {
    renderApp(signedIn().on('GET', '/api/v1/machines/m1', m), '/pool/machines/m1');
    await screen.findByRole('heading', { name: 'build-1' });
    expect(screen.getAllByText(/no \/dev\/kvm/)).toHaveLength(1);
  });

  it('says an installed backend is not turned on and ticks it on request', async () => {
    renderApp(signedIn().on('GET', '/api/v1/machines/m1', m), '/pool/machines/m1');
    const tick = await screen.findByRole('button', { name: 'Tick docker' });
    fireEvent.click(tick);
    const box = screen.getByRole<HTMLInputElement>('checkbox', { name: /docker/ });
    expect(box.checked).toBe(true);
  });

  it('keeps Remove in its own section with what it does', async () => {
    renderApp(signedIn().on('GET', '/api/v1/machines/m1', m), '/pool/machines/m1');
    const section = await screen.findByRole('region', { name: 'Remove crsp' });
    expect(within(section).getByText('Choose what happens to your data.')).toBeTruthy();
    expect(within(section).getByRole('button', { name: 'Remove crsp' })).toBeTruthy();
  });
});
