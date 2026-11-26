// CRSP299: real screenshots of the Runners page, the current bar and options 1..3, at 1300 and 393 px.
// Run: pnpm exec playwright test -c docs/work/crsp299/pw.config.ts (needs a dev server on 8562).
import { test } from '@playwright/test';
import { runner, signedIn } from '../../../e2e/tests/web-console/fixtures';

const OS = ['linux', 'windows', 'macos'] as const;
const SIZES = ['small', 'medium', 'large', 'xl'] as const;
const ISO = ['container', 'VM', 'microVM'] as const;
const STATES = ['idle', 'idle', 'busy', 'idle', 'failed'] as const;

function demo() {
  const items: Record<string, unknown>[] = [];
  const machines = [
    { id: 'm1', name: 'build-1' },
    { id: 'm2', name: 'win-2' },
    { id: 'm3', name: 'mac-3' },
  ];
  for (let i = 0; i < 24; i++) {
    const m = machines[i % 3]!;
    const os = OS[i % 3]!;
    const persistent = i % 4 === 0;
    const isolation = persistent ? 'persistent' : ISO[i % 3]!;
    items.push(
      runner({
        id: `r${i}`,
        name: `crsp-${os}-${i + 1}`,
        machine_id: m.id,
        machine_name: m.name,
        os,
        kind: persistent ? 'persistent' : 'ephemeral',
        backend: persistent ? 'persistent' : 'docker',
        isolation,
        keeps_state: persistent,
        size_class: SIZES[i % 4],
        state: STATES[i % 5],
        failed_reason: i % 5 === 4 ? 'The runner did not start.' : undefined,
        managed: i % 7 !== 0,
        current_job:
          STATES[i % 5] === 'busy'
            ? {
                id: i,
                repository: 'octo/app',
                run_id: 1,
                run_attempt: 1,
                github_job_id: i,
                job_name: 'e2e',
                labels: [],
                state: 'running',
                started_at: '2026-11-26T09:50:00Z',
              }
            : undefined,
      }),
    );
  }
  return items;
}

const shots = [
  { name: 'current', q: '' },
  { name: 'option1', q: '?option=1' },
  { name: 'option2', q: '?option=2' },
  { name: 'option3', q: '?option=3' },
];
const sizes = [
  { w: 1300, h: 1500 },
  { w: 393, h: 1900 },
];

for (const s of shots)
  for (const v of sizes)
    test(`${s.name} at ${v.w}`, async ({ page }) => {
      await page.setViewportSize({ width: v.w, height: v.h });
      const api = signedIn().on('GET', '/api/v1/runners', { items: demo() });
      await api.install(page);
      await page.goto(`/setup/runners${s.q}`);
      await page.getByRole('group', { name: 'Runners toolbar' }).waitFor();
      await page.screenshot({
        path: `docs/work/crsp299/${s.name}-${v.w}.png`,
        fullPage: false,
      });
      await page
        .getByRole('region', { name: 'Runners', exact: true })
        .screenshot({ path: `docs/work/crsp299/${s.name}-bar-${v.w}.png` });
      if (s.name === 'option3') {
        await page.getByRole('button', { name: 'Filters' }).click();
        await page.getByRole('dialog').waitFor();
        await page.screenshot({ path: `docs/work/crsp299/option3-sheet-${v.w}.png` });
      }
    });
