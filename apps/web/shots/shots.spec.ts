// Takes the console screenshots of the public site from a seeded demo Controller (website R5.4) and refuses a shot that
// shows a real machine name, login, repository or token (R6.8). The data is the console e2e suite's mocked API.
import { createServer } from 'node:http';
import { existsSync, mkdirSync, readFileSync, writeFileSync } from 'node:fs';
import { hostname, userInfo } from 'node:os';
import { extname, join, resolve } from 'node:path';
import { expect, test } from '@playwright/test';
import { signedIn } from '../../../e2e/tests/web-console/fixtures';
import { findPrivate } from '../src/lib/shotcheck';

const dist = resolve(import.meta.dirname, '../../controller-console/dist');
const out = resolve(import.meta.dirname, '../content/shots');
const TYPES: Record<string, string> = {
  '.html': 'text/html',
  '.js': 'text/javascript',
  '.css': 'text/css',
  '.png': 'image/png',
};

const SCREENS = [
  {
    name: 'console-overview',
    path: '/',
    alt: 'The console overview of a demo pool: its machine, the jobs that run and wait, and what needs attention.',
  },
  {
    name: 'console-machines',
    path: '/pool/machines',
    alt: 'The Pool machines page of a demo pool: one machine card with its load, health and runners.',
  },
  {
    name: 'console-repositories',
    path: '/setup/pool',
    alt: 'The Setup page of a demo pool, with its list of repositories and the choice of which ones use the pool.',
  },
] as const;

test('takes the console screenshots', async ({ page }) => {
  expect(existsSync(join(dist, 'index.html')), 'build the console first').toBe(true);
  const server = createServer((req, res) => {
    const path = (req.url ?? '/').split('?')[0]!;
    const file = join(dist, path === '/' ? 'index.html' : path);
    const known = existsSync(file) && extname(file) !== '';
    const target = known ? file : join(dist, 'index.html');
    res.writeHead(200, { 'content-type': TYPES[extname(target)] ?? 'text/html' });
    res.end(readFileSync(target));
  });
  await new Promise<void>((ok) => server.listen(0, '127.0.0.1', ok));
  const port = (server.address() as { port: number }).port;
  mkdirSync(out, { recursive: true });
  const who = {
    hostname: hostname(),
    logins: [userInfo().username, process.env['GITHUB_LOGIN'] ?? 'luis-mc'],
    owners: ['luis-mc'],
  };
  const manifest: { name: string; alt: string; width: number; height: number }[] = [];
  try {
    await signedIn().install(page);
    for (const s of SCREENS) {
      await page.goto(`http://127.0.0.1:${port}${s.path}`);
      await page.waitForLoadState('networkidle');
      await page.waitForTimeout(500);
      const text = await page.evaluate(() => document.body.innerText);
      expect(findPrivate(text, who), `${s.name} shows private data`).toEqual([]);
      const png = await page.screenshot();
      writeFileSync(join(out, `${s.name}.png`), png);
      manifest.push({ name: s.name, alt: s.alt, width: 1280, height: 800 });
    }
  } finally {
    server.close();
  }
  writeFileSync(join(out, 'shots.json'), `${JSON.stringify(manifest, null, 2)}\n`);
});
