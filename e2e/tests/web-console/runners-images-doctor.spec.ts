// CRSP163 (CRSP152/154): Runners, Images and caches and Doctor keep their h1, show one primary, hold lists and
// results inside a pane (CORE-3, UX-4, UX-10, UX-12, UX-17, UX-61), at 360, 393 and 1300 px, with the DOM layout
// and axe checks. The default states are covered by design-layout.spec.ts and a11y.spec.ts; this file drives the
// empty and failed states and the pane facts.
import AxeBuilder from '@axe-core/playwright';
import { expect, test } from '@playwright/test';
import { expectDomLayout } from '../../../scripts/design/dom-layout';
import { failure, signedIn } from './fixtures';
import { jobsAndDoctor } from './jobs-doctor-fixtures';

const WIDTHS = [360, 393, 1300] as const;

const STATES: {
  name: string;
  path: string;
  h1: string;
  pane?: string;
  empty?: [string, unknown];
  failed?: string;
  primaries: number;
}[] = [
  {
    name: 'runners with rows',
    path: '/setup/runners',
    h1: 'Setup',
    pane: 'Runners',
    primaries: 1,
  },
  {
    name: 'runners empty',
    path: '/setup/runners',
    h1: 'Setup',
    pane: 'Runners',
    empty: ['/api/v1/runners', { items: [] }],
    primaries: 1,
  },
  {
    name: 'runners failed',
    path: '/setup/runners',
    h1: 'Setup',
    failed: '/api/v1/runners',
    primaries: 1,
  },
  {
    name: 'images empty',
    path: '/setup/images',
    h1: 'Setup',
    pane: 'Images',
    empty: ['/api/v1/images', { items: [] }],
    primaries: 1,
  },
  {
    name: 'caches empty',
    path: '/setup/images?tab=caches',
    h1: 'Setup',
    pane: 'Caches',
    empty: ['/api/v1/caches', { items: [] }],
    primaries: 1,
  },
  {
    name: 'image detail',
    path: '/setup/images/img_m1_linux',
    h1: 'build-1',
    pane: 'Layers',
    primaries: 1,
  },
  {
    name: 'images failed',
    path: '/setup/images',
    h1: 'Setup',
    failed: '/api/v1/images',
    primaries: 1,
  },
  {
    name: 'doctor empty',
    path: '/doctor',
    h1: 'Doctor',
    pane: 'Findings',
    empty: ['/api/v1/doctor/findings', { items: [], last_run_at: null }],
    primaries: 1,
  },
  {
    name: 'doctor failed',
    path: '/doctor',
    h1: 'Doctor',
    failed: '/api/v1/doctor/findings',
    primaries: 1,
  },
];

for (const width of WIDTHS) {
  for (const s of STATES) {
    test(`${s.name} at ${width} px: h1, pane, one primary, layout, axe`, async ({
      page,
    }) => {
      const mock = jobsAndDoctor(signedIn());
      if (s.empty) mock.on('GET', s.empty[0], s.empty[1]);
      if (s.failed)
        mock.on(
          'GET',
          s.failed,
          failure(500, 'internal', 'The controller could not answer.'),
        );
      await mock.install(page);
      await page.setViewportSize({ width, height: 900 });
      await page.goto(s.path);
      await expect(page.getByRole('heading', { level: 1, name: s.h1 })).toBeVisible();
      if (s.pane) await expect(page.getByRole('region', { name: s.pane })).toBeVisible();
      if (s.failed) await expect(page.getByRole('alert')).toBeVisible();
      await page.waitForLoadState('networkidle');
      // One primary per screen (UX-4); the header's, never a second one in an empty or failed state.
      expect(await page.locator('main .ds-button[data-kind="primary"]').count()).toBe(
        s.primaries,
      );
      await expectDomLayout(page, `${s.name} ${width}`, { spaceVar: '--ds-space-' });
      const axe = await new AxeBuilder({ page }).analyze();
      expect(
        axe.violations.filter((v) => v.impact === 'serious' || v.impact === 'critical'),
      ).toEqual([]);
    });
  }
}

test('runners: Select all N and Clear sit in the toolbar inside the pane (UX-12)', async ({
  page,
}) => {
  const mock = jobsAndDoctor(signedIn());
  await mock.install(page);
  await page.goto('/setup/runners');
  const pane = page.getByRole('region', { name: 'Runners' });
  await expect(pane.getByRole('button', { name: /^Select all \d+$/ })).toBeVisible();
  await expect(
    page
      .getByRole('region', { name: 'Machine build-1' })
      .getByRole('columnheader', { name: 'Type' }),
  ).toBeVisible();
  await pane.getByRole('button', { name: /^Select all \d+$/ }).click();
  await pane.getByRole('button', { name: 'Clear' }).click();
  await expect(pane.getByRole('button', { name: 'Clear' })).toBeDisabled();
});
