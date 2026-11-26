/**
 * The six colour themes (design system tokens.md, Colour themes). The one source the token CSS is built from
 * (`gen-themes.ts` writes it into packages/ui/src/tokens.css; `themes.test.ts` fails when the two drift).
 * Owner decisions LOOK D1 and D2 (2026-11-15). Keys: sel = selection, onprimary = on-primary.
 */
export interface Palette {
  readonly page: string;
  readonly surface: string;
  readonly text: string;
  readonly muted: string;
  /** Text of a row the console dims as inactive: grey, apart from the green or blue muted text (UX-70). */
  readonly inactive: string;
  readonly border: string;
  readonly primary: string;
  readonly onprimary: string;
  readonly done: string;
  readonly note: string;
  readonly needs: string;
  readonly problem: string;
  readonly sel: string;
  /** Magenta of the concept: an ephemeral or new thing. Defaults to the note colour. */
  readonly accent?: string;
  /** Orange of the concept: a warm hint. Defaults to the needs-you colour. */
  readonly warm?: string;
}

export interface ThemeData {
  readonly id: string;
  readonly name: string;
  /** The radius of inputs and badges (--ds-radius-1); buttons and cards use radius 2, dialogs radius 3. */
  readonly radius: readonly [string, string, string];
  readonly dark: Palette;
  readonly light: Palette;
  /** A flat theme: no glow, no scanline, no shadow, a 1 px ring for focus (the terminal theme). */
  readonly flat?: boolean;
}

const sharp = ['0', '0', '0'] as const;

function terminal(p: {
  bg: string;
  panel: string;
  fg: string;
  dim: string;
  muted: string;
  g: string;
  y: string;
  r: string;
  c: string;
  m: string;
  o: string;
  sel: string;
}): Palette {
  return {
    page: p.bg,
    surface: p.panel,
    text: p.fg,
    muted: p.muted,
    inactive: p.muted,
    border: p.dim,
    primary: p.g,
    onprimary: p.bg,
    done: p.g,
    note: p.c,
    needs: p.y,
    problem: p.r,
    sel: p.sel,
    accent: p.m,
    warm: p.o,
  };
}

/** The three palettes of the concept page (tokens --bg --panel --fg --dim --g --y --r --c --m --o --sel). */
export const TERMINAL: Record<'green' | 'amber' | 'ice', Palette> = {
  green: terminal({
    bg: '#0b0e0d',
    panel: '#0f1413',
    fg: '#cfd8d3',
    dim: '#6b7a73',
    muted: '#8b9892',
    g: '#4fd18b',
    y: '#d9c04a',
    r: '#e0605a',
    c: '#4fc3d9',
    m: '#b48ae0',
    o: '#e8944a',
    sel: '#17332a',
  }),
  amber: terminal({
    bg: '#0e0b06',
    panel: '#140f08',
    fg: '#e8d9b8',
    dim: '#8a7650',
    muted: '#9f8c67',
    g: '#e8b44a',
    y: '#f0d060',
    r: '#e0705a',
    c: '#d9a24f',
    m: '#c8907a',
    o: '#e8944a',
    sel: '#33260f',
  }),
  ice: terminal({
    bg: '#070b10',
    panel: '#0b1219',
    fg: '#c9d6e2',
    dim: '#62778a',
    muted: '#7f92a3',
    g: '#5fd0c4',
    y: '#e0c868',
    r: '#e86a7a',
    c: '#5aa9f0',
    m: '#a98ae8',
    o: '#f0a060',
    sel: '#112a3d',
  }),
};

