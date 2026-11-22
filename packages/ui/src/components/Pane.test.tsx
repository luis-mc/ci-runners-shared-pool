// Component 8: the pane. Title in the top border, actions in the border line, count, heading level.
import { cleanup, render, screen } from '@testing-library/react';
import { MemoryRouter } from 'react-router-dom';
import { afterEach, describe, expect, it } from 'vitest';
import { Section } from './layout';
import { Pane } from './Pane';
import { Table } from './Table';
import { Tabs } from './Controls';
import { ArrowNavigation } from './Navigation';
import { fireEvent } from '@testing-library/react';

afterEach(cleanup);

describe('Pane', () => {
  it('names the region by its title and sets the title as a level-2 heading', () => {
    render(
      <Pane title="Machines" count={3}>
        body
      </Pane>,
    );
    const region = screen.getByRole('region', { name: 'Machines' });
    expect(region.className).toBe('ds-pane');
    const h = screen.getByRole('heading', { level: 2 });
    expect(h.textContent).toBe('Machines3');
    expect(region.textContent).toContain('body');
  });

  it('puts actions in the border line and marks the pane that has them', () => {
    render(
      <Pane title="Access" actions={<button type="button">Make an access</button>}>
        x
      </Pane>,
    );
    const region = screen.getByRole('region', { name: 'Access' });
    expect(region.hasAttribute('data-actions')).toBe(true);
    const actions = screen.getByRole('button', { name: 'Make an access' }).parentElement;
    expect(actions?.className).toBe('ds-pane__actions');
    expect(actions?.parentElement).toBe(region);
  });

  it('has no actions box when there are none', () => {
    render(<Pane title="Plain">x</Pane>);
    expect(document.querySelector('.ds-pane__actions')).toBeNull();
    expect(
      screen.getByRole('region', { name: 'Plain' }).hasAttribute('data-actions'),
    ).toBe(false);
  });

  it('uses a level-3 heading and a link in a card grid', () => {
    render(
      <MemoryRouter>
        <Pane title="Runners" level={3} to="/setup/runners">
          x
        </Pane>
      </MemoryRouter>,
    );
    expect(screen.getByRole('heading', { level: 3 })).toBeTruthy();
    expect(screen.getByRole('link', { name: 'Runners' }).getAttribute('href')).toBe(
      '/setup/runners',
    );
  });

  it('is what a Section renders, with its hint as a note inside', () => {
    render(
      <Section title="Budgets" hint="Limits per day.">
        body
      </Section>,
    );
    const region = screen.getByRole('region', { name: 'Budgets' });
    expect(region.className).toBe('ds-pane');
    expect(region.textContent).toContain('Limits per day.');
  });
});

describe('Table selected row', () => {
  const rows = [
    { id: 'a', name: 'one' },
    { id: 'b', name: 'two' },
  ];
  it('makes every row a list row the cursor can reach and marks a ticked row', () => {
    render(
      <Table
        caption="Things"
        columns={[{ id: 'n', header: 'Name', cell: (r) => r.name }]}
        rows={rows}
        rowKey={(r) => r.id}
        selected={new Set(['b'])}
        onToggle={() => undefined}
      />,
    );
    const trs = screen.getAllByRole('row').slice(1);
    expect(trs.every((r) => r.hasAttribute('data-list-row') && r.tabIndex === 0)).toBe(
      true,
    );
    expect(trs[0]?.hasAttribute('data-selected')).toBe(false);
    expect(trs[1]?.hasAttribute('data-selected')).toBe(true);
  });

  it('draws the row under the cursor in reverse video (stylesheet rule)', async () => {
    const { readFileSync } = await import('node:fs');
    const { join } = await import('node:path');
    const css = readFileSync(join(import.meta.dirname, '..', 'components.css'), 'utf8');
    const rule = css.slice(css.indexOf('.ds-table tr[data-list-row]:focus,'));
    const block = rule.slice(0, rule.indexOf('}'));
    expect(block).toContain('background: var(--ds-color-primary)');
    expect(block).toContain('color: var(--ds-color-on-primary)');
  });
});

describe('Tabs', () => {
  const tabs = [
    { id: 'a', label: 'One' },
    { id: 'b', label: 'Two' },
    { id: 'c', label: 'Three' },
  ];
  it('wraps onto as many rows as needed and never scrolls sideways (UX-68)', async () => {
    const { readFileSync } = await import('node:fs');
    const { join } = await import('node:path');
    const css = readFileSync(join(import.meta.dirname, '..', 'components.css'), 'utf8');
    const rule = css.slice(css.indexOf('.ds-tabs {'));
    const block = rule.slice(0, rule.indexOf('}'));
    expect(block).toContain('flex-wrap: wrap');
    expect(block).not.toContain('overflow-x');
    expect(block).not.toContain('nowrap');
    const tab = css.slice(css.indexOf('.ds-tab {'));
    expect(tab.slice(0, tab.indexOf('}'))).not.toContain('nowrap');
    const menu = css.slice(css.indexOf('.ds-topmenu {'));
    expect(menu.slice(0, menu.indexOf('}'))).not.toContain('overflow-x');
  });

  it('moves with the arrow keys and keeps one tab in the tab order', () => {
    const seen: string[] = [];
    render(
      <ArrowNavigation>
        <Tabs label="Parts" tabs={tabs} current="b" onChange={(id) => seen.push(id)} />
      </ArrowNavigation>,
    );
    expect(screen.getAllByRole('tab').filter((t) => t.tabIndex === 0)).toHaveLength(1);
    const [a, , c] = screen.getAllByRole('tab');
    const press = (el: HTMLElement | undefined, key: string) => {
      el?.focus();
      fireEvent.keyDown(el as HTMLElement, { key });
    };
    press(screen.getAllByRole('tab')[1], 'ArrowRight');
    press(a, 'ArrowLeft');
    press(a, 'End');
    expect(seen).toEqual(['c', 'c', 'c']);
    expect(c).toBeDefined();
  });
});
