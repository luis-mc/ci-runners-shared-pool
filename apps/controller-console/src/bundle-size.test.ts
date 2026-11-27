// R6.12: the console's first JavaScript (the entry chunk and everything it imports at start) stays within the gzip
// budget. It builds the app in memory, so it needs no dist folder. The number is in apps/controller-console/perf-budgets.json.
import { readFileSync } from 'node:fs';
import { dirname, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';
import { gzipSync } from 'node:zlib';
import { build } from 'vite';
import type { Rollup } from 'vite';
import { expect, test } from 'vitest';

const here = dirname(fileURLToPath(import.meta.url));
const budgets = JSON.parse(
  readFileSync(resolve(here, '../perf-budgets.json'), 'utf8'),
) as { firstJavaScriptGzipBytes: number };

test('the first JavaScript is within the gzip budget', async () => {
  // Vitest sets NODE_ENV=test, which makes vite bundle React's development build. Measure what ships.
  const before = process.env.NODE_ENV;
  process.env.NODE_ENV = 'production';
  let out: Rollup.RollupOutput | Rollup.RollupOutput[];
  try {
    out = (await build({
      root: resolve(here, '..'),
      mode: 'production',
      logLevel: 'silent',
      build: { write: false },
    })) as Rollup.RollupOutput | Rollup.RollupOutput[];
  } finally {
    if (before === undefined) delete process.env.NODE_ENV;
    else process.env.NODE_ENV = before;
  }
  const chunks = (Array.isArray(out) ? out.flatMap((o) => o.output) : out.output).filter(
    (o): o is Rollup.OutputChunk => o.type === 'chunk',
  );
  const byName = new Map(chunks.map((c) => [c.fileName, c]));
  const first = new Set<string>();
  const visit = (name: string) => {
    if (first.has(name)) return;
    first.add(name);
    for (const dep of byName.get(name)?.imports ?? []) visit(dep);
  };
  for (const c of chunks) if (c.isEntry) visit(c.fileName);
  expect(first.size).toBeGreaterThan(0);
  const total = [...first].reduce(
    (sum, name) => sum + gzipSync(byName.get(name)?.code ?? '').length,
    0,
  );
  console.log(
    `bundle: first JavaScript ${total} bytes gzipped (budget ${budgets.firstJavaScriptGzipBytes})`,
  );
  expect(total).toBeLessThanOrEqual(budgets.firstJavaScriptGzipBytes);
}, 10_000); // measured 2.5 s: the one bundle is minified in one piece
