// Release data for the Download page (website R5.7, R5.8, R5.10). One pure function turns the GitHub release and its
// SHA256SUMS into `release.json`; the build fails when the release lacks an installer or a checksum.

export const REPO = 'luis-mc/ci-runners-shared-pool';

export type Os = 'windows' | 'macos' | 'linux';
export type Arch = 'x64' | 'arm64';
export type Format = 'msi' | 'zip' | 'pkg' | 'tar.gz' | 'deb' | 'rpm';

export interface Asset {
  name: string;
  url: string;
  size: number;
  sha256: string;
  os: Os;
  arch: Arch;
  format: Format;
}

export interface ReleaseData {
  version: string;
  tag: string;
  /** The publish date, year-month-day. */
  date: string;
  notesUrl: string;
  checksumsUrl: string;
  sboms: { name: string; url: string }[];
  /** Today the installers are not signed (owner decision D58); the page changes when signing turns on. */
  signed: boolean;
  assets: Asset[];
}

/** The fields of a GitHub release that the site reads. */
export interface GhRelease {
  tag_name: string;
  html_url: string;
  published_at: string;
  prerelease?: boolean;
  assets: { name: string; size: number; browser_download_url: string }[];
}

/** The formats the release workflow builds per system (R5.7). */
export const FORMATS: Record<Os, readonly Format[]> = {
  windows: ['msi', 'zip'],
  macos: ['pkg', 'tar.gz'],
  linux: ['deb', 'rpm', 'tar.gz'],
};
export const ARCHS: readonly Arch[] = ['x64', 'arm64'];
export const OSES: readonly Os[] = ['windows', 'macos', 'linux'];
export const SBOM_NAMES = ['crsp-sbom-go.spdx.json', 'crsp-sbom-web.spdx.json'] as const;

const OS_WORD: Record<string, Os> = { win: 'windows', macos: 'macos', linux: 'linux' };
const NAME =
  /^Crsp-(\d+\.\d+\.\d+)-(win|macos|linux)-(x64|arm64)\.(msi|zip|pkg|tar\.gz|deb|rpm)$/;

/** `<sha256>  <file name>` lines (the output of sha256sum). */
export function parseSums(text: string): Map<string, string> {
  const out = new Map<string, string>();
  for (const line of text.split(/\r?\n/)) {
    const m = /^([0-9a-f]{64})\s+\*?(\S+)$/i.exec(line.trim());
    if (m) out.set(m[2]!, m[1]!.toLowerCase());
  }
  return out;
}

export function parseAssetName(
  name: string,
): { version: string; os: Os; arch: Arch; format: Format } | null {
  const m = NAME.exec(name);
  if (!m) return null;
  return {
    version: m[1]!,
    os: OS_WORD[m[2]!]!,
    arch: m[3] as Arch,
    format: m[4] as Format,
  };
}

/** Throws with every problem at once, so the failed build says what to fix. */
export function buildRelease(gh: GhRelease, sums: string): ReleaseData {
  const problems: string[] = [];
  if (gh.prerelease)
    problems.push('the release is a pre-release; the site lists stable releases only');
  const version = gh.tag_name.replace(/^v/, '');
  const checksums = parseSums(sums);
  const byName = new Map(gh.assets.map((a) => [a.name, a]));

  const assets: Asset[] = [];
  for (const os of OSES) {
    for (const arch of ARCHS) {
      for (const format of FORMATS[os]) {
        const word = os === 'windows' ? 'win' : os;
        const name = `Crsp-${version}-${word}-${arch}.${format}`;
        const a = byName.get(name);
        if (!a) {
          problems.push(`missing asset ${name}`);
          continue;
        }
        const sha256 = checksums.get(name);
        if (!sha256) {
          problems.push(`missing checksum for ${name}`);
          continue;
        }
        assets.push({
          name,
          url: a.browser_download_url,
          size: a.size,
          sha256,
          os,
          arch,
          format,
        });
      }
    }
  }
  // R5.7: no agent download is offered. An asset named for an agent is a mistake in the release.
  for (const a of gh.assets) {
    if (/agent/i.test(a.name)) problems.push(`unexpected agent asset ${a.name}`);
  }

  const sumsAsset = byName.get('SHA256SUMS');
  if (!sumsAsset) problems.push('missing SHA256SUMS');
  const sboms: { name: string; url: string }[] = [];
  for (const name of SBOM_NAMES) {
    const a = byName.get(name);
    if (a) sboms.push({ name, url: a.browser_download_url });
    else problems.push(`missing asset ${name}`);
  }
  if (problems.length > 0) {
    throw new Error(
      `Release ${gh.tag_name} cannot be published on the site:\n- ${problems.join('\n- ')}`,
    );
  }
  return {
    version,
    tag: gh.tag_name,
    date: gh.published_at.slice(0, 10),
    notesUrl: gh.html_url,
    checksumsUrl: sumsAsset!.browser_download_url,
    sboms,
    signed: false,
    assets,
  };
}

/** Reads the latest stable release and its checksums from GitHub (the build step, R5.8). */
export async function fetchRelease(
  fetchFn: typeof fetch = fetch,
  token: string | undefined = process.env['GITHUB_TOKEN'],
): Promise<ReleaseData> {
  const agent = 'crsp-website-build';
  const headers: Record<string, string> = {
    accept: 'application/vnd.github+json',
    'user-agent': agent,
  };
  if (token) headers['authorization'] = `Bearer ${token}`;
  const res = await fetchFn(`https://api.github.com/repos/${REPO}/releases/latest`, {
    headers,
  });
  if (!res.ok) throw new Error(`GitHub releases answered ${res.status}`);
  const gh = (await res.json()) as GhRelease;
  const sumsAsset = gh.assets.find((a) => a.name === 'SHA256SUMS');
  if (!sumsAsset) throw new Error(`Release ${gh.tag_name} has no SHA256SUMS`);
  const sumsRes = await fetchFn(sumsAsset.browser_download_url, {
    headers: { 'user-agent': agent },
  });
  if (!sumsRes.ok) throw new Error(`SHA256SUMS answered ${sumsRes.status}`);
  return buildRelease(gh, await sumsRes.text());
}
