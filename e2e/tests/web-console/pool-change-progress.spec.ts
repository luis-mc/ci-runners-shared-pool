import { mkdirSync } from 'node:fs';
import { expect, test, type Page } from '@playwright/test';
import { judgeLayout, measureDomLayout } from '../../../scripts/design/dom-layout';
import { repository, signedIn } from './fixtures';
import { frames } from './ux-guards';

// CRSP405 (UX-10, UX-13, UX-51, UX-88): Setup > Pool setup > turning the pool off. The page reads the plan while the apply
// request is open, so the card's bar and the main bar fill step by step; the repository card clears the bar above it and its
// long name stays on one line at 360, 393 and 1300 px. CRSP405_SHOTS=<folder> keeps a picture of each state.
const LONG = 'luis-mc/shared-product-framework';
const OTHER = 'luis-mc/another-repository-with-a-long-name';

const step = (id: string, repo: number, action: string, text: string, state: string) => ({
  id,
  action,
  text,
  target: repo === 7 ? LONG : OTHER,
  repository_id: repo,
  needs_yes: false,
  state,
});
const frame = (planState: string, states: string[], error?: string) => ({
  id: 'p1',
  area: 'repositories',
  state: planState,
  created_at: '2026-11-28T10:00:00Z',
  created_by: 'octo',
  steps: [
    step('s1', 7, 'pool_off', `Turn the pool off for ${LONG}`, states[0] ?? 'planned'),
    step(
      's2',
      7,
      'sync_runners',
      `Adjust the pool runners for ${LONG}`,
      states[1] ?? 'planned',
    ),
    step('s3', 8, 'pool_off', `Turn the pool off for ${OTHER}`, states[2] ?? 'planned'),
    {
      ...step(
        's4',
        8,
        'sync_runners',
        `Adjust the pool runners for ${OTHER}`,
        states[3] ?? 'planned',
      ),
      ...(error ? { error } : {}),
    },
  ],
});

const FRAMES = {
  planned: frame('planned', []),
  start: frame('applying', ['running']),
  half: frame('applying', ['done', 'running']),
  second: frame('applying', ['done', 'done', 'running']),
  failed: frame(
    'failed',
    ['done', 'done', 'done', 'failed'],
    'GitHub refused: a permission is missing',
  ),
  done: frame('done', ['done', 'done', 'done', 'done']),
};

const repos = () => [
  repository({
    id: 7,
    full_name: LONG,
    owner: 'luis-mc',
    name: 'shared-product-framework',
    pool_on: true,
  }),
  repository({
    id: 8,
    full_name: OTHER,
    owner: 'luis-mc',
    name: 'another-repository-with-a-long-name',
    pool_on: true,
  }),
];

async function shot(page: Page, name: string, width: number) {
  const dir = process.env.CRSP405_SHOTS;
  if (!dir) return;
  mkdirSync(dir, { recursive: true });
  await page.screenshot({ path: `${dir}/${name}-${width}.png`, fullPage: true });
}

async function judge(page: Page, name: string) {
  const problems = await measureDomLayout(page, {
    known: [],
    spaceVar: '--ds-space-',
    sectionGapVar: '--ds-space-6',
    focusVars: {
      width: '--ds-focus-width',
      offset: '--ds-focus-offset',
      glow: '--ds-glow-focus',
    },
    ready: () => frames(page),
  });
  expect(judgeLayout(name, problems, []).unknown).toEqual([]);
}

/** Each card's title is one line and sits clear of the bar above it. */
async function titlesClear(page: Page) {
  const titles = page.locator('.ds-card-grid .ds-pane__title');
  await expect(titles).toHaveCount(2);
  const bar = await page
    .locator('.ds-wizard__body [role="progressbar"]')
    .first()
    .boundingBox();
  for (let i = 0; i < 2; i += 1) {
    const box = (await titles.nth(i).boundingBox())!;
    expect(box.height, `title ${i} is one line`).toBeLessThan(34);
    if (bar)
      expect(box.y, `title ${i} clears the bar`).toBeGreaterThanOrEqual(
        bar.y + bar.height + 8,
      );
  }
}

