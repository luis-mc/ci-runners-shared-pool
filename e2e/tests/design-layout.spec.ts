import { expect, test } from '@playwright/test';
import { expectDomLayout } from '../../scripts/design/dom-layout';
import {
  failure,
  needingRecovery,
  publicRows,
  signedIn,
  stoppedMachine,
} from './web-console/fixtures';
import { jobsAndDoctor } from './web-console/jobs-doctor-fixtures';

// The DOM layout checker (design system, Enforcement kit) on the console's M1 screens, at 360, 393 and 1300 px.
// It uses the DOM only. The known list (scripts/design/layout.known.json) is empty and only shrinks.
const SCREENS: {
  name: string;
  path: string;
  ready: string;
  loads?: string[];
  recovery?: boolean;
  stopped?: boolean;
  publicRows?: boolean;
  signedOut?: boolean;
  policy?: boolean;
  tweak?: (m: ReturnType<typeof signedIn>) => unknown;
}[] = [
  { name: 'sign-in', path: '/sign-in', ready: 'Sign in', signedOut: true },
  { name: 'first-run wizard', path: '/setup', ready: 'Set up the pool' },
  {
    name: 'first-run without a setup link',
    path: '/setup',
    ready: 'Set up the pool',
    tweak: (m: ReturnType<typeof signedIn>) =>
      m.on('GET', '/api/v1/setup', {
        has_app: false,
        has_owner: false,
        setup_link_redeemed: false,
        steps: {
          connect_github: 'todo',
          first_machine: 'todo',
          first_repository: 'todo',
        },
      }),
  },
  { name: 'overview', path: '/', ready: 'Overview' },
  { name: 'repositories', path: '/repositories', ready: 'Repositories' },
  {
    name: 'repositories with a public row while the setting is off',
    path: '/repositories',
    ready: 'Repositories',
    publicRows: true,
  },
  { name: 'repository detail', path: '/repositories/octo/app', ready: 'octo/app' },
  {
    name: 'repository policy',
    path: '/repositories/octo/app',
    ready: 'octo/app',
    policy: true,
  },
  { name: 'turn on the pool', path: '/repositories/turn-on', ready: 'Turn on the pool' },
  { name: 'machines', path: '/pool/machines', ready: 'Machines' },
  { name: 'machine detail', path: '/pool/machines/m1', ready: 'build-1' },
  {
    name: 'machine detail stopped',
    path: '/pool/machines/m1',
    ready: 'build-1',
    stopped: true,
  },
  {
    name: 'machine detail needing recovery',
    path: '/pool/machines/m1',
    ready: 'build-1',
    recovery: true,
  },
  { name: 'add a machine', path: '/pool/machines/add', ready: 'Add a machine' },
  { name: 'runners', path: '/pool/runners', ready: 'Runners' },
  { name: 'jobs', path: '/jobs', ready: 'Jobs' },
  { name: 'jobs live', path: '/jobs/live', ready: 'Jobs' },
  { name: 'jobs done', path: '/jobs/done', ready: 'Jobs' },
  { name: 'jobs run elsewhere', path: '/jobs/elsewhere', ready: 'Jobs' },
  {
    name: 'jobs empty',
    path: '/jobs',
    ready: 'Jobs',
    tweak: (m: ReturnType<typeof signedIn>) =>
      m.on('GET', '/api/v1/jobs', { items: [], stale: { stale: false } }),
  },
  { name: 'job detail', path: '/jobs/2', ready: 'test-2' },
  { name: 'doctor', path: '/doctor', ready: 'Doctor' },
  {
    name: 'doctor with runner leftovers',
    path: '/doctor',
    ready: 'Doctor',
    tweak: (m: ReturnType<typeof signedIn>) =>
      m.on('GET', '/api/v1/doctor/findings', {
        items: [
          {
            check: 'machine.runner_leftovers',
            subject: { kind: 'machine', id: 'm1', name: 'build-1' },
            severity: 'warn',
            title: 'build-1 has runner leftovers no runner owns',
            fix: { text: 'Remove the leftovers.' },
            needs_admin: false,
            items: [
              {
                kind: 'service',
                name: 'actions.runner.octo-app.crsp-old',
                account: '.\\crsp-old',
                why: 'No runner of this controller owns it.',
              },
              {
                kind: 'folder',
                name: 'C:\\ProgramData\\crsp\\runners\\crsp-old\\_work',
                size_bytes: 2147483648,
                why: 'No runner record points to it.',
              },
            ],
            action: { id: 'remove_runner_leftovers', label: 'Remove' },
            milestone: 1,
            checked_at: '2026-11-14T10:00:12Z',
          },
        ],
        last_run_at: '2026-11-14T10:00:12Z',
      }),
  },
  { name: 'add runners', path: '/pool/runners/add', ready: 'Add runners' },
  { name: 'images', path: '/pool/images', ready: 'Images and caches' },
  { name: 'image detail', path: '/pool/images/img_m1_linux', ready: 'build-1' },
  { name: 'caches', path: '/pool/images?tab=caches', ready: 'Images and caches' },
  { name: 'settings access', path: '/settings/access', ready: 'Settings' },
  {
    name: 'settings repositories',
    path: '/settings/repositories',
    ready: 'Settings',
    loads: ['/api/v1/settings/pool', '/api/v1/settings/queue'],
  },
  { name: 'settings console', path: '/settings/console', ready: 'Settings' },
  { name: 'settings updates', path: '/settings/updates', ready: 'Settings' },
  { name: 'settings data', path: '/settings/data', ready: 'Settings' },
  { name: 'settings audit log', path: '/settings/audit', ready: 'Settings' },
  { name: 'settings old address', path: '/settings/backups', ready: 'Settings' },
  // The small page the tray opens (TRAY4): asks first, follows the stage, says what failed.
  ...(['confirm', 'running', 'failed', 'done'] as const).map((state) => ({
    name: `update page ${state}`,
    path: '/update',
    ready: 'Update',
    tweak: (m: ReturnType<typeof signedIn>) =>
      m.on('GET', '/api/v1/update', updateStatus(state)),
  })),
];

