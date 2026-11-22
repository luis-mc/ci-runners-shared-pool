// LOOK56: the old addresses are gone, with no redirects. No source file of the console, the shared UI or the e2e
// specs may link to one. The one file that may name them is the table that turns an address the controller sends
// (a Doctor fix) into the address of its screen today.
import { readdirSync, readFileSync, statSync } from 'node:fs';
import { dirname, join, relative, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';
import { describe, expect, it } from 'vitest';

const here = dirname(fileURLToPath(import.meta.url));
const root = resolve(here, '../../..');
const ALLOWED = new Set([
  'apps/web/src/screens/doctor/serverLinks.ts',
  'apps/web/src/screens/doctor/serverLinks.test.ts',
  'apps/web/src/oldPaths.test.ts',
]);

// A string that starts with an old address: /shared-pool, /repositories, /jobs, the old top-level lists, and the old
// children of /pool (Pool itself is the new execution section).
const OLD =
  /['"`](\/shared-pool|\/repositories|\/jobs|\/machines|\/runners|\/images|\/caches|\/pool\/(runners|images|size|machines\/))(?=[/?#'"`])/;

function files(dir: string): string[] {
  const out: string[] = [];
  for (const name of readdirSync(dir)) {
    if (name === 'node_modules' || name === 'dist' || name.startsWith('.')) continue;
    const full = join(dir, name);
    if (statSync(full).isDirectory()) out.push(...files(full));
    else if (/\.(tsx?|mjs)$/.test(name)) out.push(full);
  }
  return out;
}

describe('old addresses', () => {
  it('no source file links to /shared-pool, /repositories, /jobs or an old Shared Pool address', () => {
    const roots = ['apps/web/src', 'packages/ui/src', 'e2e'].map((d) => join(root, d));
    const hits: string[] = [];
    for (const file of roots.flatMap(files)) {
      const rel = relative(root, file);
      if (ALLOWED.has(rel)) continue;
      readFileSync(file, 'utf8')
        .split('\n')
        .forEach((line, i) => {
          if (OLD.test(line) && !line.includes('/api/'))
            hits.push(`${rel}:${i + 1}: ${line.trim()}`);
        });
    }
    expect(hits).toEqual([]);
  });
});
