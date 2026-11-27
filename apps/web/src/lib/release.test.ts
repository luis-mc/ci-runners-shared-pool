import { describe, expect, it } from 'vitest';
import { fixtureGh } from '../testing/fixture.ts';
import {
  ARCHS,
  FORMATS,
  OSES,
  buildRelease,
  fetchRelease,
  parseAssetName,
  parseSums,
  type GhRelease,
} from './release.ts';

describe('release data (website R5.7, R5.8, R5.10)', () => {
  it('has every system, CPU and format and no agent download (T1)', () => {
    const { gh, sums } = fixtureGh();
    const r = buildRelease(gh, sums);
    const want = OSES.flatMap((os) =>
      ARCHS.flatMap((a) => FORMATS[os].map((f) => `${os}/${a}/${f}`)),
    );
    expect(r.assets.map((a) => `${a.os}/${a.arch}/${a.format}`).sort()).toEqual(
      want.sort(),
    );
    expect(r.assets.some((a) => /agent/i.test(a.name))).toBe(false);
    expect(r.version).toBe('1.2.3');
    expect(r.date).toBe('2026-11-27');
    expect(r.signed).toBe(false);
    expect(r.sboms.map((s) => s.name)).toEqual([
      'crsp-sbom-go.spdx.json',
      'crsp-sbom-web.spdx.json',
    ]);
  });

  it('fails when an asset is missing (T2)', () => {
    const { gh, sums } = fixtureGh();
    gh.assets = gh.assets.filter((a) => a.name !== 'Crsp-1.2.3-win-x64.msi');
    expect(() => buildRelease(gh, sums)).toThrow(/missing asset Crsp-1.2.3-win-x64.msi/);
  });

  it('fails when a checksum is missing (T2)', () => {
    const { gh, sums } = fixtureGh();
    const cut = sums
      .split('\n')
      .filter((l) => !l.endsWith('linux-x64.deb'))
      .join('\n');
    expect(() => buildRelease(gh, cut)).toThrow(
      /missing checksum for Crsp-1.2.3-linux-x64.deb/,
    );
  });

  it('fails on a pre-release or a missing SBOM', () => {
    const a = fixtureGh();
    a.gh.prerelease = true;
    expect(() => buildRelease(a.gh, a.sums)).toThrow(/pre-release/);
    const b = fixtureGh();
    b.gh.assets = b.gh.assets.filter((x) => x.name !== 'crsp-sbom-go.spdx.json');
    expect(() => buildRelease(b.gh, b.sums)).toThrow(/crsp-sbom-go/);
  });

  it('never lists the agent installers the release also holds (R5.7)', () => {
    const c = fixtureGh();
    c.gh.assets.push({
      name: 'CrspAgent-1.2.3-win-x64.zip',
      size: 1,
      browser_download_url: 'https://x/y',
    });
    const r = buildRelease(c.gh, c.sums);
    expect(r.assets.some((a) => /agent/i.test(a.name))).toBe(false);
  });

  it('lists the earlier stable releases newest first and never edge (R5.13)', () => {
    const { gh, sums } = fixtureGh();
    const rel = (tag: string, at: string, over: Partial<GhRelease> = {}): GhRelease => ({
      tag_name: tag,
      html_url: `https://github.com/luis-mc/ci-runners-shared-pool/releases/tag/${tag}`,
      published_at: at,
      assets: [],
      ...over,
    });
    const feed = [
      gh,
      rel('edge', '2026-11-28T00:00:00Z', { prerelease: true }),
      rel('v1.2.1', '2026-11-20T00:00:00Z'),
      rel('v1.2.2', '2026-11-25T00:00:00Z'),
      rel('v1.3.0', '2026-11-29T00:00:00Z', { draft: true }),
    ];
    expect(buildRelease(gh, sums, feed).older.map((o) => o.version)).toEqual([
      '1.2.2',
      '1.2.1',
    ]);
  });

  it('links every asset to a GitHub release asset URL and ships no binary (T4)', () => {
    const r = buildRelease(fixtureGh().gh, fixtureGh().sums);
    for (const a of r.assets) {
      expect(a.url).toMatch(
        /^https:\/\/github\.com\/luis-mc\/ci-runners-shared-pool\/releases\/download\/v1\.2\.3\//,
      );
    }
  });

  it('parses names and checksum lines', () => {
    expect(parseAssetName('Crsp-0.1.28-win-arm64.msi')).toEqual({
      version: '0.1.28',
      os: 'windows',
      arch: 'arm64',
      format: 'msi',
    });
    expect(parseAssetName('Crsp-0.1.28-linux-x64.tar.gz')?.format).toBe('tar.gz');
    expect(parseAssetName('SHA256SUMS')).toBeNull();
    expect(parseSums(`${'A'.repeat(64)}  f.msi\nnoise`).get('f.msi')).toBe(
      'a'.repeat(64),
    );
  });

  it('reads the latest release and its checksums from GitHub', async () => {
    const { gh, sums } = fixtureGh();
    const calls: string[] = [];
    const fake = ((url: string) => {
      calls.push(url);
      return Promise.resolve(
        url.includes('/releases?')
          ? new Response(JSON.stringify([gh]))
          : url.includes('api.github.com')
            ? new Response(JSON.stringify(gh))
            : new Response(sums),
      );
    }) as unknown as typeof fetch;
    const r = await fetchRelease(fake, undefined);
    expect(r.tag).toBe('v1.2.3');
    expect(calls[0]).toContain('/releases/latest');
    expect(calls.some((c) => c.includes('/releases?per_page=100'))).toBe(true);
    const bad = (() =>
      Promise.resolve(new Response('no', { status: 403 }))) as unknown as typeof fetch;
    await expect(fetchRelease(bad, undefined)).rejects.toThrow(/403/);
  });
});