for (const size of [
  { width: 1300, height: 900 },
  { width: 393, height: 852 },
  { width: 360, height: 780 },
]) {
  test(`pool off: the bars fill step by step at ${size.width} px`, async ({ page }) => {
    await page.setViewportSize(size);
    let current: unknown = FRAMES.planned;
    let release: () => void = () => {};
    const gate = new Promise<void>((r) => (release = r));
    const mock = signedIn()
      .on('GET', '/api/v1/github/repositories', {
        items: repos(),
        stale: { stale: false },
      })
      .on('POST', '/api/v1/repository-plans', FRAMES.planned)
      .on('POST', '/api/v1/repository-plans/:id/apply', async () => {
        await gate;
        return current;
      })
      .on('GET', '/api/v1/repository-plans/:id', () => current);
    await mock.install(page);
    await page.goto('/setup/pool/change?to=off&repo=7&repo=8');
    await expect(page.getByText('These will stop using the pool')).toBeVisible();
    await shot(page, 'review', size.width);
    await page.getByRole('button', { name: /^Turn off 2 repositories/ }).click();
    await expect(page.locator('[data-primary][aria-busy="true"]')).toBeDisabled();
    await expect(page.getByRole('button', { name: 'Stop' })).toBeVisible();

    const cardBar = (name: string) =>
      page.getByRole('progressbar', { name: new RegExp(`^Turning off ${name}:`) });

    current = FRAMES.start;
    await expect(cardBar(LONG)).toHaveAttribute('aria-valuenow', '0', {
      timeout: 10_000,
    });
    await expect(page.getByText(`Turn the pool off for ${LONG}`).first()).toBeVisible();
    // Only the repository whose turn has not started says waiting.
    await expect(page.getByText('waiting', { exact: true })).toHaveCount(1);
    await titlesClear(page);
    await judge(page, `pool change, started ${size.width}`);
    await shot(page, 'started', size.width);

    current = FRAMES.half;
    await expect(cardBar(LONG)).toHaveAttribute('aria-valuenow', '1', {
      timeout: 10_000,
    });
    await expect(cardBar(LONG)).toHaveAttribute('aria-valuemax', '2');
    await expect(
      page.getByText(`Adjust the pool runners for ${LONG}`).first(),
    ).toBeVisible();
    await titlesClear(page);
    await judge(page, `pool change, half ${size.width}`);
    await shot(page, 'halfway', size.width);

    // The page closes and opens again: the run goes on, and the step in hand is shown, never lost.
    current = FRAMES.second;
    await page.reload();
    await expect(cardBar(OTHER)).toHaveAttribute('aria-valuenow', '0', {
      timeout: 10_000,
    });
    await expect(cardBar(LONG)).toHaveAttribute('aria-valuenow', '2');
    await expect(page.getByRole('button', { name: 'Stop' })).toBeVisible();
    await titlesClear(page);
    await shot(page, 'reloaded', size.width);

    current = FRAMES.done;
    release();
    await expect(page.getByText('2 repositories no longer use the pool.')).toBeVisible({
      timeout: 10_000,
    });
    await judge(page, `pool change, done ${size.width}`);
    await shot(page, 'done', size.width);
  });

  test(`pool off: a failed step is named with its error at ${size.width} px`, async ({
    page,
  }) => {
    await page.setViewportSize(size);
    const mock = signedIn()
      .on('GET', '/api/v1/github/repositories', {
        items: repos(),
        stale: { stale: false },
      })
      .on('GET', '/api/v1/repository-plans/:id', FRAMES.failed);
    await mock.install(page);
    await page.goto('/setup/pool/change?to=off&repo=7&repo=8&plan=p1');
    await expect(
      page.getByText('Nothing more was changed. GitHub refused: a permission is missing'),
    ).toBeVisible();
    await judge(page, `pool change, failed ${size.width}`);
    await shot(page, 'failed', size.width);
  });
}
