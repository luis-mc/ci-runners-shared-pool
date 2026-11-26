import { defineConfig } from '@playwright/test';

export default defineConfig({
  testDir: '.',
  testMatch: 'shoot.spec.ts',
  workers: 1,
  use: { baseURL: 'http://127.0.0.1:8562', colorScheme: 'dark' },
});
