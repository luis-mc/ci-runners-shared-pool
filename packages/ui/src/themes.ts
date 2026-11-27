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

export const DEFAULT_THEME_ID = 'phosphor';

/** Where the choice is kept in this browser, so the page paints in it before anything loads (public/theme-init.js). */
export const THEME_NAME_STORAGE = 'crsp.theme-name';
export const THEME_MODE_STORAGE = 'crsp.theme-mode';

/** The mode of a new install, until the user chooses system or light. */
export const DEFAULT_THEME_MODE = 'dark';

/** The terminal theme (CRSP300): not one of the six colour themes; Settings offers it as the Look "Terminal". */
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

/** The colour theme to return to when the Look leaves Terminal: the last one used, or the default. */
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
