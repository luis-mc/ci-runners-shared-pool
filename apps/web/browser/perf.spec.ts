// T15 (R6.1): Largest Contentful Paint under 2.0 s on a mid phone over 4G, and no layout shift above 0.05. The phone is
// emulated: 4x slower CPU and a 4G connection (9 Mbit/s down, 170 ms round trip). The JavaScript size is checked by the
// build test.
import { expect, test, type Page } from '@playwright/test';

test.use({ viewport: { width: 393, height: 800 }, hasTouch: true, isMobile: true });

async function emulatePhone(page: Page): Promise<void> {
  const cdp = await page.context().newCDPSession(page);
  await cdp.send('Network.enable');
  await cdp.send('Network.emulateNetworkConditions', {
    offline: false,
    latency: 170,
    downloadThroughput: (9 * 1024 * 1024) / 8,
    uploadThroughput: (3 * 1024 * 1024) / 8,
  });
  await cdp.send('Emulation.setCPUThrottlingRate', { rate: 4 });
}

for (const path of ['/', '/product', '/download', '/docs', '/docs/guides/install']) {
  test(`${path} paints fast and stays still`, async ({ page }) => {
    await emulatePhone(page);
    await page.addInitScript(() => {
      const w = window as unknown as { __lcp: number; __cls: number };
      w.__lcp = 0;
      w.__cls = 0;
      new PerformanceObserver((list) => {
        for (const e of list.getEntries()) w.__lcp = e.startTime;
      }).observe({ type: 'largest-contentful-paint', buffered: true });
      new PerformanceObserver((list) => {
        for (const e of list.getEntries() as unknown as {
          value: number;
          hadRecentInput: boolean;
        }[])
          if (!e.hadRecentInput) w.__cls += e.value;
      }).observe({ type: 'layout-shift', buffered: true });
    });
    await page.goto(path);
    await page.waitForLoadState('networkidle');
    // Scroll to the end so late shifts count, then read.
    await page.evaluate(() => window.scrollTo(0, document.body.scrollHeight));
    await page.evaluate(
      () => new Promise((r) => requestAnimationFrame(() => requestAnimationFrame(r))),
    );
    const { lcp, cls } = await page.evaluate(() => {
      const w = window as unknown as { __lcp: number; __cls: number };
      return { lcp: w.__lcp, cls: w.__cls };
    });
    expect(lcp, 'LCP in ms').toBeLessThan(2000);
    expect(cls, 'cumulative layout shift').toBeLessThan(0.05);
  });
}