function updateStatus(state: 'confirm' | 'running' | 'failed' | 'done') {
  const base = {
    current_version: '1.4.0',
    latest_version: '1.5.0',
    update_available: true,
    state: 'idle',
    auto_update: false,
    running_jobs: [],
    drain_timeout_minutes: 60,
  };
  if (state === 'running')
    return {
      ...base,
      state: 'downloading',
      stage: {
        name: 'downloading',
        percent: 42,
        version: '1.5.0',
        at: new Date().toISOString(),
      },
    };
  if (state === 'failed')
    return {
      ...base,
      state: 'failed',
      error: 'The installer failed with exit code 1603',
      stage: {
        name: 'failed',
        failed_at: 'installing',
        error: 'The installer failed with exit code 1603',
        log_path: 'C:\\ProgramData\\crsp\\logs\\msi-update.log',
        at: new Date().toISOString(),
      },
    };
  if (state === 'done')
    return {
      ...base,
      current_version: '1.5.0',
      update_available: false,
      stage: {
        name: 'done',
        percent: 100,
        version: '1.5.0',
        at: new Date().toISOString(),
      },
    };
  return base;
}

for (const screen of SCREENS) {
  test(`layout: ${screen.name}`, async ({ page }) => {
    const mock = jobsAndDoctor(signedIn());
    screen.tweak?.(mock);
    if (screen.recovery) mock.on('GET', '/api/v1/machines/:id', needingRecovery());
    if (screen.stopped) mock.on('GET', '/api/v1/machines/:id', stoppedMachine());
    if (screen.publicRows) mock.on('GET', '/api/v1/github/repositories', publicRows());
    if (screen.loads) {
      mock
        .on('GET', '/api/v1/settings/pool', {
          budget_memory_pct: 85,
          budget_cpu_pct: 60,
          size_slots_from_machine: true,
          default_job_slots: 2,
          drain_timeout_minutes: 60,
        })
        .on('GET', '/api/v1/settings/queue', {
          order: ['priority', 'event_class', 'fair_share', 'queued_at'],
          fair_share: true,
          no_runner_wait_minutes: 2,
        });
    }
    mock.on('GET', '/api/v1/audit', {
      items: [
        {
          id: 1,
          at: '2026-11-14T10:00:00Z',
          actor: 'octo',
          source: 'web',
          action: 'machine.drain',
          target_type: 'machine',
          target_id: 'm1',
          summary: 'drained build-1',
          result: 'done',
        },
        {
          id: 2,
          at: '2026-11-21T14:08:00Z',
          actor: 'luis-mc',
          source: 'web',
          action: 'variable.restored',
          target_type: 'repository',
          target_id: 'luis-mc/ci-runners-shared-pool',
          summary: 'restored the variable BUILD_CACHE_REGISTRY to its saved value',
          result: 'done',
        },
      ],
    });
    if (screen.signedOut)
      mock.on('GET', '/api/v1/me', failure(401, 'unauthenticated', 'Sign in.'));
    await mock.install(page);
    await page.goto(screen.path);
    await expect(
      page.getByRole('heading', { level: 1, name: screen.ready }),
    ).toBeVisible();
    if (screen.loads) await expect(page.getByText('Queue order')).toBeVisible();
    if (screen.recovery)
      await expect(page.getByRole('button', { name: 'Recover' })).toBeVisible();
    await page.waitForLoadState('networkidle');
    if (screen.policy)
      await expect(page.getByRole('heading', { name: 'Policy' })).toBeVisible();
    await expectDomLayout(page, screen.name, { spaceVar: '--ds-space-' });
  });
}

