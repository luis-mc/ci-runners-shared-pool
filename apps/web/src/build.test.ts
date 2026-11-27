// The build check: builds the site from the fixture release and reads what it wrote (website T4, T5, T12, T14, R6.1, R6.2, R6.5, R6.6).
import { mkdtempSync, readdirSync, readFileSync, rmSync, statSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { gzipSync } from 'node:zlib';
import { afterAll, beforeAll, describe, expect, it } from 'vitest';
import { buildSite } from '../scripts/build.ts';
import { PAGES, canonical } from './lib/pages.ts';
import { fixtureRelease } from './testing/fixture.ts';

let out = '';
const read = (f: string): string => readFileSync(join(out, f), 'utf8');
const walk = (dir: string): string[] =>
  readdirSync(dir).flatMap((n) =>
    statSync(join(dir, n)).isDirectory() ? walk(join(dir, n)) : [join(dir, n)],
  );

beforeAll(async () => {
  out = mkdtempSync(join(tmpdir(), 'crsp-web-test-'));
  // Vitest sets NODE_ENV=test, which makes React render its development build. Build what ships.
  const before = process.env['NODE_ENV'];
  process.env['NODE_ENV'] = 'production';
  try {
    await buildSite(out, fixtureRelease());
  } finally {
    if (before === undefined) delete process.env['NODE_ENV'];
    else process.env['NODE_ENV'] = before;
  }
}, 120_000);

afterAll(() => rmSync(out, { recursive: true, force: true }));

describe('the built site', () => {
  it('writes every page, the sitemap, robots.txt and release.json', () => {
    for (const p of PAGES) expect(read(p.file).length).toBeGreaterThan(500);
    expect(read('sitemap.xml')).toContain(canonical('/download'));
    expect(read('sitemap.xml')).not.toContain('/404');
    expect(read('robots.txt')).toContain('Sitemap: https://crsp.luismc.net/sitemap.xml');
    expect((JSON.parse(read('release.json')) as { version: string }).version).toBe(
      '1.2.3',
    );
  });

  it('gives each page one h1, a title, a description and a canonical link (T14)', () => {
    for (const p of PAGES) {
      const html = read(p.file);
      expect(html.match(/<h1[ >]/g), p.path).toHaveLength(1);
      expect(html).toContain(`<title>${p.title}</title>`);
      expect(html).toContain('<meta name="description"');
      if (p.indexed)
        expect(html).toContain(`<link rel="canonical" href="${canonical(p.path)}">`);
    }
  });

  it('lists every system with its checksum and no script needed (R5.9, R6.2)', () => {
    const html = read('download/index.html');
    for (const id of ['os-windows', 'os-macos', 'os-linux'])
      expect(html).toContain(`id="${id}"`);
    expect(html).toContain('Crsp-1.2.3-win-x64.msi');
    expect(html).toContain('SHA-256 ');
    expect(html).toContain('The installers are not signed');
    expect(html).not.toMatch(/agent/i);
  });

  it('links only to this site and to GitHub, and sets no cookie (R5.10, R6.5)', () => {
    for (const p of PAGES) {
      const html = read(p.file);
      for (const m of html.matchAll(/(?:href|src)="(https?:\/\/[^"]+)"/g)) {
        const url = m[1]!;
        expect(
          url.startsWith('https://github.com/') ||
            url.startsWith('https://crsp.luismc.net/'),
          url,
        ).toBe(true);
      }
      expect(html).not.toMatch(/document\.cookie|<form|<iframe/);
    }
  });

  it('has no inline script and no style attribute, so the strict policy holds (R6.6)', () => {
    for (const p of PAGES) {
      const html = read(p.file);
      expect(html).not.toMatch(/<script(?![^>]*(?:src=|type="application\/ld\+json"))/);
      expect(html, p.path).not.toMatch(/ style="/);
      expect(html).not.toMatch(/<style/);
    }
  });

  it('ships no binary and stays under the JavaScript budget (R5.10, R6.1)', () => {
    const files = walk(out);
    expect(files.filter((f) => /\.(msi|pkg|deb|rpm|zip|gz|exe)$/.test(f))).toEqual([]);
    const js = files.filter((f) => f.endsWith('.js') && !f.endsWith('theme-init.js'));
    const gz = js.reduce((n, f) => n + gzipSync(readFileSync(f)).length, 0);
    expect(gz).toBeLessThan(50 * 1024);
  });
}, 20_000);