export const THEME_DATA: readonly ThemeData[] = [
  {
    id: 'phosphor',
    name: 'Phosphor',
    radius: sharp,
    dark: {
      page: '#040a05',
      surface: '#08130a',
      text: '#b9f7b4',
      muted: '#78b874',
      inactive: '#8a948a',
      border: '#4a8a47',
      primary: '#4dff88',
      onprimary: '#021207',
      done: '#6dff8f',
      note: '#7fe7ff',
      needs: '#ffc94d',
      problem: '#ff7466',
      sel: '#123d1a',
    },
    light: {
      page: '#eef5ea',
      surface: '#f9fcf7',
      text: '#0e2a12',
      muted: '#3f5e41',
      inactive: '#5c635c',
      border: '#5f7d61',
      primary: '#0f6b2c',
      onprimary: '#ffffff',
      done: '#0f6b2c',
      note: '#005f73',
      needs: '#8a5200',
      problem: '#b3261e',
      sel: '#d4ead2',
    },
  },
  {
    id: 'tokyo-night',
    name: 'Tokyo Night',
    radius: sharp,
    dark: {
      page: '#16161e',
      surface: '#1a1b26',
      text: '#c0caf5',
      muted: '#9aa5ce',
      inactive: '#8a948a',
      border: '#6b74a3',
      primary: '#7aa2f7',
      onprimary: '#11121a',
      done: '#9ece6a',
      note: '#7dcfff',
      needs: '#e0af68',
      problem: '#ff8aa0',
      sel: '#292e48',
    },
    light: {
      page: '#e1e2e7',
      surface: '#f4f5f8',
      text: '#343b58',
      muted: '#4f5574',
      inactive: '#5c635c',
      border: '#6b7194',
      primary: '#2350b5',
      onprimary: '#ffffff',
      done: '#3a6a17',
      note: '#0f6587',
      needs: '#865500',
      problem: '#b4214a',
      sel: '#d5d9ea',
    },
  },
  {
    id: 'catppuccin',
    name: 'Catppuccin',
    radius: ['8px', '8px', '8px'],
    dark: {
      page: '#181825',
      surface: '#1e1e2e',
      text: '#cdd6f4',
      muted: '#a6adc8',
      inactive: '#8a948a',
      border: '#7f849c',
      primary: '#cba6f7',
      onprimary: '#1e1e2e',
      done: '#a6e3a1',
      note: '#89dceb',
      needs: '#f9e2af',
      problem: '#f38ba8',
      sel: '#313244',
    },
    light: {
      page: '#e6e9ef',
      surface: '#eff1f5',
      text: '#4c4f69',
      muted: '#5c5f77',
      inactive: '#5c635c',
      border: '#7c7f93',
      primary: '#7a2ee0',
      onprimary: '#ffffff',
      done: '#2f6d1a',
      note: '#0b6680',
      needs: '#835300',
      problem: '#b5163a',
      sel: '#dce0e8',
    },
  },
  {
    id: 'gruvbox',
    name: 'Gruvbox',
    radius: sharp,
    dark: {
      page: '#1d2021',
      surface: '#282828',
      text: '#ebdbb2',
      muted: '#bdae93',
      inactive: '#8a948a',
      border: '#7c6f64',
      primary: '#fabd2f',
      onprimary: '#1d2021',
      done: '#b8bb26',
      note: '#8ec0a8',
      needs: '#fe8019',
      problem: '#ff6b5a',
      sel: '#32302f',
    },
    light: {
      page: '#f2e5bc',
      surface: '#fbf1c7',
      text: '#3c3836',
      muted: '#5a514a',
      inactive: '#5c635c',
      border: '#7c6f64',
      primary: '#076678',
      onprimary: '#ffffff',
      done: '#5d5a0a',
      note: '#076678',
      needs: '#8f3e02',
      problem: '#9d0006',
      sel: '#ebdbb2',
    },
  },
  {
    id: 'solarized',
    name: 'Solarized',
    radius: sharp,
    dark: {
      page: '#002b36',
      surface: '#073642',
      text: '#d2dbd9',
      muted: '#a5b4b5',
      inactive: '#9aa29a',
      border: '#7a9497',
      primary: '#6cbcf5',
      onprimary: '#002b36',
      done: '#a5c23a',
      note: '#4fcfc4',
      needs: '#e0b13a',
      problem: '#ff9a95',
      sel: '#0a3f4c',
    },
    light: {
      page: '#fdf6e3',
      surface: '#fffbf0',
      text: '#073642',
      muted: '#4c6168',
      inactive: '#5c635c',
      border: '#6d7f83',
      primary: '#1a5f96',
      onprimary: '#ffffff',
      done: '#4f6000',
      note: '#1d6e68',
      needs: '#7d5300',
      problem: '#b0201d',
      sel: '#eee8d5',
    },
  },
  {
    id: 'nord',
    name: 'Nord',
    radius: ['6px', '6px', '6px'],
    dark: {
      page: '#20242c',
      surface: '#2e3440',
      text: '#eceff4',
      muted: '#b4bccb',
      inactive: '#9aa29a',
      border: '#76829b',
      primary: '#88c0d0',
      onprimary: '#1b2029',
      done: '#a3be8c',
      note: '#93b4d6',
      needs: '#ebcb8b',
      problem: '#ef9aa1',
      sel: '#363c4a',
    },
    light: {
      page: '#e5e9f0',
      surface: '#f6f8fb',
      text: '#2e3440',
      muted: '#4c566a',
      inactive: '#5c635c',
      border: '#6b768c',
      primary: '#2f6585',
      onprimary: '#ffffff',
      done: '#466a2c',
      note: '#3a5d88',
      needs: '#805600',
      problem: '#a3343f',
      sel: '#dde3ec',
    },
  },
  {
    // The btop-style terminal theme (CRSP300; owner-approved concept 2026-11-25). It is dark in every mode: a
    // choice of light or system mode leaves it as it is. --dim of the concept is the border; the muted text is
    // --dim lightened to 4.5:1 on every surface (UX-17).
    id: 'terminal',
    name: 'Terminal',
    radius: sharp,
    flat: true,
    dark: TERMINAL.green,
    light: TERMINAL.green,
  },
];

/** The palettes of the terminal theme, picked with `data-pal` (green is the default and has no attribute). */
export const TERMINAL_PALETTES = ['green', 'amber', 'ice'] as const;
export type TerminalPalette = (typeof TERMINAL_PALETTES)[number];

export const DEFAULT_THEME = 'phosphor';