// LOOK32, UX-3: the machine page header keeps Stop new jobs and Resume at the right edge, with the hint right-aligned
// under the button, and the header and the Facts box do not move between the two states.
for (const width of [1300, 393, 360]) {
  test(`machine header: action at the end, same height in both states at ${width} px`, async ({
    page,
  }) => {
    const mock = jobsAndDoctor(signedIn());
    await mock.install(page);
    await page.setViewportSize({ width, height: 900 });
    const measure = async (word: string) => {
      await page.goto('/pool/machines/m1');
      const button = page.getByRole('button', { name: word, exact: true });
      await expect(button).toBeVisible();
      const header = (await page.locator('.ds-page-header').boundingBox())!;
      const b = (await button.boundingBox())!;
      const hint = (await page.locator('.ds-page-header__hint').boundingBox())!;
      const facts = (await page.getByRole('heading', { name: 'Facts' }).boundingBox())!;
      return { header, b, hint, facts };
    };
    const before = await measure('Stop new jobs');
    mock.on('GET', '/api/v1/machines/:id', stoppedMachine());
    const after = await measure('Resume');
    for (const m of [before, after]) {
      const right = m.header.x + m.header.width;
      if (width >= 600) expect(Math.abs(m.b.x + m.b.width - right)).toBeLessThan(2);
      expect(m.hint.x + m.hint.width).toBeLessThanOrEqual(right + 1);
      if (width >= 600) expect(Math.abs(m.hint.x + m.hint.width - right)).toBeLessThan(2);
    }
    expect(Math.abs(after.header.height - before.header.height)).toBeLessThan(1);
    expect(Math.abs(after.facts.y - before.facts.y)).toBeLessThan(1);
  });
}

// UX-15: on a wide screen the step bar of a wizard is one row, not a wrapped narrow column (CRSP124).
for (const path of ['/repositories/turn-on', '/pool/runners/add', '/pool/machines/add']) {
  test(`step bar is one row at 1300 px: ${path}`, async ({ page }) => {
    const mock = jobsAndDoctor(signedIn());
    await mock.install(page);
    await page.setViewportSize({ width: 1300, height: 900 });
    await page.goto(path);
    await expect(page.getByRole('list', { name: 'Steps' })).toBeVisible();
    const tops = await page
      .getByRole('list', { name: 'Steps' })
      .locator('li')
      .evaluateAll((els) => els.map((e) => Math.round(e.getBoundingClientRect().top)));
    expect(new Set(tops).size).toBe(1);
    const frame = await page.locator('.ds-page').first().boundingBox();
    expect(frame?.width ?? 0).toBeGreaterThan(900);
  });
}

