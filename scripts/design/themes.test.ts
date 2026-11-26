/**
 * The colour themes guard (design system tokens.md, Colour themes; UX-17, UX-55, UX-60):
 *  - every theme defines every colour token, in dark, in light by choice and in light under the system setting;
 *  - the token CSS is the one the theme data produces (no hand edit, no drift);
 *  - every text pair meets 4.5:1 and every border 3:1, in every theme and mode;
 *  - no theme sets anything but token values (colour, effect, shadow and radius tokens).
 */
import { readFileSync } from 'node:fs';
import { describe, expect, it } from 'vitest';
import { THEME_DATA, DEFAULT_THEME, TERMINAL, type ThemeData } from './themes.data.ts';
import { colourTokens, effectTokens, ratio } from './themes.ts';

const css = readFileSync(
  new URL('../../packages/ui/src/tokens.css', import.meta.url),
  'utf8',
).replace(/\/\*[\s\S]*?\*\//g, '');

interface Rule {
  readonly media: string;
  readonly selectors: readonly string[];
  readonly decls: Map<string, string>;
}

const norm = (v: string): string =>
  v.replace(/\s+/g, ' ').replace(/\( /g, '(').replace(/ \)/g, ')').trim();

function parse(source: string): Rule[] {
  const rules: Rule[] = [];
  let media = '';
  let i = 0;
  while (i < source.length) {
    const open = source.indexOf('{', i);
    if (open < 0) break;
    const head = source.slice(i, open).trim();
    if (head.startsWith('@media')) {
      media = head;
      i = open + 1;
      continue;
    }
    const close = source.indexOf('}', open);
    const decls = new Map<string, string>();
    for (const d of source.slice(open + 1, close).split(/;\s*(?![^()]*\))/)) {
      const at = d.indexOf(':');
      if (at > 0) decls.set(d.slice(0, at).trim(), norm(d.slice(at + 1)));
    }
    rules.push({
      media,
      selectors: head.split(',').map((s) => s.trim().replace(/\s+/g, ' ')),
      decls,
    });
    i = close + 1;
    if (source.slice(i).match(/^\s*\}/)) {
      media = '';
      i = source.indexOf('}', i) + 1;
    }
  }
  return rules;
}

