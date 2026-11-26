// CRSP300: the terminal theme parts: Panel, Meter, Spark, and the machine title of Pane and Card.
import { cleanup, render, screen } from '@testing-library/react';
import { readFileSync } from 'node:fs';
import { join } from 'node:path';
import { MemoryRouter } from 'react-router-dom';
import { afterEach, describe, expect, it } from 'vitest';
import { Card } from './Card';
import { Meter } from './Meter';
import { Panel } from './Panel';
import { Spark, SPARK_LEVELS, sparkChars } from './Spark';

afterEach(cleanup);
const css = readFileSync(join(import.meta.dirname, '..', 'components.css'), 'utf8');

describe('Panel', () => {
  it('sets a plain title over the border with its count and its right-hand text', () => {
    render(
      <Panel title="runners" count={3} meta="live ●" tone="note">
        body
      </Panel>,
    );
    const region = screen.getByRole('region', { name: 'runners' });
    expect(region.getAttribute('data-tone')).toBe('note');
    const title = screen.getByRole('heading', { name: /runners/ });
    expect(title.textContent).toBe('runners · 3');
    expect(title.hasAttribute('data-machine')).toBe(false);
    expect(screen.getByText('live ●')).toBeTruthy();
  });

  it('marks a machine title', () => {
    render(
      <Panel title="HOME-PC" machine>
        x
      </Panel>,
    );
    expect(
      screen.getByRole('heading', { name: 'HOME-PC' }).hasAttribute('data-machine'),
    ).toBe(true);
  });

  it('colours only a machine title: no rule colours a plain title in the theme', () => {
    const plain = css.match(
      /\[data-theme-name='terminal'\] \.ds-pane__title,\n\.ds-panel__title \{[^}]*\}/,
    );
    expect(plain?.[0]).toContain('color: var(--ds-color-text)');
    expect(css).not.toMatch(
      /\.ds-panel__title(?!\[data-machine\])[^{]*\{[^}]*color: var\(--ds-color-(done|note|needs-you|problem|accent)\)/,
    );
  });
});

describe('Card machine', () => {
  it('marks the machine name of a card', () => {
    render(
      <MemoryRouter>
        <Card title="HOME-PC" machine>
          x
        </Card>
      </MemoryRouter>,
    );
    expect(
      screen.getByRole('heading', { name: 'HOME-PC' }).hasAttribute('data-machine'),
    ).toBe(true);
  });
});

describe('Meter', () => {
  it('draws block characters and says the level in words', () => {
    render(<Meter label="CPU" value={52} />);
    const m = screen.getByRole('meter', { name: 'CPU' });
    expect(m.getAttribute('aria-valuenow')).toBe('52');
    expect(m.getAttribute('aria-valuetext')).toBe('52%');
    expect(m.querySelector('.ds-meter__on')?.textContent).toBe('■■■■');
    expect(m.querySelector('.ds-meter__off')?.textContent).toBe('■■■■');
    expect(m.getAttribute('data-level')).toBe('ok');
  });

  it('warns at 75 and goes bad at 90, and clamps', () => {
    render(
      <>
        <Meter label="a" value={80} />
        <Meter label="b" value={95} />
        <Meter label="c" value={400} />
        <Meter label="d" value={-5} />
      </>,
    );
    expect(screen.getByRole('meter', { name: 'a' }).getAttribute('data-level')).toBe(
      'warn',
    );
    expect(screen.getByRole('meter', { name: 'b' }).getAttribute('data-level')).toBe(
      'bad',
    );
    expect(screen.getByRole('meter', { name: 'c' }).getAttribute('aria-valuenow')).toBe(
      '100',
    );
    expect(screen.getByRole('meter', { name: 'd' }).getAttribute('aria-valuenow')).toBe(
      '0',
    );
  });
});

describe('Spark', () => {
  it('uses the eight block levels, lowest to highest', () => {
    expect(SPARK_LEVELS).toBe('▁▂▃▄▅▆▇█');
    expect(sparkChars([0, 1, 2, 3, 4, 5, 6, 7])).toBe('▁▂▃▄▅▆▇█');
    expect(sparkChars([5, 5, 5])).toBe('▁▁▁');
    expect(sparkChars([])).toBe('');
  });

  it('has a text alternative with the range', () => {
    render(<Spark label="Minutes saved" values={[1, 4, 2]} unit=" min" />);
    expect(
      screen.getByRole('img', {
        name: 'Minutes saved: from 1 min to 2 min, lowest 1 min, highest 4 min',
      }),
    ).toBeTruthy();
  });
});
