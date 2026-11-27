// The controller names a console address in each Doctor fix (Fix.link). Some of them still use the addresses from
// before LOOK56. The console has no redirects (owner decision), so this one table turns such an address into the
// address of its screen today. An address it does not know is used as it is. This file is the only source file that
// may name an old address (the guard in oldPaths.test.ts allows it).
const OLD_TO_NEW: readonly (readonly [RegExp, string])[] = [
  [/^\/pool\/(machines|runners|images|size)(?=[/?#]|$)/, '/setup/$1'],
  [/^\/(machines|runners|images)(?=[/?#]|$)/, '/setup/$1'],
  [/^\/caches(?=[/?#]|$)/, '/setup/images?tab=caches'],
  [/^\/repositories(?=[/?#]|$)/, '/setup/pool'],
  [/^\/jobs\/run\//, '/pool/runs/'],
  [/^\/jobs(?=[/?#]|$)/, '/pool/jobs'],
];

/** The address of a screen for a link the controller sent. */
export function consoleAddress(link: string): string {
  for (const [from, to] of OLD_TO_NEW) if (from.test(link)) return link.replace(from, to);
  return link;
}
