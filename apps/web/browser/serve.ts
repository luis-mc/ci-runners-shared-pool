// Builds the site from the recorded release and this repository's docs into node_modules/.site-e2e, then serves it (the browser
// tests' web server). The release is the test fixture, so the run needs no network.
import { rmSync } from 'node:fs';
import { join, resolve } from 'node:path';
import { buildSite, loadSiteData } from '../scripts/build.ts';
import { fixtureRelease } from '../src/testing/fixture.ts';
import { startServer } from './static-server.ts';

const root = resolve(import.meta.dirname, '..');
const dist = join(root, 'node_modules', '.site-e2e');

// React renders its development build unless told otherwise; build what ships.
process.env['NODE_ENV'] = 'production';
rmSync(dist, { recursive: true, force: true });
const { data, llms } = loadSiteData(resolve(root, '..', '..'), fixtureRelease());
await buildSite(dist, data, llms);
const port = Number(process.env['SITE_E2E_PORT'] ?? 4173);
await startServer(root, dist, port);
console.log(`site served on ${port}`);
