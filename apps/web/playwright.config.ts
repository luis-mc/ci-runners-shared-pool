// The site's own browser tests (website T3, T8, T10 to T12, T15). A separate project from the console's e2e suite at the
// repository root: it needs no Controller, only the built site. `pnpm --filter web e2e` builds the site from a recorded
// release and serves it the way Vercel does (e2e/serve.ts), then runs the specs.
import { defineConfig } from '@playwright/test';

const port = 4173;

export default defineConfig({
  testDir: './e2e',
  testMatch: '**/*.spec.ts',
  reporter: 'line',
  retries: 0,
  forbidOnly: !!process.env['CI'],
  outputDir: 'test-results',
  use: { baseURL: `http://127.0.0.1:${port}` },
  webServer: {
    command: 'node e2e/serve.ts',
    url: `http://127.0.0.1:${port}/`,
    reuseExistingServer: false,
    timeout: 90_000,
    env: { SITE_E2E_PORT: String(port) },
  },
});
