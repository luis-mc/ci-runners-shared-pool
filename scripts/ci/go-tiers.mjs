// Reads `go list ./...` (import paths) on stdin and prints the packages of one tier,
// one per line, as ./relative paths: node scripts/ci/go-tiers.mjs unit|integration|slow|chunks [K/N]
// `chunks` prints `package count index` for each chunk of each slow package.
// CRSP313: with K/N (K from 1) only the share of shard K is printed, so N jobs together run every
// package and every chunk once. The work items are each unit or integration package and each chunk;
// an item weighs its `seconds` in go-tiers.json (a chunk: the package's seconds / chunk count, an
// unlisted package 2 s). Items go longest first to the lightest shard (ties: the lower one). The tiers are in scripts/ci/go-tiers.json. A package that is in no list is in the unit
// tier (the strictest), so a new package gets the 10 s limit until someone measures it.
// A listed package that no longer exists is an error: the file must not rot.
import { readFileSync } from 'node:fs';
import { dirname, join } from 'node:path';
import { fileURLToPath } from 'node:url';
import { createInterface } from 'node:readline';

const tier = process.argv[2];
if (!['unit', 'integration', 'slow', 'chunks'].includes(tier)) {
  console.error(
    'usage: go-tiers.mjs unit|integration|slow|chunks [K/N] < go-list-output',
  );
  process.exit(2);
}
const shardArg = process.argv[3];
let shardK = 0;
let shardN = 0;
if (shardArg) {
  const m = /^(\d+)\/(\d+)$/.exec(shardArg);
  shardK = m ? Number(m[1]) : 0;
  shardN = m ? Number(m[2]) : 0;
  if (!m || shardK < 1 || shardK > shardN) {
    console.error(`go-tiers: bad shard ${shardArg}, want K/N with 1 <= K <= N`);
    process.exit(2);
  }
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
// The work items, in a stable order: [tier, package, chunk count, chunk index, seconds].
const items = [];
for (const p of rels) {
  const t = listed.get(p) ?? 'unit';
  const secs = tiers.seconds?.[p] ?? 2;
  if (t === 'slow') {
    const n = tiers.chunks?.[p] ?? 2;
    for (let k = 0; k < n; k++) items.push([t, p, n, k, secs / n]);
  } else {
    items.push([t, p, 0, 0, secs]);
  }
}
const mine = new Set(items.map((_, i) => i));
if (shardN) {
  const total = new Array(shardN).fill(0);
  const order = items.map((_, i) => i).sort((a, b) => items[b][4] - items[a][4] || a - b);
  mine.clear();
  for (const i of order) {
    let best = 0;
    for (let k = 1; k < shardN; k++) if (total[k] < total[best]) best = k;
    total[best] += items[i][4];
    if (best === shardK - 1) mine.add(i);
  }
}
const printed = new Set();
items.forEach(([t, p, n, k], i) => {
  if (!mine.has(i)) return;
  if (tier === 'chunks') {
    if (t === 'slow') console.log(`${p} ${n} ${k}`);
  } else if (t === tier && !printed.has(p)) {
    printed.add(p);
    console.log(p);
  }
});
