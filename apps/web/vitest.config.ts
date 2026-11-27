import react from '@vitejs/plugin-react';
import { defineConfig } from 'vitest/config';

export default defineConfig({
  plugins: [react()],
  test: {
    name: 'site',
    include: ['src/**/*.test.{ts,tsx}'],
    environment: 'node',
    // STACKSPEC 11.3: a suite that finds no tests fails.
    passWithNoTests: false,
    // STACKSPEC 11.4 G1: a unit test that needs more than 10 s is broken. The build test sets its own limit.
    testTimeout: 10_000,
    hookTimeout: 10_000,
  },
});
