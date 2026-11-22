// The words of the Registry screens (RES20, approved by the owner, D18/D41). Every one is listed in
// docs/design-system/next-version/content.md, "Registry words".
import { formatDate, formatPast, formatSize, type StatusKind } from '@crsp/ui';
import type { Schemas } from '../../api/api';

export type Summary = Schemas['RegistryImageSummary'];
export type Recommendation = Schemas['RegistryRecommendation'];

/** Decimal, as `formatSize` writes sizes, so a budget and the size shown beside it agree. */
export const GB = 1e9;

/** Lists show the first 6 of the 12 hex of the key (spec REG10). */
export const shortKey = (key: string): string => key.slice(0, 6);

/** "Python and uv, variant of rust-wasm": what a list calls an image when the profile's name is not enough. */
export const imageName = (i: Pick<Summary, 'profile' | 'state' | 'branch'>): string =>
  `${i.profile}${i.state === 'variant' && i.branch ? `, variant of ${i.branch}` : ''}`;

export function stateOf(
  i: {
    state: Summary['state'];
    update_found?: boolean | undefined;
    branch?: string | undefined;
  },
  extra: { unusedDays?: number | undefined; inUse?: boolean | undefined } = {},
): { kind: StatusKind; word: string } {
  if (extra.inUse && (i.state === 'current' || i.state === 'variant'))
    return { kind: 'ok', word: 'in use' };
  if (i.update_found && i.state === 'current')
    return { kind: 'waiting', word: 'newer one found' };
  switch (i.state) {
    case 'current':
      return extra.unusedDays !== undefined
        ? { kind: 'waiting', word: `unused ${extra.unusedDays} days` }
        : { kind: 'ok', word: 'current' };
    case 'superseded':
      return { kind: 'off', word: 'superseded' };
    case 'variant':
      return { kind: 'off', word: `variant of ${i.branch ?? 'a branch'}` };
    case 'staged':
      return { kind: 'waiting', word: 'staged' };
    case 'refused':
      return { kind: 'failed', word: 'refused' };
    case 'deleted':
      return { kind: 'off', word: 'deleted' };
  }
}

export const lastPulled = (i: Pick<Summary, 'last_pull_at'>, now: number): string =>
  i.last_pull_at ? formatPast(i.last_pull_at, now) : 'never';

/** One line of a recommendation, in plain words (UX-73). The server names the image in `r.image`. */
export function recommendationTitle(r: Recommendation): string {
  const img = r.image;
  const name = img ? `${imageName(img)} ${shortKey(img.key)}` : 'An image';
  switch (r.rule) {
    case 'replace':
      return `${name} is a newer version of an image the registry holds`;
    case 'superseded':
      return `${name} was replaced and its days to go back are over`;
    case 'variant':
      return `${name} was made for a branch that is gone`;
    case 'unused':
      return `${name} is unused`;
    case 'over_budget':
      return `The ${r.kind ?? 'registry'} images are over their budget`;
    case 'dangling':
      return 'Layers no image uses';
    case 'profile_removed':
      return `${name} belongs to a profile that was removed`;
  }
}

/** Where the Open button of a recommendation goes. */
export function recommendationTo(r: Recommendation): string {
  if (r.rule === 'over_budget' || r.rule === 'dangling')
    return '/setup/images/registry/budgets';
  return r.image_id ? `/setup/images/registry/${r.image_id}` : '/setup/images?tab=registry';
}

export const decidedBy = (r: Recommendation): string =>
  r.decider === 'owner'
    ? 'your yes'
    : `crsp ${r.due_at ? `on ${formatDate(r.due_at)}` : ''}`.trim();

const NEXT_LABEL: Record<Recommendation['rule'], string> = {
  replace: 'Review the newer image',
  unused: 'Review the unused image',
  over_budget: 'Review the budget',
  variant: 'Open the variant',
  dangling: 'Open the cleanup',
  superseded: 'Open the image',
  profile_removed: 'Open the image',
};

/** The next step of the Registry tab: the first recommendation that waits for the owner (UX-4). */
export function registryNext(
  recs: readonly Recommendation[],
): { label: string; reason: string; to: string } | undefined {
  const first = recs.find((r) => r.decider === 'owner');
  return first
    ? {
        label: NEXT_LABEL[first.rule],
        reason: recommendationTitle(first),
        to: recommendationTo(first),
      }
    : undefined;
}

export const sizeOf = (bytes: number): string => formatSize(bytes);
