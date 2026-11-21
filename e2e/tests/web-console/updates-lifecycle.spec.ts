// Settings > Updates and the Remove dialog (lifecycle standard) against the mocked API: policy and window, Check,
// the retry text after a failed test, an update on another machine, Remove disabled until a data choice is made,
// and the DOM layout of the page, the remove dialog and the banner at 360, 393, 1300 and 1800 px (guard A).
import { expect, test, type Page } from '@playwright/test';
import {
  domLayoutSource,
  judgeLayout,
  type LayoutProblem,
} from '../../../scripts/design/dom-layout';
import { signedIn } from './fixtures';

const WIDTHS = [
  { width: 360, height: 780 },
  { width: 393, height: 852 },
  { width: 1300, height: 900 },
  { width: 1800, height: 1000 },
] as const;

interface State {
  status: Record<string, unknown>;
  calls: { method: string; path: string; body: unknown }[];
}

const base = () => ({
  state: 'idle',
  current: '1.4.0',
  available: null,
  required: false,
  policy: 'automatic_when_idle',
  window: null,
  next_check_at: '2026-11-21T20:00:00Z',
  failures: null,
  waiting_for_jobs: false,
});

async function lifecycleApi(
  page: Page,
  over: Record<string, unknown> = {},
): Promise<State> {
  const st: State = { status: { ...base(), ...over }, calls: [] };
  await page.route('**/api/update/**', async (route) => {
    const req = route.request();
    const path = new URL(req.url()).pathname;
    const text = req.postData();
    st.calls.push({
      method: req.method(),
      path,
      body: text ? (JSON.parse(text) as unknown) : undefined,
    });
    if (req.method() === 'GET') {
      if (path.endsWith('/status')) return route.fulfill({ json: st.status });
      if (path.endsWith('/history'))
        return route.fulfill({
          json: [
            {
              at: '2026-11-21T10:00:00Z',
              actor: 'luis',
              action: 'install',
              version: '1.4.0',
              result: 'ok',
            },
          ],
        });
      return route.fulfill({
        json: [
          { machine_id: 'm2', name: 'build-2', version: '1.3.0', state: 'needs_you' },
        ],
      });
    }
    if (path.endsWith('/policy'))
      st.status = { ...st.status, ...(text ? (JSON.parse(text) as object) : {}) };
    return route.fulfill({ json: {} });
  });
  await page.route('**/api/remove', async (route) => {
    const text = route.request().postData();
    st.calls.push({
      method: 'POST',
      path: '/api/remove',
      body: text ? (JSON.parse(text) as unknown) : undefined,
    });
    return route.fulfill({ json: {} });
  });
  return st;
}

async function layoutAtAllWidths(page: Page, screen: string): Promise<void> {
  const found: { width: number; rule: LayoutProblem['rule']; text: string }[] = [];
  for (const size of WIDTHS) {
    await page.setViewportSize(size);
    for (const p of await page.evaluate<LayoutProblem[]>(
      domLayoutSource({ spaceVar: '--ds-space-' }),
    ))
      found.push({ width: size.width, rule: p.rule, text: p.text });
  }
  const { unknown, stale } = judgeLayout(screen, found, []);
  expect([...unknown, ...stale]).toEqual([]);
}

test('Updates: policy, window, Look for updates and the history', async ({ page }) => {
  await signedIn().install(page);
  const st = await lifecycleApi(page);
  await page.goto('/settings/updates');
  const card = page.getByRole('region', { name: 'Updates' });
  await expect(card).toBeVisible();
  await expect(card.getByText('Up to date. Version 1.4.0 is the newest.')).toBeVisible();
  await expect(page.getByRole('table', { name: 'Update history' })).toContainText('luis');

  await card.getByRole('radio', { name: /Ask first/ }).click();
  await expect(card.getByRole('radio', { name: /Ask first/ })).toBeChecked();
  await expect
    .poll(() => st.calls.filter((c) => c.path === '/api/update/policy').length)
    .toBe(1);
  expect(st.calls.find((c) => c.path === '/api/update/policy')?.body).toEqual({
    policy: 'ask_first',
    window: null,
  });

  await card.getByRole('button', { name: 'Look for updates' }).click();
  await expect
    .poll(() => st.calls.filter((c) => c.path === '/api/update/check').length)
    .toBe(1);
});

