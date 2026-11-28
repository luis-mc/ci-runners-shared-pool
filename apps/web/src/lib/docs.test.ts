// Docs publishing (website T6, T7, T9; R5.15 to R5.21).
import { readFileSync } from 'node:fs';
import { join } from 'node:path';
import { describe, expect, it } from 'vitest';
import { makeRepo } from '../testing/docsrepo.ts';
import { loadDocs, pagePath, parseLlms, summaryOf } from './docs.ts';

describe('docs publishing', () => {
  it('publishes the public folders and omits work, project, archive, review and specs (T6)', () => {
    const r = loadDocs(makeRepo());
    expect(r.problems).toEqual([]);
    expect(r.pages.map((p) => p.path).sort()).toEqual([
      '/docs/guides/install',
      '/docs/overview/product',
    ]);
    for (const folder of ['work', 'project', 'archive', 'review', 'specs'])
      expect(r.pages.some((p) => p.source.startsWith(`docs/${folder}/`))).toBe(false);
  });

  it('rewrites relative links, draws diagrams, and shows version and date (T7, R5.18, R5.21)', () => {
    const page = loadDocs(makeRepo()).pages.find(
      (p) => p.path === '/docs/overview/product',
    )!;
    expect(page.html).toContain('href="/docs/guides/install#check-it"');
    expect(page.html).toContain(
      'https://github.com/luis-mc/ci-runners-shared-pool/blob/main/docs/work/note.md',
    );
    expect(page.html).toContain('<svg');
    expect(page.html).not.toMatch(/<style|sstyle="|googleapis|hidden/);
    expect(page.html).toContain('ds-prose__table');
    expect(page.version).toBe('1.0.0');
    expect(page.lastVerified).toBe('2026-11-27');
    expect(page.editUrl).toContain('/edit/main/docs/overview/product.md');
    expect(page.title).toBe('Product scope');
  });

  it('fails on a broken link, a broken anchor and an unlisted public page (T7, R5.20)', () => {
    const broken = loadDocs(
      makeRepo({
        'docs/guides/install.md':
          '# Install\n\n[gone](nope.md) and [anchor](../overview/product.md#missing)\n',
      }),
    );
    expect(broken.problems.join('\n')).toMatch(/broken link nope\.md/);
    expect(broken.problems.join('\n')).toMatch(/broken anchor #missing/);
    const unlisted = loadDocs(makeRepo({ 'docs/guides/extra.md': '# Extra\n' }));
    expect(unlisted.problems.join('\n')).toMatch(
      /extra\.md is in a public folder but not listed/,
    );
  });

  it('keeps llms.txt as it is (T9, R5.16)', () => {
    const repo = makeRepo();
    expect(loadDocs(repo).llms).toBe(
      readFileSync(join(repo, 'docs', 'llms.txt'), 'utf8'),
    );
  });

  it('maps a source file to a site path', () => {
    expect(pagePath('docs/overview/README.md')).toBe('/docs/overview');
    expect(pagePath('docs/features/x/spec.md')).toBe('/docs/features/x');
    expect(pagePath('docs/design-system/v4/ux-rules.md')).toBe(
      '/docs/design-system/v4/ux-rules',
    );
    expect(parseLlms('- [A b](docs/a/b.md)\n- not a line\n')).toEqual([
      { title: 'A b', source: 'docs/a/b.md' },
    ]);
  });

  it('takes the "In one paragraph" of a spec as its summary', () => {
    expect(
      summaryOf(
        '# T\n\n## 1. In one paragraph\n\nThe **pool** runs [jobs](x.md).\n\n## 2. Why\n\nNo.',
      ),
    ).toBe('The pool runs jobs.');
  });

  it('publishes the real docs of this repository without a problem', () => {
    const r = loadDocs(join(import.meta.dirname, '..', '..', '..', '..'));
    expect(r.problems).toEqual([]);
    expect(r.pages.length).toBeGreaterThan(40);
    expect(r.pages.some((p) => p.path === '/docs/guides/install')).toBe(true);
  });
});
