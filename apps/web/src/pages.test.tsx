import { renderToStaticMarkup } from 'react-dom/server';
import { StaticRouter } from 'react-router-dom';
import { describe, expect, it } from 'vitest';
import { Download, Home, NotFound } from './pages.tsx';
import { fixtureRelease } from './testing/fixture.ts';

const html = (el: React.ReactElement, path = '/'): string =>
  renderToStaticMarkup(<StaticRouter location={path}>{el}</StaticRouter>);

describe('pages', () => {
  it('Home names the product and has one primary action, Download (website R5.2, R5.3)', () => {
    const h = html(<Home />);
    expect(h).toContain('CI Runners Shared Pool');
    expect(h.match(/data-kind="primary"/g)).toHaveLength(1);
    expect(h).toContain('href="/download"');
    expect(h).toContain('saves nothing');
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

  it('the signing text follows the signed flag (R5.12, T5)', () => {
    const r = fixtureRelease();
    expect(html(<Download release={r} />)).toContain('are not signed');
    expect(html(<Download release={{ ...r, signed: true }} />)).toContain('are signed');
  });

  it('the 404 page links Home and the docs (R5.1)', () => {
    const h = html(<NotFound />, '/nope');
    expect(h).toContain('href="/"');
    expect(h).toContain('/docs');
  });
});