// CRSP132, UX-11: on a wide screen the toolbar above a list is one row (search, filters, selection, count, actions).
for (const path of ['/repositories', '/pool/machines', '/pool/runners', '/jobs/live']) {
  test(`list toolbar is one row at 1300 px: ${path}`, async ({ page }) => {
    // The Repositories toolbar (search, Pool, Active types, Select all, count, Add) no longer fits one row inside its pane.
    test.fixme(
      path === '/repositories',
      'CRSP162 toolbar wraps inside the pane at 1300 px',
    );
    const mock = jobsAndDoctor(signedIn());
    await mock.install(page);
    await page.setViewportSize({ width: 1300, height: 900 });
    await page.goto(path);
    const bar = page.locator('.ds-list-toolbar').first();
    await expect(bar).toBeVisible();
    const tops = await bar.locator(':scope > *').evaluateAll((els) =>
      els.map((e) => {
        const r = e.getBoundingClientRect();
        return Math.round(r.top + r.height / 2);
      }),
    );
    expect(tops.length).toBeGreaterThan(1);
    expect(Math.max(...tops) - Math.min(...tops)).toBeLessThan(8);
  });
}

// UX-15, UX-68, CRSP157: the Settings tabs are one row when they fit; on a phone they wrap onto more rows and never
// scroll sideways, and the page does not scroll sideways either.
for (const width of [1232, 1300, 393, 360]) {
  test(`settings tabs do not scroll sideways at ${width} px`, async ({ page }) => {
    const mock = jobsAndDoctor(signedIn());
    await mock.install(page);
    await page.setViewportSize({ width, height: 900 });
    await page.goto('/settings/users');
    const list = page.getByRole('tablist', { name: 'Settings' });
    await expect(list).toBeVisible();
    if (width >= 1232) {
      const tops = await list
        .getByRole('tab')
        .evaluateAll((els) => els.map((e) => Math.round(e.getBoundingClientRect().top)));
      expect(new Set(tops).size).toBe(1);
    }
    const tabsFit = await list.evaluate((el) => el.scrollWidth <= el.clientWidth);
    expect(tabsFit).toBe(true);
    const page_ = await page.evaluate(
      () => document.documentElement.scrollWidth <= document.documentElement.clientWidth,
    );
    expect(page_).toBe(true);
  });
}

// CRSP140: every titled part of a page is a pane with its title in the top border.
test('settings access for CI shows panes and the form only on demand', async ({
  page,
}) => {
  const mock = jobsAndDoctor(signedIn());
  await mock.install(page);
  await page.goto('/settings/access');
  await expect(page.getByRole('region', { name: 'Access for CI' })).toBeVisible();
  await expect(page.locator('.ds-pane').first()).toBeVisible();
  await expect(page.getByRole('region', { name: 'Make an access' })).toHaveCount(0);
  await page.getByRole('button', { name: 'Make an access' }).click();
  await expect(page.getByRole('region', { name: 'Make an access' })).toBeVisible();
});

// CRSP159, UX-15, UX-16: on a phone the areas are one Menu button, not four rows of links; every area is
// reachable by opening it.
for (const width of [360, 393]) {
  test(`the areas are one Menu button at ${width} px`, async ({ page }) => {
    const mock = jobsAndDoctor(signedIn());
    await mock.install(page);
    await page.setViewportSize({ width, height: 800 });
    await page.goto('/');
    const button = page.getByRole('button', { name: /^Menu:/ });
    await expect(button).toBeVisible();
    await expect(page.getByRole('navigation', { name: 'Areas' })).toHaveCount(0);
    expect((await button.boundingBox())?.height ?? 99).toBeLessThanOrEqual(60);
    await button.click();
    await expect(
      page.getByRole('navigation', { name: 'Areas' }).getByRole('link'),
    ).toHaveCount(7);
  });
}

