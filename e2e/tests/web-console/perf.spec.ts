// R6.12 (proposed, owner question Q14): the first screen is usable within the budget, the first JavaScript is within
// the budget gzipped, and a live event shows on screen within the budget. The numbers are in
// apps/web/perf-budgets.json, the only place to change them.
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { gzipSync } from 'node:zlib';
import { expect, test } from '@playwright/test';
import { overview, signedIn } from './fixtures';

const budgets = JSON.parse(
  readFileSync(resolve('apps/web/perf-budgets.json'), 'utf8'),
) as {
  firstScreenUsableMs: number;
  firstJavaScriptGzipBytes: number;
  liveEventOnScreenMs: number;
};

declare global {
  interface Window {
    __emitLive?: (ev: unknown) => void;
  }
}

/** Replaces EventSource with one the test drives: the snapshot arrives at once, later events when asked. */
async function fakeStream(page: import('@playwright/test').Page) {
  await page.addInitScript((snapshot) => {
    const sources: {
      fire: (ev: unknown) => void;
    }[] = [];
    class FakeSource {
      onopen: ((e: unknown) => void) | null = null;
      onerror: ((e: unknown) => void) | null = null;
      onmessage: ((e: { data: string }) => void) | null = null;
      private listeners: ((e: { data: string }) => void)[] = [];
      constructor() {
        sources.push({ fire: (ev) => this.fire(ev) });
        setTimeout(() => {
          this.onopen?.({});
          this.fire({
            id: '1',
            type: 'snapshot',
            at: new Date().toISOString(),
            snapshot,
          });
        }, 0);
      }
      addEventListener(_t: string, fn: (e: { data: string }) => void) {
        this.listeners.push(fn);
      }
      fire(ev: unknown) {
        const e = { data: JSON.stringify(ev) };
        this.onmessage?.(e);
        for (const l of this.listeners) l(e);
      }
      close() {}
    }
    (window as unknown as { EventSource: unknown }).EventSource = FakeSource;
    window.__emitLive = (ev) => sources.forEach((s) => s.fire(ev));
  }, overview());
}

test('the first screen is usable within the budget', async ({ page }) => {
  await signedIn().install(page);
  const start = Date.now();
  await page.goto('/');
  await expect(page.getByRole('heading', { level: 1, name: 'Overview' })).toBeVisible({
    timeout: budgets.firstScreenUsableMs,
  });
  await expect(page.getByRole('navigation', { name: 'Areas' })).toBeVisible();
  const ms = Date.now() - start;
  console.log(
    `perf: first screen usable in ${ms} ms (budget ${budgets.firstScreenUsableMs})`,
  );
  expect(ms).toBeLessThanOrEqual(budgets.firstScreenUsableMs);
});

test('the first JavaScript is within the gzip budget', async ({ page }) => {
  await signedIn().install(page);
  const sizes: Record<string, number> = {};
  page.on('response', async (res) => {
    const type = res.headers()['content-type'] ?? '';
    if (!/javascript/.test(type)) return;
    sizes[res.url()] = gzipSync(await res.body()).length;
  });
  await page.goto('/');
  await expect(page.getByRole('heading', { level: 1, name: 'Overview' })).toBeVisible();
  await page.waitForLoadState('networkidle');
  const total = Object.values(sizes).reduce((a, b) => a + b, 0);
  console.log(
    `perf: first JavaScript ${total} bytes gzipped (budget ${budgets.firstJavaScriptGzipBytes})`,
  );
  expect(Object.keys(sizes).length).toBeGreaterThan(0);
  expect(total).toBeLessThanOrEqual(budgets.firstJavaScriptGzipBytes);
});

test('a live event shows on screen within the budget', async ({ page }) => {
  await signedIn().install(page);
  await fakeStream(page);
  await page.goto('/');
  // LOOK61: the Queued count of the Overview is the second tile.
  const queued = page.getByRole('link', { name: 'Open the queue plan' });
  await expect(queued).toBeVisible();
  await expect(queued).toContainText('2');
  const start = Date.now();
  await page.evaluate(
    (snap) =>
      window.__emitLive?.({
        id: '2',
        type: 'snapshot',
        at: new Date().toISOString(),
        snapshot: { ...snap, queued_count: 37 },
      }),
    overview(),
  );
  await expect(queued).toContainText('37', {
    timeout: budgets.liveEventOnScreenMs,
  });
  const ms = Date.now() - start;
  console.log(
    `perf: live event on screen in ${ms} ms (budget ${budgets.liveEventOnScreenMs})`,
  );
  expect(ms).toBeLessThanOrEqual(budgets.liveEventOnScreenMs);
});
