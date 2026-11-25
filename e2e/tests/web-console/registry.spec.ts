// REG5 (registry R5.37 to R5.40): the Registry screens of the console, read from the mocked registry API. The
// checks of the RES20 review build (D18, UX-4, UX-62) run here against the real routes, and every screen passes the
// DOM layout and axe checks at 360, 393 and 1300 px.
import AxeBuilder from '@axe-core/playwright';
import { expect, test } from '@playwright/test';
import { expectDomLayout } from '../../../scripts/design/dom-layout';
import { failure, signedIn } from './fixtures';
import { jobsAndDoctor } from './jobs-doctor-fixtures';
import { registryApi } from './registry-fixtures';

const WIDTHS = [360, 393, 1300] as const;

const SCREENS: { name: string; path: string; h1: string }[] = [
  { name: 'registry tab', path: '/setup/images?tab=registry', h1: 'Setup' },
  { name: 'image page', path: '/setup/images/registry/img_docsv4', h1: 'Node web' },
  { name: 'budgets', path: '/setup/images/registry/budgets', h1: 'Registry budgets' },
  {
    name: 'mirror',
    path: '/setup/images/registry/mirror',
    h1: 'Mirror of upstream images',
  },
  {
    name: 'replace, compare',
    path: '/setup/images/registry/img_docsv4/replace',
    h1: 'Replace an image',
  },
  {
    name: 'replace, done after reload',
    path: '/setup/images/registry/img_docsv4/replace?plan=plan_1',
    h1: 'Replace an image',
  },
];

for (const width of WIDTHS) {
  for (const s of SCREENS) {
    test(`${s.name} at ${width} px: h1, layout, axe`, async ({ page }) => {
      await jobsAndDoctor(signedIn()).install(page);
      await page.setViewportSize({ width, height: 900 });
      await page.goto(s.path);
      await expect(page.getByRole('heading', { level: 1, name: s.h1 })).toBeVisible();
      await page.waitForLoadState('networkidle');
      await expectDomLayout(page, `${s.name} ${width}`, { spaceVar: '--ds-space-' });
      const axe = await new AxeBuilder({ page }).analyze();
      expect(
        axe.violations.filter((v) => v.impact === 'serious' || v.impact === 'critical'),
      ).toEqual([]);
    });
  }
}

test('the registry answering 501 shows the failed state, not a blank page', async ({
  page,
}) => {
  const mock = jobsAndDoctor(signedIn());
  mock.on(
    'GET',
    '/api/v1/registry/overview',
    failure(501, 'not_implemented', 'Not built yet.'),
  );
  await mock.install(page);
  await page.goto('/setup/images?tab=registry');
  await expect(page.getByText('Could not load the registry.')).toBeVisible();
  await expect(page.getByRole('button', { name: 'Try again' })).toBeVisible();
});

// UX-4: the registry tab has one primary, the first recommendation that waits for the owner.
test('the registry tab has one primary: the newer image', async ({ page }) => {
  await jobsAndDoctor(signedIn()).install(page);
  await page.goto('/setup/images?tab=registry');
  const bar = page.locator('.ds-action-bar');
  await expect(bar.getByRole('button', { name: 'Review the newer image' })).toBeVisible();
  await expect(bar.locator('[data-kind="primary"]')).toHaveCount(1);
  await expect(page.getByRole('heading', { name: 'Recommendations' })).toBeVisible();
});

// R5.40: a list never deletes.
test('no row of the registry lists has a Delete button', async ({ page }) => {
  await jobsAndDoctor(signedIn()).install(page);
  await page.goto('/setup/images?tab=registry');
  await expect(
    page.getByRole('heading', { name: 'Images in the registry' }),
  ).toBeVisible();
  await expect(page.getByRole('button', { name: /Delete/ })).toHaveCount(0);
});

// D18: an image a runner uses is never deleted; the question card stays open and names the job.
test('the registry refuses to delete an image a runner uses', async ({ page }) => {
  await jobsAndDoctor(signedIn()).install(page);
  await page.goto('/setup/images/registry/img_goold');
  await page.getByRole('button', { name: 'Delete', exact: true }).click();
  const card = page.getByRole('dialog');
  await expect(card.getByText(/job 8133 of octo\/app/)).toBeVisible();
  await card.getByRole('button', { name: 'Delete', exact: true }).click();
  await expect(card.getByText(/A runner uses this image/)).toBeVisible();
  await expect(card).toBeVisible();
  await expect(page.locator('h1')).toHaveText('Go, Node and Postgres');
});

test('the registry deletes an unused image after a yes and goes back to the tab', async ({
  page,
}) => {
  const mock = jobsAndDoctor(signedIn());
  await mock.install(page);
  await page.goto('/setup/images/registry/img_mobile');
  await page.getByRole('button', { name: 'Delete', exact: true }).click();
  const card = page.getByRole('dialog');
  await expect(card.getByText(/3\.9 GB freed/)).toBeVisible();
  await card.getByRole('button', { name: 'Delete', exact: true }).click();
  await expect(page.getByRole('tab', { name: 'Registry' })).toBeVisible();
  expect(
    mock.calls.filter((c) => c.method === 'DELETE' && c.query.get('confirm') === 'true'),
  ).toHaveLength(1);
});

// The replace flow compares the stack files of the old and the new image, then runs and ends on Done.
test('the replace wizard names both images, keeps the old one for N days and ends on Done', async ({
  page,
}) => {
  const mock = registryApi(jobsAndDoctor(signedIn()));
  await mock.install(page);
  await page.goto('/setup/images/registry/img_docsv4/replace');
  await expect(page.getByText('pnpm-lock.yaml')).toBeVisible();
  await expect(
    page.getByRole('button', { name: /^Keep the old image for/ }),
  ).toBeVisible();
  await page.locator('.ds-button[data-primary="true"]', { hasText: 'Review' }).click();
  await expect(page.getByText('Nothing has changed yet.')).toBeVisible();
  await page.getByRole('button', { name: 'Replace the image' }).click();
  await expect(page.getByText(/replaced b7e204/)).toBeVisible({ timeout: 10_000 });
  await expect(page.getByRole('button', { name: 'Open the registry' })).toBeVisible();
});

// UX-104 style edit page: the budgets count their changes.
test('the budgets page counts unsaved changes', async ({ page }) => {
  await jobsAndDoctor(signedIn()).install(page);
  await page.goto('/setup/images/registry/budgets');
  await page.getByRole('button', { name: /^Custom images\s*15/ }).click();
  for (const key of ['2', '5'])
    await page.getByRole('button', { name: key, exact: true }).click();
  await page.getByRole('button', { name: 'Done', exact: true }).click();
  await expect(page.getByText('Unsaved changes (1)')).toBeVisible();
});