// CRSP160, UX-6: with every step done the setup page offers one primary that goes on, and no word sends the owner to a terminal.
test('setup done: one primary, Open the overview, and no terminal words', async ({
  page,
}) => {
  await jobsAndDoctor(signedIn()).install(page);
  await page.goto('/setup');
  const go = page.getByRole('button', { name: 'Open the overview' });
  await expect(go).toBeVisible();
  await expect(page.locator('[data-kind="primary"]')).toHaveCount(1);
  await expect(page.locator('main')).not.toContainText(/crsp open|terminal/i);
  await go.click();
  await expect(page.getByRole('heading', { level: 1, name: 'Overview' })).toBeVisible();
});

// CRSP160, UX-6: the first-run text offers the tray's Open, not a command.
test('setup without a link names the tray, not a terminal', async ({ page }) => {
  const mock = jobsAndDoctor(signedIn());
  mock.on('GET', '/api/v1/setup', {
    has_app: false,
    has_owner: false,
    setup_link_redeemed: false,
    steps: { connect_github: 'todo', first_machine: 'todo', first_repository: 'todo' },
  });
  await mock.install(page);
  await page.goto('/setup');
  await expect(page.getByText(/Open in the tray menu/)).toBeVisible();
  await expect(page.locator('main')).not.toContainText(/crsp open|terminal/i);
});

// LOOK36, UX-69, tokens (spacing): a tab row and the page under it. At every width the page title is one section gap
// (--ds-space-6) under the tab row, and the phone's Menu button, the first tab label of every row and the title
// start on the frame's one left edge. A tab row follows component 3: it wraps onto more rows, no label is cut.
for (const path of ['/pool', '/pool/machines', '/settings']) {
  for (const width of [1300, 393, 360]) {
    test(`tab row, title gap and left edges at ${width} px: ${path}`, async ({
      page,
    }) => {
      const mock = jobsAndDoctor(signedIn());
      await mock.install(page);
      await page.setViewportSize({ width, height: 900 });
      await page.goto(path);
      await expect(page.locator('[role=tablist]').first()).toBeVisible();
      await page.waitForLoadState('networkidle');
      const m = await page.evaluate(() => {
        const left = (e: Element) => {
          const r = document.createRange();
          r.selectNodeContents(e);
          return r.getBoundingClientRect().left;
        };
        const probe = document.createElement('i');
        probe.style.cssText =
          'position:absolute;visibility:hidden;padding-left:var(--ds-space-6)';
        document.body.appendChild(probe);
        const gapToken = parseFloat(getComputedStyle(probe).paddingLeft);
        probe.remove();
        const list = document.querySelector('[role=tablist]')!;
        const lr = list.getBoundingClientRect();
        const h1 = document.querySelector('main h1')!;
        const h1r = h1.getBoundingClientRect();
        const main = document.querySelector('main')!;
        const frameLeft =
          main.getBoundingClientRect().left +
          parseFloat(getComputedStyle(main).paddingLeft);
        const menu = Array.from(document.querySelectorAll('button')).find((b) =>
          /^Menu:/.test(b.textContent ?? ''),
        );
        const tabs = Array.from(list.querySelectorAll('[role=tab]'));
        const rowStarts = new Map<number, number>();
        for (const t of tabs) {
          const top = Math.round(t.getBoundingClientRect().top);
          if (!rowStarts.has(top)) rowStarts.set(top, left(t));
        }
        return {
          gapToken,
          titleBelowTabs: h1r.top >= lr.bottom - 1,
          gap: h1r.top - lr.bottom,
          frameLeft,
          menuBox: menu?.getBoundingClientRect().left ?? null,
          tabLeft: [...rowStarts.values()],
          h1Left: left(h1),
          cut: tabs.filter((t) => t.getBoundingClientRect().right > lr.right + 1).length,
          wrapped: tabs.filter(
            (t) => t.getClientRects().length > 0 && t.scrollWidth > t.clientWidth + 1,
          ).length,
        };
      });
      if (m.titleBelowTabs) expect(Math.abs(m.gap - m.gapToken)).toBeLessThan(1.5);
      expect(Math.abs(m.h1Left - m.frameLeft)).toBeLessThan(1);
      for (const l of m.tabLeft) expect(Math.abs(l - m.frameLeft)).toBeLessThan(1);
      if (width < 600) {
        expect(m.menuBox).not.toBeNull();
        expect(Math.abs((m.menuBox ?? 0) - m.frameLeft)).toBeLessThan(1);
      }
      expect(m.cut).toBe(0);
      expect(m.wrapped).toBe(0);
    });
  }
}

