// LOOK44, LOOK47, LOOK63: the Help page. A side menu of 13 sections; Start here shows the job path and nine question
// cards with an Open link each; nothing is locked; a ? beside a term opens a short answer that links to its section.
// Layout at 360, 393 and 1300 px is checked for every route by ux-routes.spec.ts; this file checks the behaviour
// and the layout of Help at 360, 393, 1278 and 1300 px.
import { expect, test } from '@playwright/test';
import { machine, signedIn } from './fixtures';

const real = machine({
  settings: {
    budget_memory_pct: 70,
    budget_cpu_pct: 55,
    job_slots: 3,
    light_slots: 1,
    heavy_slots: 2,
    allowed_backends: ['docker'],
    drain_timeout_minutes: 30,
    windows_symlink_right: false,
  },
});

test('Help is the seventh item of the menu and shows the 13 sections, the picture and nine cards', async ({
  page,
}) => {
  await signedIn().install(page);
  await page.goto('/');
  const nav = page.getByRole('navigation', { name: 'Areas' });
  await expect(nav.getByRole('link')).toHaveCount(7);
  await nav.getByRole('link', { name: 'Help' }).click();
  await expect(page).toHaveURL(/\/help$/);
  await expect(page.getByRole('heading', { level: 1, name: 'Help' })).toBeVisible();
  await expect(page.getByRole('list', { name: 'Steps' })).toHaveCount(0);
  const menu = page.getByRole('navigation', { name: 'Help sections' });
  await expect(menu.getByRole('link')).toHaveCount(13);
  await expect(menu.getByRole('link', { name: /Admission and passing/ })).toBeVisible();
  const picture = page.getByRole('region', { name: 'How a job gets run' });
  const questions = page.getByRole('region', { name: 'Questions' });
  await expect(picture).toBeVisible();
  await expect(questions.getByRole('heading', { level: 3 })).toHaveCount(9);
  await expect(questions.getByRole('link', { name: /^Open / })).toHaveCount(9);
  await expect(picture.locator('button')).toHaveCount(0);
});

test('every new section opens from the menu', async ({ page }) => {
  await signedIn().install(page);
  await page.goto('/help');
  const menu = page.getByRole('navigation', { name: 'Help sections' });
  for (const name of [
    'Pool and load budget',
    'Runner types',
    'Size classes',
    'Admission and passing',
    'Reservation',
    'Job size',
    'Before assignment',
    'Gating',
    'Routing labels',
    'Timers',
  ]) {
    await menu.getByRole('link', { name: new RegExp(name) }).click();
    await expect(page.getByRole('region', { name })).toBeVisible();
  }
});

test('an Open link of a card goes to its console screen', async ({ page }) => {
  await signedIn().install(page);
  await page.goto('/help');
  const link = page.getByRole('link', { name: 'Open Pool > Live' });
  await link.click();
  await expect(page).toHaveURL(/\/pool\/live$/);
});

test('the ? beside Slots opens a short answer that links to its Help section', async ({
  page,
}) => {
  await signedIn().on('GET', '/api/v1/machines/:id', real).install(page);
  await page.goto('/setup/machines/m1');
  await page.getByRole('button', { name: 'What does Slots mean?' }).click();
  const panel = page.getByRole('group', { name: 'Slots' });
  await expect(panel).toBeVisible();
  await panel.getByRole('link', { name: 'More about this in Help' }).click();
  await expect(page).toHaveURL(/\/help\/pool-and-load-budget$/);
  await expect(page.getByRole('region', { name: 'Pool and load budget' })).toBeVisible();
});

test('Escape closes the ? answer', async ({ page }) => {
  // LOOK61: the Overview no longer carries the Queue term; the machine page's Slots term shows the same.
  await signedIn().on('GET', '/api/v1/machines/:id', real).install(page);
  await page.goto('/setup/machines/m1');
  await page.getByRole('button', { name: 'What does Slots mean?' }).click();
  await expect(page.getByRole('group', { name: 'Slots' })).toBeVisible();
  await page.keyboard.press('Escape');
  await expect(page.getByRole('group', { name: 'Slots' })).toHaveCount(0);
});