test('Updates: a time window needs both times', async ({ page }) => {
  await signedIn().install(page);
  const st = await lifecycleApi(page);
  await page.goto('/settings/updates');
  await page.getByLabel(/From/).fill('01:00');
  expect(st.calls.filter((c) => c.path === '/api/update/policy')).toHaveLength(0);
  await page.getByLabel(/Until/).fill('05:00');
  await expect
    .poll(() => st.calls.filter((c) => c.path === '/api/update/policy').length)
    .toBe(1);
  expect(st.calls.find((c) => c.path === '/api/update/policy')?.body).toEqual({
    policy: 'automatic_when_idle',
    window: { start: '01:00', end: '05:00' },
  });
});

test('Updates: a failed test says when it tries again; the banner offers a retry', async ({
  page,
}) => {
  await signedIn().install(page);
  await lifecycleApi(page, {
    state: 'rolled_back',
    available: '1.5.0',
    failures: { version: '1.5.0', count: 1, retry_at: '2026-11-21T20:00:00Z' },
  });
  await page.goto('/settings/updates');
  await expect(
    page
      .getByText(/failed its test\. Version 1\.4\.0 is running again\. We try again at/)
      .first(),
  ).toBeVisible();
  const banner = page.locator('[data-lifecycle="update-banner"]');
  await expect(banner).toHaveAttribute('data-state', 'failed');
  await expect(banner.getByRole('button', { name: 'Try again' })).toBeVisible();
});

test('Updates: start an update on another machine', async ({ page }) => {
  await signedIn().install(page);
  const st = await lifecycleApi(page, {
    state: 'available',
    available: '1.5.0',
    policy: 'ask_first',
  });
  await page.goto('/settings/updates');
  await page.getByRole('button', { name: 'Update build-2' }).click();
  const remote = page.getByRole('region', { name: 'Update build-2' });
  await remote.getByRole('button', { name: 'Update build-2' }).click();
  await expect
    .poll(() =>
      st.calls.find(
        (c) =>
          c.path === '/api/update/install' &&
          (c.body as { machine_id?: string })?.machine_id,
      ),
    )
    .toBeTruthy();
});

test('Remove stays disabled until a data choice is made', async ({ page }) => {
  await signedIn().install(page);
  const st = await lifecycleApi(page);
  await page.goto('/settings/updates');
  await page.getByRole('button', { name: 'Remove crsp' }).click();
  const dialog = page.getByRole('dialog', { name: 'Remove crsp' });
  const confirm = dialog.getByRole('button', { name: 'Remove crsp' });
  await expect(confirm).toBeDisabled();
  await expect(
    dialog.getByText('Keep my data or Delete my data too is chosen'),
  ).toBeVisible();
  await expect(dialog.getByRole('radio', { name: /Keep my data/ })).not.toBeChecked();
  await expect(
    dialog.getByRole('radio', { name: /Delete my data too/ }),
  ).not.toBeChecked();
  await dialog.getByRole('radio', { name: /Delete my data too/ }).check();
  await expect(confirm).toBeEnabled();
  await confirm.click();
  await expect
    .poll(() => st.calls.find((c) => c.path === '/api/remove')?.body)
    .toEqual({ delete_data: true });
  await expect(page.getByText(/Removing crsp\. This page stops working/)).toBeVisible();
});

test('layout at 360, 393, 1300 and 1800 px: the page, the banner and the remove dialog', async ({
  page,
}) => {
  await signedIn().install(page);
  await lifecycleApi(page, {
    state: 'available',
    available: '1.5.0',
    policy: 'ask_first',
  });
  await page.goto('/settings/updates');
  await expect(page.getByRole('region', { name: 'Updates' })).toBeVisible();
  await expect(page.locator('[data-lifecycle="update-banner"]')).toBeVisible();
  await layoutAtAllWidths(page, 'settings updates lifecycle');
  await page.getByRole('button', { name: 'Remove crsp' }).click();
  await expect(page.getByRole('dialog', { name: 'Remove crsp' })).toBeVisible();
  await layoutAtAllWidths(page, 'remove dialog');
});
