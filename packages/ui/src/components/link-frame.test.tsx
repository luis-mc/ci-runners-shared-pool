// UX-79, UX-80 (LOOK56): link colours come from tokens in every state; the frame token and the wide breakpoint;
// the phone header keeps its status in its own column.
import { readFileSync } from 'node:fs';
import { join } from 'node:path';
import { describe, expect, it } from 'vitest';

const dir = join(import.meta.dirname, '..');
const css = readFileSync(join(dir, 'components.css'), 'utf8');
const tokens = readFileSync(join(dir, 'tokens.css'), 'utf8');

/** The declaration blocks whose selector list contains `selector`, joined. */
function blocks(selector: string): string {
  const out: string[] = [];
  const re = /([^{}]+)\{([^{}]*)\}/g;
  let m: RegExpExecArray | null;
  while ((m = re.exec(css))) {
    const sels = (m[1] ?? '')
      .replace(/\/\*[\s\S]*?\*\//g, '')
      .split(',')
      .map((s) => s.trim());
    if (sels.includes(selector)) out.push(m[2] ?? '');
  }
  return out.join('\n');
}

const colour = /(^|[\s;])color:\s*var\(--ds-color-[a-z-]+\)/;

describe('links use theme tokens (UX-80)', () => {
  it('has a base rule for a and a:visited with a token colour', () => {
    expect(blocks(':where(a)')).toMatch(colour);
    expect(blocks(':where(a:visited)')).toMatch(colour);
    expect(blocks(':where(a:hover)')).toMatch(colour);
    expect(blocks(':where(a:focus-visible)')).toContain('outline');
    expect(blocks(':where(a[aria-current])')).toMatch(colour);
  });

  it('keeps the base rule at zero specificity so a class always wins', () => {
    expect(css).not.toMatch(/^a(:visited)?\s*[,{]/m);
  });

  it.each(['.ds-link', '.ds-topmenu__link', '.ds-boxbadge', '.ds-sidemenu__link'])(
    '%s sets a token colour for normal, visited, hover and focus-visible',
    (cls) => {
      expect(blocks(cls)).toMatch(colour);
      expect(blocks(`${cls}:visited`)).toMatch(colour);
      expect(blocks(`${cls}:hover`)).toMatch(colour);
      expect(blocks(`${cls}:focus-visible`)).toMatch(colour);
    },
  );

  it.each(['.ds-link', '.ds-boxbadge'])('%s sets a token colour when current', (cls) => {
    expect(blocks(`${cls}[aria-current]`)).toMatch(colour);
  });

  it('marks the current top menu item with a token colour in every state', () => {
    for (const s of ['', ':visited', ':hover', ':focus-visible']) {
      expect(blocks(`.ds-topmenu__link[aria-current='page']${s}`)).toMatch(colour);
    }
  });

  it('every link class in the stylesheet sets a token colour for visited, hover and focus', () => {
    const classes = new Set(css.match(/\.ds-[a-z0-9_-]*link(?![a-z0-9_-])/g) ?? []);
    expect(classes.size).toBeGreaterThan(2);
    for (const cls of classes) {
      for (const s of [':visited', ':hover', ':focus-visible']) {
        expect(blocks(`${cls}${s}`), `${cls}${s}`).toMatch(colour);
      }
    }
  });

  it('marks the current side menu item with a token colour in every state', () => {
    for (const s of ['', ':visited', ':hover', ':focus-visible']) {
      expect(blocks(`.ds-sidemenu__link[aria-current='page']${s}`)).toMatch(colour);
    }
  });

  it('never writes a literal colour in a link rule', () => {
    const linky = css.match(
      /[^{}]*(a:visited|__link|\.ds-link|\.ds-boxbadge)[^{}]*\{[^}]*\}/g,
    );
    for (const b of linky ?? []) expect(b).not.toMatch(/#[0-9a-f]{3,8}\b|rgba?\(/i);
  });
});

describe('page frame (UX-79)', () => {
  it('is 96rem wide with wide layouts from 1400px', () => {
    expect(tokens).toMatch(/--ds-frame-wide:\s*96rem;/);
    expect(tokens).toMatch(/--ds-bp-wide:\s*1400px;/);
    expect(blocks('.ds-band')).toContain('max-inline-size: var(--ds-frame-wide)');
  });
});

describe('phone header', () => {
  it('lets the status wrap in its own column instead of running under Commands', () => {
    const phone = css.slice(css.indexOf('.ds-header__frame {\n    display: grid'));
    const state = phone.slice(phone.indexOf('.ds-header__state {'));
    const block = state.slice(0, state.indexOf('}'));
    expect(block).toContain('grid-area: state');
    expect(block).toContain('min-inline-size: 0');
    expect(block).toContain('overflow-wrap: anywhere');
  });
});
