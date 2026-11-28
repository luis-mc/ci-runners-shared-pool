// The shot guard (website R5.4, R6.8): a console screenshot is taken against a seeded demo Controller, and the shot
// script refuses a page whose visible text shows a real machine name, login, repository or a token.

export interface Identity {
  /** This computer's name. */
  hostname: string;
  /** Logins of the person running the script: the OS user and the GitHub login. */
  logins: readonly string[];
  /** Owners of the real repository, for example `luis-mc`. */
  owners: readonly string[];
}

const TOKENS: [string, RegExp][] = [
  ['a GitHub token', /\b(?:gh[pousr]_[A-Za-z0-9]{20,}|github_pat_[A-Za-z0-9_]{20,})\b/],
  ['a private key', /-----BEGIN [A-Z ]*PRIVATE KEY-----/],
  ['a bearer token', /\bBearer\s+[A-Za-z0-9._-]{20,}\b/i],
  ['a long secret', /\b(?:secret|token|password|key)\s*[:=]\s*[A-Za-z0-9+/_-]{24,}/i],
  ['an email address', /\b[\w.+-]+@[\w-]+\.[\w.-]+\b/],
];

const escape = (s: string): string => s.replace(/[.*+?^${}()|[\]\\]/g, '\\$&');

/** What the text shows that must not be published; empty when the shot is clean. */
export function findPrivate(text: string, who: Identity): string[] {
  const found: string[] = [];
  for (const [what, re] of TOKENS) if (re.test(text)) found.push(what);
  const word = (w: string): boolean =>
    w.length >= 3 && new RegExp(`(^|[^\\w-])${escape(w)}($|[^\\w-])`, 'i').test(text);
  if (word(who.hostname)) found.push(`the machine name ${who.hostname}`);
  for (const l of who.logins) if (word(l)) found.push(`the login ${l}`);
  for (const o of who.owners)
    if (new RegExp(`${escape(o)}/[\\w.-]+`, 'i').test(text) || word(o))
      found.push(`the repository owner ${o}`);
  return found;
}
