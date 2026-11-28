// The shot script (website R5.4): `pnpm --filter web shots` builds the console, serves it with the console e2e suite's
// mocked controller API (a seeded demo Controller) and writes the screenshots under content/shots. Not part of the gate.
import { defineConfig } from '@playwright/test';

export default defineConfig({
  testDir: './shots',
  reporter: 'line',
  workers: 1,
  timeout: 60_000,
  use: { viewport: { width: 1280, height: 800 }, colorScheme: 'dark' },
});
