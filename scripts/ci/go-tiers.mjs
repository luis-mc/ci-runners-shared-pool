// Reads `go list ./...` (import paths) on stdin and prints the packages of one tier,
// one per line, as ./relative paths: node scripts/ci/go-tiers.mjs unit|integration|slow|chunks
// `chunks` prints `package count` for each slow package. The tiers are in scripts/ci/go-tiers.json. A package that is in no list is in the unit
// tier (the strictest), so a new package gets the 10 s limit until someone measures it.
// A listed package that no longer exists is an error: the file must not rot.
import { readFileSync } from 'node:fs';
import { dirname, join } from 'node:path';
import { fileURLToPath } from 'node:url';
import { createInterface } from 'node:readline';

const tier = process.argv[2];
if (!['unit', 'integration', 'slow', 'chunks'].includes(tier)) {
  console.error('usage: go-tiers.mjs unit|integration|slow|chunks < go-list-output');
  process.exit(2);
}
const tiers = JSON.parse(
  readFileSync(join(dirname(fileURLToPath(import.meta.url)), 'go-tiers.json'), 'utf8'),
);
const listed = new Map();
for (const name of ['integration', 'slow']) {
  for (const p of tiers[name] ?? []) {
    if (listed.has(p)) {
      console.error(`go-tiers: ${p} is in two tiers`);
      process.exit(2);
    }
    listed.set(p, name);
  }
}

const all = [];
const rl = createInterface({ input: process.stdin, crlfDelay: Infinity });
for await (const raw of rl) {
  const line = raw.trim();
  if (line) all.push(line);
}
// The module path is the `module` line of go.mod (run from the repository root).
const mod = /^module\s+(\S+)/m.exec(readFileSync('go.mod', 'utf8'))?.[1] ?? '';
const rel = (p) => (p === mod ? '.' : `./${p.slice(mod.length + 1)}`);
const rels = all.map(rel);
for (const p of listed.keys()) {
  if (!rels.includes(p)) {
    console.error(
      `go-tiers: ${p} is in go-tiers.json but is not a package of this module`,
    );
    process.exit(2);
  }
}
for (const p of rels) {
  const t = listed.get(p) ?? 'unit';
  if (tier === 'chunks') {
    if (t === 'slow') console.log(`${p} ${tiers.chunks?.[p] ?? 2}`);
  } else if (t === tier) console.log(p);
}
