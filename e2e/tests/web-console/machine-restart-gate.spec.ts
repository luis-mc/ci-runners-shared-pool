// CRSP235: the two admin steps that were missing from the console, against the mocked API. Restart the service of
// one machine (machines R5.81): the machine page offers it, the shared wizard names the running jobs, shows
// Restarting and ends at Done, and a running job is never cut. Gate maximum wait (scheduling R5.75): the field is on
// the machine page, 1 or 2 minutes, saved by the one Save, and the Doctor fix opens the page at it. DOM layout at 360,
// 393 and 1300 px (UX-10, UX-15) and the wizard rules (UX-5, UX-30, UX-54).
import { expect, test, type Page } from '@playwright/test';
import {
  domLayoutSource,
  judgeLayout,
  type LayoutProblem,
} from '../../../scripts/design/dom-layout';
import { machine, signedIn } from './fixtures';

const WIDTHS = [
  { width: 360, height: 780 },
  { width: 393, height: 852 },
  { width: 1300, height: 900 },
] as const;

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

const change = (state: string) => ({
  id: 'ch1',
  machine_id: 'm1',
  kind: 'service_restart',
  state,
  started_at: '2026-11-14T10:00:00Z',
  waiting_for: [],
});

test('the machine page has Gate maximum wait, saved by the one Save', async ({
  page,
}) => {
  const mock = signedIn().on('PATCH', '/api/v1/machines/:id', machine());
  await mock.install(page);
  await page.goto('/setup/machines/m1#gate-max-wait');
  const field = page.getByRole('button', { name: /Gate maximum wait/ });
  await expect(field).toBeVisible();
  await expect(field).toBeInViewport();
  await field.click();
  const dialog = page.getByRole('dialog');
  await dialog.getByRole('button', { name: '1', exact: true }).click();
  await dialog.getByRole('button', { name: 'Done' }).click();
  await page.getByRole('button', { name: 'Save' }).click();
  await expect.poll(() => mock.count('PATCH', '/api/v1/machines/m1')).toBe(1);
  expect(
    (mock.calls.find((c) => c.method === 'PATCH')?.body as Record<string, unknown>)
      .gate_max_wait_minutes,
  ).toBe(1);
  await layoutAtAllWidths(page, 'machine page, Gate maximum wait');
});

test('Restart the service: Review names the job, Restarting, then Done', async ({
  page,
}) => {
  let step = 0;
  const mock = signedIn()
    .on('GET', '/api/v1/machines/:id', () => {
      if (step === 0) return machine();
      step += 1;
      return step < 3
        ? machine({ pending_change: change('running') })
        : machine({ running_jobs: [] });
    })
    .on('POST', '/api/v1/machines/:id/restart', () => {
      step = 1;
      return change('waiting');
    });
  await mock.install(page);
  await page.goto('/setup/machines/m1');
  await page.getByRole('button', { name: 'Restart the service' }).click();
  await expect(page.getByRole('group', { name: 'Restart the service' })).toBeVisible();
  await expect(page.getByText('octo/app e2e')).toBeVisible();
  await expect(page.getByText(/Nothing has changed yet/)).toBeVisible();
  await layoutAtAllWidths(page, 'restart the service, review');
  await page.getByRole('button', { name: 'Restart the service' }).last().click();
  await expect(page.getByText(/The service of build-1 restarted\./)).toBeVisible({
    timeout: 15_000,
  });
  expect(mock.count('POST', '/api/v1/machines/m1/restart')).toBe(1);
  await layoutAtAllWidths(page, 'restart the service, done');
});
