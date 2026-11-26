// UX-72 (OPS46): the Remove button of a failed runner sits in the table's row action column and never wraps, and a
// status word and the reason after it in one cell are separated by a space, at 1300, 393 px and between.
import { expect, test } from '@playwright/test';
import { failure, runner, signedIn } from './fixtures';
import { jobsAndDoctor } from './jobs-doctor-fixtures';

const REASON = 'no usable container engine';

async function open(page: import('@playwright/test').Page, width: number) {
  const mock = jobsAndDoctor(signedIn());
  mock.on('GET', '/api/v1/runners', {
    items: [
      runner(),
      runner({
        id: 'r2',
        name: 'crsp-persistent-pool-3',
        state: 'failed',
        failed_reason: REASON,
        reason: REASON,
      }),
    ],
  });
  mock.on('GET', '/api/v1/runners/:id', failure(404, 'not_found', 'No such runner.'));
  await mock.install(page);
  await page.setViewportSize({ width, height: 900 });
  await page.goto('/setup/runners');
  await expect(page.getByRole('heading', { level: 1, name: 'Setup' })).toBeVisible();
}

for (const width of [1300, 1000, 393]) {
  test(`runners at ${width} px: Remove does not wrap and the state has a space`, async ({
    page,
  }) => {
    await open(page, width);
    const remove = page.getByRole('button', { name: 'Remove' });
    await expect(remove).toHaveCount(1);
    const box = await remove.boundingBox();
    const control = await page.evaluate(() => {
      const probe = document.createElement('div');
      probe.style.cssText =
        'position:absolute;visibility:hidden;inline-size:var(--ds-control-height)';
      document.body.append(probe);
      const n = probe.getBoundingClientRect().width;
      probe.remove();
      return n;
    });
    expect(box?.height ?? 999).toBeLessThanOrEqual(control + 1);
    // The word is whole on one line: Remove is not cut into pieces.
    expect(await remove.evaluate((b) => b.textContent)).toBe('Remove');
    const state = page
      .locator('td', { has: page.getByText(REASON) })
      .locator('.ds-table__value')
      .first();
    const gap = await state.evaluate((v: HTMLElement, text: string): number => {
      const status = v.querySelector('.ds-status');
      const reason = Array.from(v.children).find((c) =>
        (c.textContent ?? '').includes(text),
      );
      if (!status || !reason) return -1;
      const a: DOMRect = status.getBoundingClientRect();
      const b: DOMRect = reason.getClientRects()[0] ?? reason.getBoundingClientRect();
      // Same line: the space between the end of the word and the start of the reason; stacked: below it.
      return b.top > a.top + 2 ? 1 : b.left - a.right;
    }, REASON);
    expect(gap).toBeGreaterThan(2);
  });
}

// CRSP298, UX-86: the pool is one summary line, number first, on one line at every width; no label and value rows.
for (const width of [1300, 393, 360]) {
  test(`runners at ${width} px: the pool is one summary line`, async ({ page }) => {
    await open(page, width);
    const line = page.locator('.ds-summary');
    await expect(line).toHaveText(
      '1 machine · 2 runners — 1 idle · 0 busy · 1 not ready',
    );
    await expect(line.locator('[data-tone="warning"]')).toHaveText('1 not ready');
    await expect(page.locator('.ds-facts')).toHaveCount(0);
    const overflow = await page.evaluate(
      () => document.documentElement.scrollWidth > document.documentElement.clientWidth,
    );
    expect(overflow).toBe(false);
  });
}
