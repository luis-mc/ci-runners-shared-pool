// A small repository with a `docs/` tree for the docs tests: two public pages, one page that is never published, and the
// generated files the build reads (llms.txt, docs-map.json).
import { mkdirSync, mkdtempSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { dirname, join } from 'node:path';

export function makeRepo(extra: Record<string, string> = {}): string {
  const repo = mkdtempSync(join(tmpdir(), 'crsp-docs-'));
  const files: Record<string, string> = {
    'docs/overview/product.md':
      '---\ntitle: Product\nversion: 1.0.0\nlast_verified: 2026-11-27\n---\n# Product scope\n\nThe pool runs jobs on your own machines. See the [guide](../guides/install.md#check-it) and the [work note](../work/note.md).\n\n```mermaid\nflowchart TD\n  a[Start] --> b[Done]\n```\n\n| a | b |\n|---|---|\n| 1 | 2 |\n\n<!-- hidden -->\n',
    'docs/guides/install.md': '# Install\n\nSteps.\n\n## Check it\n\nUse the checksum.\n',
    'docs/work/note.md': '# Work note\n\nNever published.\n',
    'docs/llms.txt':
      '# Documentation map\n\n## guides\n\n- [Install and check a download](docs/guides/install.md)\n\n## overview\n\n- [Product scope](docs/overview/product.md)\n',
    'docs/docs-map.json': JSON.stringify({
      schema: 1,
      map: { 'apps/x/**': ['docs/overview/product.md'] },
    }),
    ...extra,
  };
  for (const [path, text] of Object.entries(files)) {
    mkdirSync(dirname(join(repo, path)), { recursive: true });
    writeFileSync(join(repo, path), text);
  }
  return repo;
}
