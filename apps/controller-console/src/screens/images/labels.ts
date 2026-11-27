// The words of the images and caches screen and of its activity strip entry (content.md).
import type { Schemas } from '../../api/api';

const KIND: Record<Schemas['CacheKind'], string> = {
  tool_cache: 'Tool cache',
  rust_cache: 'Rust build cache',
  actions_cache: 'Actions cache',
  git_mirror: 'Git mirror',
  registry_mirror: 'Registry mirror',
  dependency_cache: 'Dependency cache',
};

export const cacheKindLabel = (k: Schemas['CacheKind']): string => KIND[k];

/** The name of one cache: its kind, and the runner it belongs to. */
export function cacheName(c: Pick<Schemas['Cache'], 'kind' | 'runner_name'>): string {
  return c.runner_name
    ? `${cacheKindLabel(c.kind)} of ${c.runner_name}`
    : cacheKindLabel(c.kind);
}

/** The activity strip line of a wiping cache (UX-34): what it wipes and what it waits for. */
export function wipingLabel(c: Schemas['Cache']): string {
  const waits = c.waiting_for ?? [];
  return waits.length > 0
    ? `Wiping ${cacheName(c)}, waiting for ${waits.join(', ')}`
    : `Wiping ${cacheName(c)}`;
}

const OS_NAME: Record<Schemas['OS'], string> = {
  linux: 'Linux',
  windows: 'Windows',
  macos: 'macOS',
};

/** The heading of an image: its machine and the name of its OS. */
export const imageTitle = (machine: string, i: Pick<Schemas['Image'], 'os'>): string =>
  `${machine}, ${OS_NAME[i.os]}`;

/** The layers a repository needs that the given choice lacks, and the repositories that need them. */
export function missingOf(
  i: Schemas['Image'],
  chosen?: readonly string[],
): { layers: Schemas['ImageLayer'][]; repos: string[] } {
  const on = new Set(chosen ?? i.layers.filter((l) => l.enabled).map((l) => l.id));
  const layers = i.layers.filter((l) => l.needed_by.count > 0 && !on.has(l.id));
  const repos = [...new Set(layers.flatMap((l) => l.needed_by.repositories))];
  return { layers, repos };
}

/** The repositories that use an image: those that need a layer or set extra packages. */
export const usedBy = (i: Schemas['Image']): string[] => [
  ...new Set([
    ...i.layers.flatMap((l) => l.needed_by.repositories),
    ...i.extra_packages.map((e) => e.repository),
  ]),
];
