// CRSP166: the shared parts the Settings tabs and the machine page lean on: one primary per screen, the theme choice
// with a live preview, a pane footer button at the pane's width, 44 px row links.
import { cleanup, render, screen } from '@testing-library/react';
import { readFileSync } from 'node:fs';
import { join } from 'node:path';
import { afterEach, describe, expect, it } from 'vitest';
import { ExportButton } from './Export';
import { ChoiceField } from './Fields';
import { Wizard } from './Wizard';
import { THEME_CHOICES, THEMES, themeChoiceOf } from '../themes';

afterEach(cleanup);
const css = readFileSync(join(import.meta.dirname, '..', 'components.css'), 'utf8');
const block = (start: string): string => {
  const rest = css.slice(css.indexOf(start));
  return rest.slice(0, rest.indexOf('}'));
};

describe('theme choice with a preview', () => {
  it('shows each theme in dark and in light', () => {
    render(
      <ChoiceField
        label="Theme"
        value="phosphor"
        onChange={() => undefined}
        choices={THEMES.map((t) => ({ value: t.id, label: t.name, preview: t.id }))}
      />,
    );
    expect(document.querySelectorAll('.ds-theme-preview')).toHaveLength(
      THEMES.length * 2,
    );
    expect(screen.getByRole('img', { name: 'Tokyo Night, dark' })).toBeTruthy();
    expect(screen.getByRole('img', { name: 'Tokyo Night, light' })).toBeTruthy();
  });

  it('shows a Terminal choice in dark and light, in the Terminal look with its palette', () => {
    render(
      <ChoiceField
        label="Theme"
        value="terminal-ice"
        onChange={() => undefined}
        choices={THEME_CHOICES.map((t) => ({
          value: t.id,
          label: t.name,
          preview: t.id,
        }))}
      />,
    );
    expect(document.querySelectorAll('.ds-theme-preview')).toHaveLength(
      THEMES.length * 2 + 6,
    );
    const iceLight = screen.getByRole('img', { name: 'Terminal Ice, light' });
    expect(iceLight.getAttribute('data-pal')).toBe('ice');
    expect(iceLight.getAttribute('data-theme')).toBe('light');
    const ice = screen.getByRole('img', { name: 'Terminal Ice, dark' });
    expect(ice.getAttribute('data-theme-name')).toBe('terminal');
    expect(ice.getAttribute('data-pal')).toBe('ice');
    const green = screen.getByRole('img', { name: 'Terminal Green, dark' });
    expect(green.hasAttribute('data-pal')).toBe(false);
  });

  it('turns the old Look and Palette into one choice', () => {
    expect(themeChoiceOf('terminal', 'amber')).toBe('terminal-amber');
    expect(themeChoiceOf('terminal', 'green')).toBe('terminal-green');
    expect(themeChoiceOf('terminal', 'bogus')).toBe('terminal-green');
    expect(themeChoiceOf('nord', 'amber')).toBe('nord');
    expect(themeChoiceOf('nonsense', 'amber')).toBe('phosphor');
  });
});

describe('one primary per screen', () => {
  it('an export button is default unless it is the screen primary', () => {
    const { rerender } = render(
      <ExportButton what="x" onExport={() => Promise.resolve()} />,
    );
    expect(document.querySelector('[data-primary]')).toBeNull();
    rerender(<ExportButton primary what="x" onExport={() => Promise.resolve()} />);
    expect(document.querySelector('[data-primary]')).toBeTruthy();
  });

  it('an embedded wizard draws no primary button but keeps its primary part', () => {
    render(
      <Wizard
        name="Pool size"
        embedded
        steps={['Size', 'Review', 'Done']}
        current={0}
        phase="first-run"
        primary={{ label: 'Review', onPress: () => undefined }}
      >
        body
      </Wizard>,
    );
    expect(document.querySelector('[data-primary]')).toBeNull();
    expect(document.querySelector('[data-part=primary]')?.textContent).toBe('Review');
  });

  it('a wizard on its own screen draws the primary', () => {
    render(
      <Wizard
        name="Add"
        steps={['Size', 'Review', 'Done']}
        current={0}
        phase="first-run"
        primary={{ label: 'Review', onPress: () => undefined }}
      >
        body
      </Wizard>,
    );
    expect(document.querySelectorAll('[data-primary]')).toHaveLength(1);
  });
});

describe('hit areas and pane footer', () => {
  for (const sel of ['.ds-link {', '.ds-breadcrumbs__list a {'])
    it(`${sel} is 44 px wide as well as high`, () => {
      expect(block(sel)).toContain('min-inline-size: var(--ds-target-min)');
    });
  it('a button in a pane footer takes the full width at phone width, primary first (UX-37)', () => {
    expect(css).toMatch(/\.ds-pane__footer \.ds-button,[^{]*\{[^}]*inline-size: 100%/);
    expect(css).toMatch(/flex-direction: column-reverse/);
  });
});
