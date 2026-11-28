// The build check: builds the site from the fixture release and this repository's docs, and reads what it wrote
// (website T4, T5, T6, T9, T12, T14, R6.1, R6.2, R6.5, R6.6). The site is built once, in beforeAll.
import {
  existsSync,
  mkdtempSync,
  readdirSync,
  readFileSync,
  rmSync,
  statSync,
} from 'node:fs';
import { tmpdir } from 'node:os';
import { join, resolve } from 'node:path';
import { gzipSync } from 'node:zlib';
import { afterAll, beforeAll, describe, expect, it } from 'vitest';
import { buildSite, loadSiteData } from '../scripts/build.ts';
import { canonical, pagesFor, type PageMeta, type SiteData } from './lib/pages.ts';
import { fixtureRelease } from './testing/fixture.ts';

let out = '';
let pages: PageMeta[] = [];
let data: SiteData;
const repo = resolve(import.meta.dirname, '..', '..', '..');
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
    const loaded = loadSiteData(repo, fixtureRelease());
    data = loaded.data;
    pages = pagesFor(data);
    await buildSite(out, data, loaded.llms);
  } finally {
    if (before === undefined) delete process.env['NODE_ENV'];
    else process.env['NODE_ENV'] = before;
  }
}, 10_000);

afterAll(() => rmSync(out, { recursive: true, force: true }));

describe('the built site', () => {
  it('writes every page, the sitemap, robots.txt, llms.txt, the search index and release.json', () => {
    for (const p of pages) expect(read(p.file).length).toBeGreaterThan(500);
    for (const path of ['/', '/product', '/download', '/docs', '/security'])
      expect(pages.some((p) => p.path === path)).toBe(true);
    expect(read('sitemap.xml')).toContain(canonical('/download'));
    expect(read('sitemap.xml')).toContain(canonical('/docs/guides/install'));
    expect(read('sitemap.xml')).not.toContain('/404');
    expect(read('robots.txt')).toContain('Sitemap: https://crsp.luismc.net/sitemap.xml');
    expect((JSON.parse(read('release.json')) as { version: string }).version).toBe(
      '1.2.3',
    );
    expect(read('llms.txt')).toBe(readFileSync(join(repo, 'docs', 'llms.txt'), 'utf8'));
    const index = JSON.parse(read('search-index.json')) as { pages: unknown[] };
    expect(index.pages).toHaveLength(data.docs.length);
  });

  it('publishes the docs and none of work, project, archive, review, specs (T6, R5.17)', () => {
    expect(data.docs.length).toBeGreaterThan(40);
    for (const folder of ['work', 'project', 'archive', 'review', 'specs'])
      expect(existsSync(join(out, 'docs', folder))).toBe(false);
    expect(existsSync(join(out, 'docs', 'guides', 'install', 'index.html'))).toBe(true);
  });

  it('gives each page one h1, a title, a description and a canonical link (T14)', () => {
    for (const p of pages) {
      const html = read(p.file);
      expect(html.match(/<h1[ >]/g), p.path).toHaveLength(1);
      expect(html).toContain(`<title>${p.title.replace(/&/g, '&amp;')}</title>`);
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

  it('every internal link goes to a page or file the build wrote (R5.20)', () => {
    const exists = (href: string): boolean => {
      const path = href.split('#')[0]!.split('?')[0]!;
      const rel = path.replace(/^\//, '');
      return (
        path === '/' ||
        [rel, `${rel}.html`, join(rel, 'index.html')].some((f) =>
          existsSync(join(out, f)),
        )
      );
    };
    for (const p of pages) {
      for (const m of read(p.file).matchAll(/href="(\/[^"]*)"/g)) {
        expect(exists(m[1]!), `${p.path} links ${m[1]}`).toBe(true);
      }
    }
  });

  it('links to nothing but this site and GitHub, except in doc text, and sets no cookie (R5.10, R6.5)', () => {
    for (const p of pages) {
      const html = read(p.file);
      const isDoc = p.path.startsWith('/docs/');
      for (const m of html.matchAll(/(?:href|src)="(https?:\/\/[^"]+)"/g)) {
        const url = m[1]!;
        const ok =
          url.startsWith('https://github.com/') ||
          url.startsWith('https://crsp.luismc.net/') ||
          // A link in a doc's own words may go anywhere; it is a link, never a request the page makes.
          (isDoc && html.includes(`<a href="${url}"`));
        expect(ok, `${p.path}: ${url}`).toBe(true);
      }
      expect(html).not.toMatch(/<script[^>]* src="https?:/);
      expect(html).not.toMatch(
        /<link[^>]* href="https?:\/\/(?!crsp\.luismc\.net)[^"]*"[^>]*rel="(?:stylesheet|preload)/,
      );
      expect(html).not.toMatch(/document\.cookie|<form|<iframe/);
    }
  });

  it('has no inline script and no style attribute, so the strict policy holds (R6.6)', () => {
    for (const p of pages) {
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
});