test('the first-run Overview shows the picture and Open Help', async ({ page }) => {
  await signedIn()
    .on('GET', '/api/v1/machines', { items: [] })
    .on('GET', '/api/v1/runners', { items: [] })
    .install(page);
  await page.goto('/');
  const picture = page.getByRole('region', { name: 'How a job gets run' });
  await expect(picture).toBeVisible();
  await picture.getByRole('button', { name: 'Open Help' }).click();
  await expect(page).toHaveURL(/\/help$/);
});

const SECTIONS = [
  '',
  '/where',
  '/pool-and-load-budget',
  '/runner-types',
  '/size-classes',
  '/admission',
  '/reservation',
  '/job-size',
  '/before-assignment',
  '/gating',
  '/routing-labels',
  '/timers',
  '/queue',
];
for (const width of [360, 393, 1278, 1300]) {
  test(`every Help section is inside the frame at ${width} px`, async ({ page }) => {
    await page.setViewportSize({ width, height: 900 });
    await signedIn().install(page);
    for (const section of SECTIONS) {
      await page.goto(`/help${section}`);
      await expect(page.getByRole('heading', { level: 1, name: 'Help' })).toBeVisible();
      await expect(page.getByRole('navigation', { name: 'Help sections' })).toBeVisible();
      const over = await page.evaluate(
        () => document.documentElement.scrollWidth - document.documentElement.clientWidth,
      );
      expect(over, `/help${section}`).toBeLessThanOrEqual(0);
    }
  });
}

for (const width of [360, 393, 1278, 1300]) {
  test(`Help does not scroll sideways at ${width} px`, async ({ page }) => {
    await page.setViewportSize({ width, height: 900 });
    await signedIn().install(page);
    await page.goto('/help');
    await expect(page.getByRole('heading', { level: 1, name: 'Help' })).toBeVisible();
    const over = await page.evaluate(
      () => document.documentElement.scrollWidth - document.documentElement.clientWidth,
    );
    expect(over).toBeLessThanOrEqual(0);
  });
}

// LOOK63: at 1278 px with real names the last box of the picture was cut off and the page scrolled sideways.
test('the picture fits the frame at 1278 px with long real names', async ({ page }) => {
  await page.setViewportSize({ width: 1278, height: 1248 });
  const long = machine({ name: 'crsp-linux-build-machine-number-1' });
  await signedIn()
    .on('GET', '/api/v1/machines', { items: [long] })
    .on('GET', '/api/v1/runners', {
      items: [
        {
          id: 'r1',
          name: 'crsp-linux-build-machine-number-1-runner-1',
          machine_id: 'm1',
          machine_name: long.name,
          os: 'linux',
          arch: 'x64',
          kind: 'persistent',
          backend: 'persistent',
          isolation: 'persistent',
          labels: ['self-hosted'],
          scope: { kind: 'repository', repository_id: 7 },
          state: 'busy',
          current_job: 1,
          managed: true,
          keeps_state: true,
        },
      ],
    })
    .on('GET', '/api/v1/overview', {
      machines: [long],
      queued_count: 12,
      queued: [],
      running: [
        {
          id: 1,
          repository: 'luis-mc/ci-runners-shared-pool',
          run_id: 123456789,
          trigger: 'workflow_dispatch',
          job_name: 'integration-tests-windows',
          state: 'running',
        },
      ],
      last_event_id: '1',
    })
    .install(page);
  await page.goto('/help');
  const picture = page.getByRole('region', { name: 'How a job gets run' });
  await expect(
    picture.getByText('crsp-linux-build-machine-number-1', { exact: true }),
  ).toBeVisible();
  const frame = await page
    .locator('main')
    .evaluate((m) => m.getBoundingClientRect().right);
  const boxes = await picture
    .locator('.ds-jobpath__box')
    .evaluateAll((els) => els.map((e) => Math.round(e.getBoundingClientRect().right)));
  expect(boxes).toHaveLength(7);
  for (const right of boxes) expect(right).toBeLessThanOrEqual(frame);
  const over = await page.evaluate(
    () => document.documentElement.scrollWidth - document.documentElement.clientWidth,
  );
  expect(over).toBeLessThanOrEqual(0);
});
