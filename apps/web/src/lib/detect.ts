// Which system the visitor is on, read from the user agent (website R5.9). Without script every system is listed.
import type { Os } from './release.ts';

export const osCardId = (os: Os): string => `os-${os}`;

export function detectOs(userAgent: string): Os | null {
  // Phones and tablets run an installer of none of the three, so they get the full list.
  if (/android|iphone|ipad|ipod/i.test(userAgent)) return null;
  if (/windows/i.test(userAgent)) return 'windows';
  if (/macintosh|mac os x/i.test(userAgent)) return 'macos';
  if (/linux|x11/i.test(userAgent)) return 'linux';
  return null;
}

/** Moves the visitor's system to the front of the list. Returns the system it found, for tests. */
export function putSystemFirst(
  doc: Pick<Document, 'getElementById'>,
  userAgent: string,
): Os | null {
  const os = detectOs(userAgent);
  if (!os) return null;
  const card = doc.getElementById(osCardId(os));
  const parent = card?.parentElement;
  if (!card || !parent || parent.firstElementChild === card) return os;
  parent.insertBefore(card, parent.firstElementChild);
  return os;
}