const rules = parse(css);
const ds = (s: string): string => s.replace(/'/g, '');

function declared(selector: string, media: string): Map<string, string> {
  const hit = rules.find(
    (r) =>
      r.media === media &&
      r.selectors.map(ds).includes(selector) &&
      r.decls.has('--ds-color-page'),
  );
  if (!hit) throw new Error(`no rule ${selector} in ${media || 'the page'}`);
  return hit.decls;
}

const LIGHT_MEDIA = '@media (prefers-color-scheme: light)';
type Mode = 'dark' | 'light';
const MODES: readonly Mode[] = ['dark', 'light'];

/** The three places a theme's mode is written: [selector, media]. */
function places(t: ThemeData, mode: Mode): [string, string][] {
  const name = `[data-theme-name=${t.id}]`;
  if (mode === 'dark')
    return [
      [name, ''],
      [`[data-theme=dark]${name}`, ''],
      [`[data-theme=dark] ${name}`, ''],
    ];
  return [
    [`:root:not([data-theme=dark])${name}`, LIGHT_MEDIA],
    [`[data-theme=light]${name}`, ''],
    [`[data-theme=light] ${name}`, ''],
  ];
}

const REQUIRED_COLOURS = [
  'page',
  'surface',
  'text',
  'text-muted',
  'text-inactive',
  'border',
  'divider',
  'primary',
  'on-primary',
  'focus',
  'selection',
  'accent',
  'warm',
  ...['done', 'note', 'needs-you', 'problem'].flatMap((n) => [
    n,
    `${n}-bg`,
    `on-${n}`,
    `${n}-tint`,
    `on-${n}-tint`,
  ]),
];
const ALLOWED = /^--ds-(color-|radius-[123]$|glow-focus$|scanline-focus$|shadow-[23]$)/;

describe('every theme defines every token', () => {
  it('has the seven themes, Phosphor default', () => {
    expect(THEME_DATA.map((t) => t.id)).toEqual([
      'phosphor',
      'tokyo-night',
      'catppuccin',
      'gruvbox',
      'solarized',
      'nord',
      'terminal',
    ]);
    expect(DEFAULT_THEME).toBe('phosphor');
  });

  for (const t of THEME_DATA)
    for (const mode of MODES)
      it(`${t.id} ${mode}: all colour tokens, radius, effects; values are the data`, () => {
        const want: Record<string, string> = {
          ...Object.fromEntries(
            Object.entries(colourTokens(t[mode], mode)).map(([k, v]) => [
              `--ds-color-${k}`,
              v,
            ]),
          ),
          ...Object.fromEntries(
            Object.entries(effectTokens(t[mode], mode, t.flat === true)).map(([k, v]) => [
              `--ds-${k}`,
              v,
            ]),
          ),
          '--ds-radius-1': t.radius[0],
          '--ds-radius-2': t.radius[1],
          '--ds-radius-3': t.radius[2],
        };
        for (const k of REQUIRED_COLOURS)
          expect(want[`--ds-color-${k}`], `${t.id} ${mode} ${k}`).toBeTruthy();
        for (const [sel, media] of places(t, mode)) {
          const got = declared(sel, media);
          for (const [k, v] of Object.entries(want))
            expect(got.get(k), `${sel} ${k}`).toBe(norm(v));
          for (const k of got.keys())
            expect(k, `${sel} sets only tokens`).toMatch(ALLOWED);
        }
      });

  for (const t of THEME_DATA)
    for (const mode of MODES)
      it(`${t.id} ${mode}: a sample inside any page keeps its own mode`, () => {
        const got = declared(`:root [data-theme=${mode}][data-theme-name=${t.id}]`, '');
        const want = colourTokens(t[mode], mode);
        for (const [k, v] of Object.entries(want))
          expect(got.get(`--ds-color-${k}`), `${t.id} ${mode} ${k}`).toBe(norm(v));
      });

  it('the page without a theme is Phosphor, dark and light', () => {
    const def = THEME_DATA.find((t) => t.id === DEFAULT_THEME) as ThemeData;
    const dark = declared(':root', '');
    const light = declared(':root:not([data-theme=dark])', LIGHT_MEDIA);
    const chosen = declared('[data-theme=light]', '');
    for (const [k, v] of Object.entries(colourTokens(def.dark, 'dark')))
      expect(dark.get(`--ds-color-${k}`)).toBe(v);
    for (const [k, v] of Object.entries(colourTokens(def.light, 'light'))) {
      expect(light.get(`--ds-color-${k}`)).toBe(v);
      expect(chosen.get(`--ds-color-${k}`)).toBe(v);
    }
  });
});

describe('contrast in every theme (UX-17)', () => {
  for (const t of THEME_DATA)
    for (const mode of MODES)
      it(`${t.id} ${mode}: text pairs 4.5:1, borders 3:1`, () => {
        const c = colourTokens(t[mode], mode);
        const at = (k: string): string => c[k] as string;
        const text: [string, string][] = [];
        for (const bg of ['page', 'surface', 'selection'])
          text.push(['text', bg], ['text-muted', bg]);
        for (const bg of ['page', 'surface']) text.push(['text-inactive', bg]);
        for (const bg of ['page', 'surface']) text.push(['primary', bg]);
        text.push(['on-primary', 'primary']);
        for (const n of ['done', 'note', 'needs-you', 'problem']) {
          text.push(
            [n, 'page'],
            [n, 'surface'],
            [`on-${n}`, n],
            [`on-${n}-tint`, `${n}-tint`],
            [`on-${n}-tint`, `${n}-bg`],
          );
        }
        for (const [fg, bg] of text)
          expect(ratio(at(fg), at(bg)), `${fg} on ${bg}`).toBeGreaterThanOrEqual(4.5);
        for (const bg of ['page', 'surface'])
          expect(ratio(at('border'), at(bg)), `border on ${bg}`).toBeGreaterThanOrEqual(
            3,
          );
      });
});

describe('terminal palettes (data-pal)', () => {
  for (const pal of ['amber', 'ice'] as const)
    it(`${pal}: written after the mode rules, tokens only, contrast holds`, () => {
      const rule = rules.find((r) =>
        r.selectors.map(ds).includes(`:root[data-theme-name=terminal][data-pal=${pal}]`),
      );
      expect(rule, `${pal} rule`).toBeTruthy();
      const want = colourTokens(TERMINAL[pal], 'dark');
      for (const [k, v] of Object.entries(want))
        expect(rule?.decls.get(`--ds-color-${k}`), k).toBe(norm(v));
      for (const k of rule?.decls.keys() ?? []) expect(k).toMatch(ALLOWED);
      for (const bg of ['page', 'surface', 'selection'])
        for (const fg of ['text', 'text-muted'])
          expect(ratio(want[fg] as string, want[bg] as string)).toBeGreaterThanOrEqual(
            4.5,
          );
      for (const bg of ['page', 'surface'])
        expect(ratio(want.border as string, want[bg] as string)).toBeGreaterThanOrEqual(
          3,
        );
    });
});
