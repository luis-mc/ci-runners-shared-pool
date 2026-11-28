// CRSP400 (runners R5.178 to R5.181, web-console R5.178): name the pool at the first setup and rename it from Setup,
// against the mocked API. DOM layout at 360, 393 and 1300 px (UX-10, UX-15) in each state of the wizard.
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

const step = (id: string, text: string, state = 'pending', error?: string) => ({
  id,
  kind: id.split(':')[0] ?? id,
  text,
  state,
  ...(error ? { error } : {}),
});
const A = 'Add the label crsp-lab to runner box-1';
const S = 'Store the new name crsp-lab';
const rename = (state: string, steps: unknown[]) => ({
  id: 'pr1',
  old: 'crsp-home',
  new: 'crsp-lab',
  state,
  steps,
  counts: { runners: 1, registered: 1, ephemeral: 0, repositories: 2, scale_set: false },
  created_at: '2026-11-28T10:00:00Z',
  updated_at: '2026-11-28T10:00:00Z',
});
const info = { name: 'crsp-home', source: 'default', env_override: false };
const PATH = '/setup/pool/rename';

test('first setup asks for the pool name with crsp-home filled in', async ({ page }) => {
  const mock = signedIn()
    .on('GET', '/api/v1/setup', {
      has_app: true,
      has_owner: true,
      setup_link_redeemed: true,
      steps: { connect_github: 'done', first_machine: 'todo', first_repository: 'todo' },
    })
    .on('GET', '/api/v1/pool/name', info);
  await mock.install(page);
  await page.goto('/setup?wizard=1');
  await expect(page.getByRole('textbox', { name: 'Pool name' })).toHaveValue('crsp-home');
  await layoutAtAllWidths(page, 'pool name, first setup name');
});

test('rename: name, review, done', async ({ page }) => {
  const steps = (s: string) => [step('add_label:r1', A, s), step('store_name', S, s)];
  const mock = signedIn()
    .on('GET', '/api/v1/pool/name', info)
    .on('POST', '/api/v1/pool/renames', rename('planned', steps('pending')))
    .on('POST', '/api/v1/pool/renames/:id/apply', rename('applying', steps('pending')))
    .on('GET', '/api/v1/pool/renames/:id', rename('done', steps('done')));
  await mock.install(page);
  await page.goto(PATH);
  await layoutAtAllWidths(page, 'pool rename, name');
  await page.getByRole('textbox', { name: 'Pool name' }).fill('crsp-lab');
  await page.getByRole('button', { name: 'Next' }).click();
  await expect(page.getByText(/still finds a runner until the last step/)).toBeVisible();
  expect(mock.count('POST', '/api/v1/pool/renames/pr1/apply')).toBe(0);
  await layoutAtAllWidths(page, 'pool rename, review');
  await page.getByRole('button', { name: 'Rename the pool' }).click();
  await expect(page.getByText('The pool is called crsp-lab.')).toBeVisible();
  await layoutAtAllWidths(page, 'pool rename, done');
});

test('rename: running shows Stop; stopped and failed show what is left', async ({
  page,
}) => {
  const running = rename('applying', [
    step('add_label:r1', A, 'running'),
    step('store_name', S),
  ]);
  const mock = signedIn()
    .on('GET', '/api/v1/pool/name', info)
    .on('POST', '/api/v1/pool/renames', rename('planned', running.steps))
    .on('POST', '/api/v1/pool/renames/:id/apply', running)
    .on('GET', '/api/v1/pool/renames/:id', running);
  await mock.install(page);
  await page.goto(PATH);
  await page.getByRole('textbox', { name: 'Pool name' }).fill('crsp-lab');
  await page.getByRole('button', { name: 'Next' }).click();
  await page.getByRole('button', { name: 'Rename the pool' }).click();
  await expect(page.getByRole('button', { name: 'Stop' })).toBeVisible();
  await layoutAtAllWidths(page, 'pool rename, running');

  const stopped = signedIn()
    .on('GET', '/api/v1/pool/name', info)
    .on(
      'GET',
      '/api/v1/pool/renames/:id',
      rename('stopped', [
        step('add_label:r1', A, 'done'),
        step('store_name', S, 'skipped'),
      ]),
    );
  await stopped.install(page);
  await page.goto(`${PATH}?rename=pr1`);
  await expect(page.getByText(/1 step was done and 1 is left/)).toBeVisible();
  await expect(page.getByRole('button', { name: 'Run again' })).toBeVisible();
  await layoutAtAllWidths(page, 'pool rename, stopped');

  const failed = signedIn()
    .on('GET', '/api/v1/pool/name', info)
    .on(
      'GET',
      '/api/v1/pool/renames/:id',
      rename('failed', [
        step('add_label:r1', A, 'done'),
        step('store_name', S, 'failed', 'GitHub could not be reached'),
      ]),
    );
  await failed.install(page);
  await page.goto(`${PATH}?rename=pr1`);
  await expect(page.getByText(/1 step failed/)).toBeVisible();
  await layoutAtAllWidths(page, 'pool rename, failed');
});

test('rename: done after a reload reads the stored outcome', async ({ page }) => {
  const mock = signedIn()
    .on('GET', '/api/v1/pool/name', { ...info, name: 'crsp-lab', source: 'stored' })
    .on(
      'GET',
      '/api/v1/pool/renames/:id',
      rename('done', [step('store_name', S, 'done')]),
    );
  await mock.install(page);
  await page.goto(`${PATH}?rename=pr1`);
  await expect(page.getByText('The pool is called crsp-lab.')).toBeVisible();
  expect(mock.count('POST', '/api/v1/pool/renames')).toBe(0);
  await layoutAtAllWidths(page, 'pool rename, done after reload');
});
