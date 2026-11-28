// CRSP347 (operations R5.132): import the agent installers of a release that was carried over by hand, against the
// mocked API. Settings, Updates offers the wizard; Files takes several files; Review lists them in the order they are
// sent (SHA256SUMS first); the files go as octet-stream; a refused file is named; a viewer cannot start the import.
// DOM layout at 360, 393 and 1300 px (UX-10, UX-15) and the wizard rules (UX-5, UX-30, UX-54).
import { expect, test, type Page } from '@playwright/test';
import {
  domLayoutSource,
  judgeLayout,
  type LayoutProblem,
} from '../../../scripts/design/dom-layout';
import { failure, signedIn, viewer } from './fixtures';

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

const NAME = 'CrspAgent-1.4.0-linux-x64.tar.gz';
const SHA = 'a'.repeat(64);
const installer = (stored: boolean) => ({
  name: NAME,
  os: 'linux',
  arch: 'amd64',
  format: 'tar.gz',
  sha256: SHA,
  stored,
});
const list = (stored: boolean, have = true) => ({
  version: '1.4.0',
  have_sums: have,
  from_release: false,
  installers: [installer(stored)],
});
const files = () => [
  { name: NAME, mimeType: 'application/gzip', buffer: Buffer.from('binary') },
  {
    name: 'SHA256SUMS',
    mimeType: 'text/plain',
    buffer: Buffer.from(`${SHA}  ${NAME}\n`),
  },
];
const PATH = '/settings/updates/agent-installers/import';

test('Settings, Updates offers the import and the wizard opens', async ({ page }) => {
  const mock = signedIn().on('GET', '/api/v1/agent-installers', list(false, false));
  await mock.install(page);
  await page.goto('/settings/updates');
  await page.getByRole('button', { name: 'Import agent installers' }).click();
  await expect(
    page.getByRole('group', { name: 'Import agent installers' }),
  ).toBeVisible();
  await expect(page.getByLabel('Release files')).toBeAttached();
  await expect(page.getByRole('button', { name: 'Next' })).toBeDisabled();
  await layoutAtAllWidths(page, 'import agent installers, files');
});

test('files go SHA256SUMS first as octet-stream and the wizard ends at Done', async ({
  page,
}) => {
  const mock = signedIn()
    .on('GET', '/api/v1/agent-installers', list(false, false))
    .on('PUT', '/api/v1/agent-installers/:name', () => list(true));
  await mock.install(page);
  await page.goto(PATH);
  await page.getByLabel('Release files').setInputFiles(files());
  await expect(page.getByText(NAME)).toBeVisible();
  await layoutAtAllWidths(page, 'import agent installers, files chosen');
  await page.getByRole('button', { name: 'Next' }).click();
  await expect(page.getByText(/Nothing has changed yet/)).toBeVisible();
  await layoutAtAllWidths(page, 'import agent installers, review');
  await page.getByRole('button', { name: 'Import 2 files' }).click();
  await expect(
    page.getByText('2 files stored and checked against SHA256SUMS.'),
  ).toBeVisible();
  const puts = mock.calls.filter((c) => c.method === 'PUT');
  expect(puts.map((c) => c.path)).toEqual([
    '/api/v1/agent-installers/SHA256SUMS',
    `/api/v1/agent-installers/${NAME}`,
  ]);
  expect(puts[1]?.body).toBe('binary');
  await layoutAtAllWidths(page, 'import agent installers, done');
});

test('a refused file is named', async ({ page }) => {
  const mock = signedIn()
    .on('GET', '/api/v1/agent-installers', list(false, false))
    .on('PUT', '/api/v1/agent-installers/:name', (_c: unknown, p: { name: string }) =>
      p.name === NAME
        ? failure(400, 'invalid', 'The file does not match its SHA256SUMS line.')
        : list(false),
    );
  await mock.install(page);
  await page.goto(PATH);
  await page.getByLabel('Release files').setInputFiles(files());
  await page.getByRole('button', { name: 'Next' }).click();
  await page.getByRole('button', { name: 'Import 2 files' }).click();
  await expect(page.getByText(/1 of 2 files were not stored/)).toBeVisible();
  await expect(
    page.getByText(`${NAME}: The file does not match its SHA256SUMS line.`),
  ).toBeVisible();
  await layoutAtAllWidths(page, 'import agent installers, failed');
});

test('a viewer sees the import button disabled with the reason', async ({ page }) => {
  const mock = signedIn(viewer).on('GET', '/api/v1/agent-installers', list(false, false));
  await mock.install(page);
  await page.goto(PATH);
  await page.getByLabel('Release files').setInputFiles(files());
  await page.getByRole('button', { name: 'Next' }).click();
  const button = page.getByRole('button', { name: 'Import 2 files' });
  await expect(button).toBeDisabled();
  await expect(button).toHaveAttribute('aria-describedby', /.+/);
});
