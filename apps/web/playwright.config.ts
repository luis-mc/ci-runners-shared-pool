// The site's own browser tests (website T3, T8, T10 to T12, T15). A separate project from the console's e2e suite at the
// repository root: it needs no Controller, only the built site. `pnpm --filter web e2e` builds the site from a recorded
// release and serves it the way Vercel does (browser/serve.ts), then runs the specs.
import { defineConfig } from '@playwright/test';

const port = 4173;

export default defineConfig({
  testDir: './browser',
  testMatch: '**/*.spec.ts',
  reporter: 'line',
  retries: 0,
  // The LCP spec throttles the CPU, so a crowded machine would fail it for no reason: at most two workers.
  workers: 2,
  forbidOnly: !!process.env['CI'],
  outputDir: 'test-results',
  use: { baseURL: `http://127.0.0.1:${port}` },
  webServer: {
    command: 'node browser/serve.ts',
    url: `http://127.0.0.1:${port}/`,
    reuseExistingServer: false,
    timeout: 90_000,
    env: { SITE_E2E_PORT: String(port) },
  },
});
