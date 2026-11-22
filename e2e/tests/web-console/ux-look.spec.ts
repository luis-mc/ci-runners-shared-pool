// CRSP164 (design system UX-55, UX-60; look decision D1 Option B, D2 six palettes): a sample screen is drawn in
// each of the six themes, dark and light, from the console's own stylesheet. The computed colours must equal the
// swatches of docs/project/content/look/02-options.html for that palette, parsed from the file. Any difference fails.
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { expect, test } from '@playwright/test';
import { openRoute, type Mode } from './ux-guards';

type Swatch = Record<string, string>;
const LOOK = resolve('docs/project/content/look/02-options.html');

/** Option letter to the id of the theme in packages/ui/src/themes.ts. */
const PALETTES: { match: RegExp; id: string }[] = [
  { match: /phosphor/i, id: 'phosphor' },
  { match: /Tokyo Night/i, id: 'tokyo-night' },
  { match: /Catppuccin/i, id: 'catppuccin' },
  { match: /Gruvbox/i, id: 'gruvbox' },
  { match: /Solarized/i, id: 'solarized' },
  { match: /Nord/i, id: 'nord' },
];

/** The twelve swatches (page, surface, text, muted, border, primary, onprimary, done, note, needs, problem, sel) of one theme, dark and light. */
export function parseLook(html: string): Record<string, Record<Mode, Swatch>> {
  const out: Record<string, Record<Mode, Swatch>> = {};
  for (const chunk of html.split('<div class="lk-opt">').slice(1)) {
    const inspired = /<b>Inspired by<\/b>([^<]*)/.exec(chunk)?.[1] ?? '';
    const id = PALETTES.find((p) => p.match.test(inspired))?.id;
    if (!id) throw new Error(`02-options.html: no palette found in "${inspired}"`);
    const blocks = [...chunk.matchAll(/<div class="lk-sw">(.*?)<\/div>/g)].map(
      (m) => m[1] ?? '',
    );
    if (blocks.length !== 2)
      throw new Error(`02-options.html: ${id} has no dark and light swatches`);
    const read = (b: string): Swatch => {
      const s: Swatch = {};
      for (const m of b.matchAll(/title="(\w+) (#[0-9a-fA-F]{6})"/g))
        s[m[1] as string] = (m[2] as string).toLowerCase();
      return s;
    };
    out[id] = { dark: read(blocks[0] as string), light: read(blocks[1] as string) };
  }
  return out;
}

const hexToRgb = (h: string): string => {
  const n = parseInt(h.slice(1), 16);
  return `rgb(${(n >> 16) & 255}, ${(n >> 8) & 255}, ${n & 255})`;
};

const look = parseLook(readFileSync(LOOK, 'utf8'));

test('the look file holds the six palettes with twelve colours each', () => {
  expect(Object.keys(look).sort()).toEqual(PALETTES.map((p) => p.id).sort());
  for (const [id, modes] of Object.entries(look))
    for (const mode of ['dark', 'light'] as const)
      expect(Object.keys(modes[mode]).sort(), `${id} ${mode}`).toEqual(
        [
          'border',
          'done',
          'needs',
          'note',
          'onprimary',
          'page',
          'primary',
          'problem',
          'sel',
          'surface',
          'text',
          'muted',
        ].sort(),
      );
});

const SAMPLE = `
<div id="look-sample" class="ds-frame">
  <section class="ds-pane" aria-label="Sample">
    <h2 class="ds-pane__title">Machines<span class="ds-pane__count">3</span></h2>
    <div class="ds-pane__body"><div class="ds-table-frame"><table class="ds-table"><tbody>
      <tr id="s-cursor" data-list-row data-active><td>home-pc</td></tr>
      <tr id="s-ticked" data-list-row data-selected><td>build-box</td></tr>
      <tr id="s-plain" data-list-row><td>old-laptop</td></tr>
    </tbody></table></div></div>
  </section>
  <span class="ds-button-wrap"><button id="s-primary" class="ds-button" data-kind="primary" data-primary>Add machine</button></span>
  <footer id="s-keys" class="ds-keyhints"><ul class="ds-keyhints__list"><li><kbd class="ds-kbd">/</kbd> search</li></ul></footer>
</div>`;

for (const [id, modes] of Object.entries(look)) {
  for (const mode of ['dark', 'light'] as const) {
    test(`look parity: ${id} ${mode}`, async ({ page }) => {
      await openRoute(
        page,
        { url: '/setup/machines', signedOut: false },
        { mode, themeName: id },
      );
      await page.evaluate(
        ([name, m, html]) => {
          const root = document.documentElement;
          root.setAttribute('data-theme-name', name as string);
          root.setAttribute('data-theme', m as string);
          document.body.insertAdjacentHTML('beforeend', html as string);
        },
        [id, mode, SAMPLE],
      );
      const got = await page.evaluate(() => {
        const css = (sel: string, prop: string) =>
          getComputedStyle(document.querySelector(sel) as Element).getPropertyValue(prop);
        const token = (name: string) => {
          const probe = document.createElement('i');
          probe.style.color = `var(${name})`;
          document.body.append(probe);
          const c = getComputedStyle(probe).color;
          probe.remove();
          return c;
        };
        return {
          page: css('body', 'background-color'),
          text: css('body', 'color'),
          surface: css('#look-sample .ds-pane', 'background-color'),
          border: css('#look-sample .ds-pane', 'border-top-color'),
          primary: css('#s-primary', 'background-color'),
          onprimary: css('#s-primary', 'color'),
          muted: css('#s-keys', 'color'),
          sel: css('#s-ticked', 'background-color'),
          cursorBg: css('#s-cursor', 'background-color'),
          cursorFg: css('#s-cursor', 'color'),
          title: css('#look-sample .ds-pane__title', 'color'),
          titleBg: css('#look-sample .ds-pane__title', 'background-color'),
          done: token('--ds-color-done'),
          note: token('--ds-color-note'),
          needs: token('--ds-color-needs-you'),
          problem: token('--ds-color-problem'),
        };
      });
      const want = modes[mode];
      const w = (k: string) => hexToRgb(want[k] as string);
      expect(got).toEqual({
        page: w('page'),
        text: w('text'),
        surface: w('surface'),
        border: w('border'),
        primary: w('primary'),
        onprimary: w('onprimary'),
        muted: w('muted'),
        sel: w('sel'),
        // reverse video: the cursor row is the primary fill with its own text colour (components 41)
        cursorBg: w('primary'),
        cursorFg: w('onprimary'),
        // the pane title is cut into the border: primary text over the page colour
        title: w('primary'),
        titleBg: w('page'),
        done: w('done'),
        note: w('note'),
        needs: w('needs'),
        problem: w('problem'),
      });
    });
  }
}
