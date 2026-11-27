import { configDefaults, defineConfig } from 'vitest/config';

/**
 * STACKSPEC 11.3: a suite that finds no tests fails, so passWithNoTests is
 * false everywhere. Projects:
 *   guard  repository-wide guard tests (13.5), run whatever changed
 *   design the design-system guard tests (scripts/design, DOCSPEC 15)
 *   web    the React app (apps/controller-console/vitest.config.ts)
 * Go has its own suites (go test); scripts/ci/go-test.sh holds its zero-test guard.
 */
const nestedCheckouts = ['**/.claude/worktrees/**', '**/.worktrees/**'];

export default defineConfig({
  test: {
    passWithNoTests: false,
    projects: [
      {
        test: {
          name: 'guard',
          include: ['**/*.guard.test.ts'],
          exclude: [...configDefaults.exclude, ...nestedCheckouts],
          environment: 'node',
          passWithNoTests: false,
          testTimeout: 10_000, // measured: slowest guard test 0.4 s
          hookTimeout: 10_000,
        },
      },
      {
        test: {
          name: 'design',
          include: ['scripts/design/*.test.ts'],
          environment: 'node',
          passWithNoTests: false,
          testTimeout: 10_000,
        },
      },
      'apps/controller-console',
      'packages/ui',
    ],
  },
});
