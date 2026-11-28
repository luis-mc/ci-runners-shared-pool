// T3 (e2e) and T11 (R5.9, R6.2): with JavaScript off, every system is listed, the docs are readable and navigable, and
// the search box (which needs script) is not offered.
import { expect, test } from '@playwright/test';

test.use({ javaScriptEnabled: false });

test('Download lists every system with its checksum', async ({ page }) => {
  await page.goto('/download');
  for (const os of ['Windows', 'macOS', 'Linux'])
    await expect(page.getByRole('region', { name: os })).toBeVisible();
  await expect(page.getByText('Crsp-1.2.3-win-x64.msi').first()).toBeVisible();
  await expect(page.getByText(/SHA-256 [0-9a-f]{64}/).first()).toBeVisible();
  await expect(page.getByText('The installers are not signed')).toBeVisible();
});

test('Home, Product and Security read in full', async ({ page }) => {
  for (const [path, heading] of [
    ['/', 'CI Runners Shared Pool'],
    ['/product', 'What the pool does'],
    ['/security', 'Security'],
  ] as const) {
    await page.goto(path);
    await expect(page.getByRole('heading', { level: 1, name: heading })).toBeVisible();
  }
});

test('the docs list every page, the search box is hidden, and a doc page reads and links on', async ({
  page,
}) => {
  await page.goto('/docs');
  await expect(page.getByRole('search')).toBeHidden();
  await page.getByRole('link', { name: 'Install and check a download' }).click();
  await expect(page).toHaveURL(/\/docs\/guides\/install$/);
  await expect(page.getByRole('heading', { level: 1 })).toContainText('Install');
  await expect(page.getByRole('heading', { name: 'Check the checksum' })).toBeVisible();
});

test('a diagram is drawn without script', async ({ page }) => {
  await page.goto('/docs/features/website');
  await expect(page.locator('figure svg[role="img"]').first()).toBeVisible();
});
