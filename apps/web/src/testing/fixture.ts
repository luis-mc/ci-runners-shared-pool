// A recorded-shape release for tests: every asset the release workflow builds (website R5.7), fake sizes and checksums.
import { createHash } from 'node:crypto';
import {
  ARCHS,
  FORMATS,
  OSES,
  SBOM_NAMES,
  buildRelease,
  type GhRelease,
  type ReleaseData,
} from '../lib/release.ts';

const base = 'https://github.com/luis-mc/ci-runners-shared-pool/releases/download/v1.2.3';

export function fixtureGh(): { gh: GhRelease; sums: string } {
  const names: string[] = [];
  for (const os of OSES) {
    for (const arch of ARCHS) {
      for (const f of FORMATS[os])
        names.push(`Crsp-1.2.3-${os === 'windows' ? 'win' : os}-${arch}.${f}`);
    }
  }
  const sums = names
    .map((n) => `${createHash('sha256').update(n).digest('hex')}  ${n}`)
    .join('\n');
  const all = [...names, ...SBOM_NAMES, 'SHA256SUMS', 'manifest.json'];
  return {
    gh: {
      tag_name: 'v1.2.3',
      html_url: 'https://github.com/luis-mc/ci-runners-shared-pool/releases/tag/v1.2.3',
      published_at: '2026-11-27T05:19:09Z',
      assets: all.map((name, i) => ({
        name,
        size: 1_000_000 + i,
        browser_download_url: `${base}/${name}`,
      })),
    },
    sums,
  };
}

export function fixtureRelease(): ReleaseData {
  const { gh, sums } = fixtureGh();
  return buildRelease(gh, sums);
}
