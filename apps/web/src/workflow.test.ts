// T16 (R5.14): the deploy-hook workflow runs when a stable release is published and every night, calls the hook from a
// secret, never from a literal address, and has a dry run that calls nothing.
import { readFileSync } from 'node:fs';
import { join } from 'node:path';
import { describe, expect, it } from 'vitest';

const text = readFileSync(
  join(
    import.meta.dirname,
    '..',
    '..',
    '..',
    '.github',
    'workflows',
    'website-deploy.yml',
  ),
  'utf8',
);

describe('website-deploy workflow (T16)', () => {
  it('runs on a published release, after the Release workflow, nightly and by hand', () => {
    expect(text).toMatch(/^ {2}release:\n {4}types: \[published\]/m);
    expect(text).toMatch(/workflow_run:\n {4}workflows: \[Release\]/);
    expect(text).toMatch(/schedule:\n {4}- cron: '\d+ \d+ \* \* \*'/);
    expect(text).toContain('workflow_dispatch:');
  });

  it('skips a pre-release and a Release run that did not succeed', () => {
    expect(text).toContain('github.event.release.prerelease == false');
    expect(text).toContain('"$RUN_CONCLUSION" != "success"');
  });

  it('reads the hook from a secret and holds no address', () => {
    expect(text).toContain('secrets.VERCEL_DEPLOY_HOOK_URL');
    expect(text).not.toMatch(/https:\/\/api\.vercel\.com\/v1\/integrations\/deploy/);
  });

  it('has a dry run that exits before the call', () => {
    const dry = text.indexOf('"$DRY_RUN" = "true"');
    const call = text.indexOf('curl ');
    expect(dry).toBeGreaterThan(0);
    expect(call).toBeGreaterThan(dry);
    expect(text.slice(dry, call)).toContain('exit 0');
  });
});
