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

  it('fails on a pre-release, a missing SBOM or an agent asset', () => {
    const a = fixtureGh();
    a.gh.prerelease = true;
    expect(() => buildRelease(a.gh, a.sums)).toThrow(/pre-release/);
    const b = fixtureGh();
    b.gh.assets = b.gh.assets.filter((x) => x.name !== 'crsp-sbom-go.spdx.json');
    expect(() => buildRelease(b.gh, b.sums)).toThrow(/crsp-sbom-go/);
    const c = fixtureGh();
    c.gh.assets.push({
      name: 'Crsp-agent-1.2.3.msi',
      size: 1,
      browser_download_url: 'https://x/y',
    });
    expect(() => buildRelease(c.gh, c.sums)).toThrow(/agent asset/);
  });

  it('skips the Controller-fetched agent packages and offers none of them (R5.7)', () => {
    const f = fixtureGh();
    for (const n of [
      'CrspAgent-1.2.3-win-x64.zip',
      'CrspAgent-1.2.3-linux-arm64.tar.gz',
      'CrspAgent-1.2.3-macos-arm64.tar.gz',
    ]) {
      f.gh.assets.push({ name: n, size: 1, browser_download_url: 'https://x/y' });
    }
    const r = buildRelease(f.gh, f.sums);
    expect(r.assets.some((a) => /agent/i.test(a.name))).toBe(false);
    const g = fixtureGh();
    g.gh.assets.push({
      name: 'CrspAgent-9.9.9-win-x64.zip',
      size: 1,
      browser_download_url: 'https://x/y',
    });
    expect(() => buildRelease(g.gh, g.sums)).toThrow(/agent asset/);
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
        url.includes('api.github.com')
          ? new Response(JSON.stringify(gh))
          : new Response(sums),
      );
    }) as unknown as typeof fetch;
    const r = await fetchRelease(fake, undefined);
    expect(r.tag).toBe('v1.2.3');
    expect(calls[0]).toContain('/releases/latest');
    const bad = (() =>
      Promise.resolve(new Response('no', { status: 403 }))) as unknown as typeof fetch;
    await expect(fetchRelease(bad, undefined)).rejects.toThrow(/403/);
  });
});
