import { renderToStaticMarkup } from 'react-dom/server';
import { StaticRouter } from 'react-router-dom';
import { describe, expect, it } from 'vitest';
import {
  Doc,
  Docs,
  Download,
  Home,
  NotFound,
  Product,
  Security,
  brief,
} from './pages.tsx';
import type { DocPage } from './lib/docs.ts';
import { fixtureRelease, fixtureSite } from './testing/fixture.ts';

const html = (el: React.ReactElement, path = '/'): string =>
  renderToStaticMarkup(<StaticRouter location={path}>{el}</StaticRouter>);

const doc = (path: string, title: string, folder: string): DocPage => ({
  path,
  source: `docs${path.slice(5)}.md`,
  folder,
  title,
  version: '1.0.0',
  lastVerified: '2026-11-27',
  html: '<h2 id="a">A</h2><p>Text</p>',
  headings: ['A'],
  text: 'A Text',
  summary: 'The first sentence is here. The second is here. A third one follows.',
  editUrl: 'https://github.com/luis-mc/ci-runners-shared-pool/edit/main/docs/x.md',
});

describe('pages', () => {
  it('Home names the product and has one primary action, Download (website R5.2, R5.3)', () => {
    const h = html(<Home data={fixtureSite()} />);
    expect(h).toContain('CI Runners Shared Pool');
    expect(h.match(/data-kind="primary"/g)).toHaveLength(1);
    expect(h).toContain('href="/download"');
    expect(h).toContain('saves nothing');
  });

  it('Home labels the savings example and shows the study numbers (R5.5)', () => {
    const h = html(<Home data={fixtureSite()} />);
    expect(h).toContain('Example, not a measurement');
    expect(h).toContain('25 Nov 2026');
    expect(h).toContain('$120');
    expect(h).toContain('vendor claims');
  });

  it('every screenshot has alt text and a stated size (R5.4, R6.4)', () => {
    const h = html(<Home data={fixtureSite()} />);
    for (const m of h.matchAll(/<img [^>]*>/g)) {
      expect(m[0]).toMatch(/ alt="[^"]+"/);
      expect(m[0]).toMatch(/ width="\d+"/);
      expect(m[0]).toMatch(/ height="\d+"/);
    }
  });

  it('Product shows the feature summaries with links to their docs and one primary action', () => {
    const data = fixtureSite([
      doc('/docs/features/machines', 'Machines', 'features/machines'),
    ]);
    const h = html(<Product data={data} />, '/product');
    expect(h).toContain('href="/docs/features/machines"');
    expect(h).toContain('The first sentence is here. The second is here.');
    expect(h.match(/data-kind="primary"/g)).toHaveLength(1);
  });

  it('Security has one primary action, Report privately, and follows the signed flag (R5.12)', () => {
    const r = fixtureRelease();
    const h = html(<Security release={r} />, '/security');
    expect(h.match(/data-kind="primary"/g)).toHaveLength(1);
    expect(h).toContain('security/advisories/new');
    expect(h).toContain('not signed');
    expect(html(<Security release={{ ...r, signed: true }} />)).not.toContain(
      'not signed',
    );
  });

  it('Download lists each installer with its size and SHA-256 and links the checks (R5.7, R5.11)', () => {
    const r = fixtureRelease();
    const h = html(<Download release={r} />, '/download');
    for (const a of r.assets) {
      expect(h).toContain(`href="${a.url}"`);
      expect(h).toContain(a.sha256);
    }
    expect(h).toContain(`href="${r.checksumsUrl}"`);
    for (const s of r.sboms) expect(h).toContain(`href="${s.url}"`);
    expect(h).toContain(`href="${r.notesUrl}"`);
  });

  it('Download lists the older versions (R5.13)', () => {
    const h = html(<Download release={fixtureRelease()} />, '/download');
    expect(h).toContain('Older versions');
    expect(h).toContain('releases/tag/v1.2.2');
  });

  it('the signing text follows the signed flag (R5.12, T5)', () => {
    const r = fixtureRelease();
    expect(html(<Download release={r} />)).toContain('are not signed');
    expect(html(<Download release={{ ...r, signed: true }} />)).toContain('are signed');
  });

  it('Docs lists every page in groups and the search box starts hidden', () => {
    const docs = [
      doc('/docs/guides/install', 'Install', 'guides'),
      doc('/docs/design-system/v4/ux-rules', 'UX rules', 'design-system/v4'),
    ];
    const h = html(<Docs docs={docs} />, '/docs');
    expect(h).toContain('href="/docs/guides/install"');
    expect(h).toContain('data-doc-path="/docs/design-system/v4/ux-rules"');
    expect(h).toContain('hidden=""');
    expect(h).toContain('href="/llms.txt"');
  });

  it('a doc page shows its version, date, an Edit on GitHub link and the text (R5.21)', () => {
    const h = html(
      <Doc page={doc('/docs/guides/install', 'Install', 'guides')} />,
      '/docs/guides/install',
    );
    expect(h).toContain('Version 1.0.0, last verified 2026-11-27');
    expect(h).toContain('Edit on GitHub');
    expect(h).toContain('<p>Text</p>');
    expect(h.match(/<h1[ >]/g)).toHaveLength(1);
  });

  it('the 404 page links Home and the docs (R5.1)', () => {
    const h = html(<NotFound />, '/nope');
    expect(h).toContain('href="/"');
    expect(h).toContain('/docs');
  });

  it('brief keeps whole sentences', () => {
    expect(brief('One. Two. Three.', 10)).toBe('One. Two.');
    expect(brief('A long sentence with no end')).toBe('A long sentence with no end');
  });
});
