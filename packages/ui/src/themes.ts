// The colour themes the user can pick (design system tokens.md, Colour themes; UX-60). Only names live here;
// the values are tokens in tokens.css, so a theme changes token values and never a component (UX-55).
export interface ThemeInfo {
  readonly id: string;
  readonly name: string;
}

export const THEMES: readonly ThemeInfo[] = [
  { id: 'phosphor', name: 'Phosphor' },
  { id: 'tokyo-night', name: 'Tokyo Night' },
  { id: 'catppuccin', name: 'Catppuccin' },
  { id: 'gruvbox', name: 'Gruvbox' },
  { id: 'solarized', name: 'Solarized' },
  { id: 'nord', name: 'Nord' },
];

/** The theme of a new install, and of a page where none is set: Phosphor, dark (owner decision LOOK D11). */
/** The palettes of the terminal theme (`data-pal` on the page root; green is the default and sets no attribute). */
export const TERMINAL_PALETTE_CHOICES: readonly ThemeInfo[] = [
  { id: 'green', name: 'Green' },
  { id: 'amber', name: 'Amber' },
  { id: 'ice', name: 'Ice' },
];
export const PALETTE_STORAGE = 'crsp.theme-palette';
/** How the Cool Terminal themes draw graphs: blocks, or dots (`data-graphs=dotted` on the page root; blocks sets none). */
export const GRAPHS_STORAGE = 'crsp.theme-graphs';
/** The text size choice (CRSP399): a percent of the base type scale, kept in this browser. 100 is the base (`--ds-type-scale`). */
export const TEXT_SIZE_STORAGE = 'crsp.text-size';
export const TEXT_SIZE_STEPS = [80, 90, 100, 110, 120, 130] as const;
export const TEXT_SIZE_DEFAULT = 100;

export const DEFAULT_THEME_ID = 'phosphor';

/** The id of a Terminal choice: `terminal-green`, `terminal-amber` or `terminal-ice` (CRSP358). */
export const TERMINAL_CHOICE_PREFIX = 'terminal-';
/** The one Theme list of Settings, Appearance: the six colour themes, then the three Terminal palettes (CRSP358). */
export const THEME_CHOICES: readonly ThemeInfo[] = [
  ...THEMES,
  ...TERMINAL_PALETTE_CHOICES.map((p) => ({
    id: TERMINAL_CHOICE_PREFIX + p.id,
    name: `Terminal ${p.name}`,
  })),
];

/** Where the choice is kept in this browser, so the page paints in it before anything loads (public/theme-init.js). */
export const THEME_NAME_STORAGE = 'crsp.theme-name';
export const THEME_MODE_STORAGE = 'crsp.theme-mode';

/** The mode of a new install, until the user chooses system or light. */
export const DEFAULT_THEME_MODE = 'dark';

/** The terminal theme (CRSP300): not one of the six colour themes; Settings offers it as Terminal Green, Amber and Ice. */
export const TERMINAL_THEME_ID = 'terminal';
/** Where the last colour theme is kept, so leaving Terminal brings it back. */
export const LAST_COLOUR_THEME_STORAGE = 'crsp.theme-last';

export function isThemeId(value: unknown): value is string {
  return value === TERMINAL_THEME_ID || THEMES.some((t) => t.id === value);
}

function remember(key: string, value: string): void {
  try {
    window.localStorage.setItem(key, value);
  } catch {
    // Storage may be blocked; the choice still applies for this visit.
  }
}

/** The saved theme name of this browser, or the default. */
export function savedThemeName(): string {
  try {
    const v = window.localStorage.getItem(THEME_NAME_STORAGE);
    if (isThemeId(v)) return v;
  } catch {
    // Storage may be blocked.
  }
  return DEFAULT_THEME_ID;
}

/** Applies a theme by name to the page and remembers it for the next first paint. */
export function applyThemeName(id: string): void {
  if (!isThemeId(id)) return;
  document.documentElement.setAttribute('data-theme-name', id);
  remember(THEME_NAME_STORAGE, id);
  if (id !== TERMINAL_THEME_ID) remember(LAST_COLOUR_THEME_STORAGE, id);
}

/** Remembers the mode (system, dark or light) for the next first paint; the server stays the source. */
export function rememberThemeMode(mode: string): void {
  remember(THEME_MODE_STORAGE, mode);
}

