// Copies apps/controller-console/dist into apps/controller/internal/webui/dist so go:embed picks it up.
// Keeps the committed placeholder.html; removes everything else first.
import { cpSync, existsSync, readdirSync, rmSync } from 'node:fs';
import { dirname, join, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';

const root = resolve(dirname(fileURLToPath(import.meta.url)), '..', '..');
const from = join(root, 'apps', 'controller-console', 'dist');
const to = join(root, 'apps', 'controller', 'internal', 'webui', 'dist');

if (!existsSync(join(from, 'index.html'))) {
  console.error(`embed-web: ${from} has no index.html; run the web build first`);
  process.exit(1);
}
for (const name of readdirSync(to)) {
  if (name !== 'placeholder.html')
    rmSync(join(to, name), { recursive: true, force: true });
}
cpSync(from, to, { recursive: true });
console.log(`embed-web: copied ${from} -> ${to}`);
