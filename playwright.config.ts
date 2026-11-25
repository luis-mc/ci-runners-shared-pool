import { existsSync, readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { defineConfig } from '@playwright/test';

// STACKSPEC 11.2: a spec that fails intermittently is moved to a committed
// quarantine list with an owner, a date and a reason, and is fixed. A
// quarantined spec still runs and reports but cannot fail the build.
//
// e2e/quarantine.json is that list:
//   [{ "spec": "e2e/tests/example.spec.ts", "owner": "name",
//      "since": "YYYY-MM-DD", "reason": "why it is flaky" }]
// `spec` is a path from the repository root. Run Playwright from the root
// (`pnpm exec playwright test`), which is how CI and `pnpm e2e` run it.
//
//   default                 every spec EXCEPT the quarantined ones (the gate)
//   E2E_QUARANTINE=only     ONLY the quarantined ones (CI's non-blocking step)
//
// The specs run against the built binary (bin/crsp), which serves the
// embedded web console over HTTPS only (web-console R5.1) with a certificate of its
// own CA, which the browser does not trust: the specs ignore certificate errors
// (tests only). Set E2E_BINARY to use another path.

interface Quarantined {
  spec: string;
  owner: string;
  since: string;
  reason: string;
}

function loadQuarantine(): Quarantined[] {
  const file = resolve('e2e/quarantine.json');
  if (!existsSync(file)) return [];
  const list: unknown = JSON.parse(readFileSync(file, 'utf8'));
  if (!Array.isArray(list)) throw new Error(`${file} must be a JSON list`);
  for (const entry of list as Partial<Quarantined>[]) {
    // An entry with no owner, date or reason is a spec hidden from the gate.
    if (!entry.spec || !entry.owner || !entry.since || !entry.reason) {
      throw new Error(
        `${file}: every entry needs spec, owner, since and reason (11.2): ${JSON.stringify(entry)}`,
      );
    }
  }
  return list as Quarantined[];
}

// Absolute, forward-slashed: Playwright globs match on absolute file paths.
const quarantined = loadQuarantine().map((q) => resolve(q.spec).split('\\').join('/'));
const only = process.env.E2E_QUARANTINE === 'only';

const port = 18080;
const exe = process.platform === 'win32' ? '.exe' : '';
// CRSP242: cmd.exe reads `bin/crsp.exe` as the switch `/crsp.exe`, so the command uses an
// absolute path, quoted. Linux runs the same absolute path.
const binary = resolve(process.env.E2E_BINARY ?? `bin/crsp${exe}`);

export default defineConfig({
  testDir: './e2e/tests',
  // CRSP209: `line` stays for the log; the JSON file holds every test's time (CI uploads it
  // as the artifact ci-run-times and scripts/ci/e2e_shards.py reads it to weigh the files).
  // PLAYWRIGHT_JSON_OUTPUT_NAME names the file, so two runs in one job do not overwrite each other.
  reporter: [
    ['line'],
    [
      'json',
      {
        outputFile:
          process.env.PLAYWRIGHT_JSON_OUTPUT_NAME ?? 'test-results/playwright-times.json',
      },
    ],
  ],
  outputDir: 'test-results/artifacts',
  // 11.3: a flaky spec is quarantined and fixed, never retried until green.
  // Retries stay 0 for the gating specs; do not raise this to make CI pass.
  retries: 0,
  // CRSP201: 4 workers in CI (the runner has 4 cores); locally Playwright picks half the cores.
  ...(process.env.CI ? { workers: 4 } : {}),
  forbidOnly: !!process.env.CI,
  use: { baseURL: `http://127.0.0.1:${port}` },
  webServer: {
    command: `"${binary}" controller`,
    url: `http://127.0.0.1:${port}/healthz`,
    reuseExistingServer: false,
    timeout: 30_000,
    env: {
      CRSP_LISTEN_ADDR: `127.0.0.1:${port}`,
      CRSP_DATA_DIR: resolve('test-results', 'e2e-data'),
      CRSP_LOG_LEVEL: 'warn',
    },
  },
  ...(only
    ? // Nothing quarantined means nothing to run, not "run everything".
      { testMatch: quarantined.length > 0 ? quarantined : ['**/__no-quarantined-spec__'] }
    : { testIgnore: quarantined }),
});