/** The saved terminal palette of this browser, or green. */
export function savedPalette(): string {
  try {
    const v = window.localStorage.getItem(PALETTE_STORAGE);
    if (TERMINAL_PALETTE_CHOICES.some((p) => p.id === v)) return v as string;
  } catch {
    // Storage may be blocked.
  }
  return 'green';
}

/** Applies a terminal palette to the page and remembers it for the next first paint. */
export function applyPalette(id: string): void {
  if (!TERMINAL_PALETTE_CHOICES.some((p) => p.id === id)) return;
  if (id === 'green') document.documentElement.removeAttribute('data-pal');
  else document.documentElement.setAttribute('data-pal', id);
  remember(PALETTE_STORAGE, id);
}

/** The colour theme to return to when the choice leaves Terminal: the last one used, or the default. */
export function lastColourTheme(): string {
  try {
    const v = window.localStorage.getItem(LAST_COLOUR_THEME_STORAGE);
    if (THEMES.some((t) => t.id === v)) return v as string;
  } catch {
    // Storage may be blocked.
  }
  return DEFAULT_THEME_ID;
}

/** The saved graph style of this browser: blocks (default) or dotted. */
export function savedGraphs(): 'blocks' | 'dotted' {
  try {
    if (window.localStorage.getItem(GRAPHS_STORAGE) === 'dotted') return 'dotted';
  } catch {
    // Storage may be blocked.
  }
  return 'blocks';
}

/** Applies a graph style to the page and remembers it for the next first paint. */
export function applyGraphs(style: 'blocks' | 'dotted'): void {
  if (style === 'dotted') document.documentElement.setAttribute('data-graphs', 'dotted');
  else document.documentElement.removeAttribute('data-graphs');
  remember(GRAPHS_STORAGE, style);
}

/** The saved text size of this browser in percent: one of the steps, or 100. */
export function savedTextSize(): number {
  try {
    const n = Number(window.localStorage.getItem(TEXT_SIZE_STORAGE));
    if ((TEXT_SIZE_STEPS as readonly number[]).includes(n)) return n;
  } catch {
    // Storage may be blocked.
  }
  return TEXT_SIZE_DEFAULT;
}

/** Applies a text size to the page (`data-text-size` on the root moves `--ds-type-scale`) and remembers it for the next first paint. */
export function applyTextSize(pct: number): void {
  const step = (TEXT_SIZE_STEPS as readonly number[]).includes(pct)
    ? pct
    : TEXT_SIZE_DEFAULT;
  if (step === TEXT_SIZE_DEFAULT)
    document.documentElement.removeAttribute('data-text-size');
  else document.documentElement.setAttribute('data-text-size', String(step));
  remember(TEXT_SIZE_STORAGE, String(step));
}

/** True for `terminal-green`, `terminal-amber` and `terminal-ice`. */
export function isTerminalChoice(id: string): boolean {
  return (
    id.startsWith(TERMINAL_CHOICE_PREFIX) &&
    TERMINAL_PALETTE_CHOICES.some((p) => TERMINAL_CHOICE_PREFIX + p.id === id)
  );
}

/** The palette of a Terminal choice (`terminal-amber` gives `amber`); green for anything else. */
export function terminalChoicePalette(id: string): string {
  return isTerminalChoice(id) ? id.slice(TERMINAL_CHOICE_PREFIX.length) : 'green';
}

/**
 * The one Theme choice of a saved name and palette. This is the migration of the old Look and Palette settings:
 * name `terminal` with palette X is the choice `terminal-X`; any colour theme keeps its own id. The storage keys
 * stay as they were, so nothing needs rewriting and the first paint (public/theme-init.js) is unchanged.
 */
export function themeChoiceOf(name: string, palette: string): string {
  if (name !== TERMINAL_THEME_ID) return isThemeId(name) ? name : DEFAULT_THEME_ID;
  const pal = TERMINAL_PALETTE_CHOICES.some((p) => p.id === palette) ? palette : 'green';
  return TERMINAL_CHOICE_PREFIX + pal;
}

/** The saved Theme choice of this browser. */
export function savedThemeChoice(): string {
  return themeChoiceOf(savedThemeName(), savedPalette());
}

/** Applies one Theme choice: a colour theme, or Terminal with its palette; remembers it for the next first paint. */
export function applyThemeChoice(id: string): void {
  if (isTerminalChoice(id)) {
    applyPalette(terminalChoicePalette(id));
    applyThemeName(TERMINAL_THEME_ID);
  } else applyThemeName(id);
}