// LOOK49, UX-78: the audit log stays inside its pane, a date and time is one line, and Export is in the toolbar.
for (const width of [1300, 393, 360]) {
  test(`audit log: inside the pane, time on one line, Export in the toolbar at ${width} px`, async ({
    page,
  }) => {
    const mock = jobsAndDoctor(signedIn()).on('GET', '/api/v1/audit', {
      items: [
        {
          id: 2,
          at: '2026-11-21T14:08:00Z',
          actor: 'luis-mc',
          source: 'web',
          action: 'variable.restored',
          target_type: 'repository',
          target_id: 'luis-mc/ci-runners-shared-pool',
          summary: 'restored the variable BUILD_CACHE_REGISTRY to its saved value',
          result: 'done',
        },
      ],
    });
    await mock.install(page);
    await page.setViewportSize({ width, height: 900 });
    await page.goto('/settings/audit');
    const table = page.getByRole('table', { name: 'Audit log' });
    await expect(table).toBeVisible();
    const m = await page.evaluate(() => {
      const t = document.querySelector('table.ds-table');
      const frame = t?.closest('.ds-table-frame')?.getBoundingClientRect();
      const cells = Array.from(t?.querySelectorAll('td') ?? []).map(
        (c) => c.getBoundingClientRect().right,
      );
      const time = document.querySelector('.ds-datetime');
      const lines = time
        ? new Set(
            Array.from(
              (() => {
                const r = document.createRange();
                r.selectNodeContents(time);
                return r.getClientRects();
              })(),
            ).map((q) => Math.round(q.top)),
          ).size
        : 0;
      const bar = document.querySelector('.ds-list-toolbar');
      const exp = Array.from(document.querySelectorAll('button')).find(
        (b) => b.textContent === 'Export',
      );
      return {
        frameRight: frame?.right ?? 0,
        cellRight: Math.max(0, ...cells),
        lines,
        exportInBar: Boolean(bar && exp && bar.contains(exp)),
        details: t?.textContent ?? '',
      };
    });
    expect(m.lines).toBe(1);
    expect(m.cellRight).toBeLessThanOrEqual(m.frameRight + 1);
    expect(m.exportInBar).toBe(true);
    expect(m.details).not.toContain('detail=');
    expect(m.details).toContain('luis-mc/ci-runners-shared-pool');
    await expectDomLayout(page, 'settings audit log', { spaceVar: '--ds-space-' });
  });
}

// LOOK49: the fields of Budget and slots top-align with their boxes on one line, however long a name or help is.
for (const width of [1300, 393, 360]) {
  test(`machine page: Budget and slots fields top-align at ${width} px`, async ({
    page,
  }) => {
    const mock = jobsAndDoctor(signedIn());
    await mock.install(page);
    await page.setViewportSize({ width, height: 900 });
    await page.goto('/pool/machines/m1');
    await expect(page.getByRole('heading', { level: 1, name: 'build-1' })).toBeVisible();
    await expect(page.getByText('Time to finish running jobs')).toBeVisible();
    const rows = await page.evaluate(() => {
      const grid = document.querySelector('.ds-field-grid');
      const fields = Array.from(grid?.querySelectorAll(':scope > .ds-field') ?? []);
      return fields.map((f) => ({
        top: Math.round(f.getBoundingClientRect().top),
        box: Math.round(f.querySelector('.ds-number')?.getBoundingClientRect().top ?? -1),
      }));
    });
    expect(rows.length).toBe(4);
    const byTop = new Map<number, number[]>();
    for (const r of rows) byTop.set(r.top, [...(byTop.get(r.top) ?? []), r.box]);
    for (const boxes of byTop.values()) expect(new Set(boxes).size).toBe(1);
    await expectDomLayout(page, 'machine detail', { spaceVar: '--ds-space-' });
  });
}
