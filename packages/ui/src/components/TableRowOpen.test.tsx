// CRSP391: a row that opens its details by itself (detail.rowOpen) and the two-line phone fold (fold="lines").
import { cleanup, fireEvent, render, screen } from '@testing-library/react';
import { afterEach, describe, expect, it } from 'vitest';
import { Table, type Column } from './Table';

afterEach(cleanup);

interface Row {
  id: string;
  name: string;
}
const columns: Column<Row>[] = [
  { id: 'name', header: 'Job', cell: (r) => r.name, line: 1 },
  { id: 'labels', header: 'Labels', cell: () => 'x', line: 2 },
];
const rows: Row[] = [
  { id: 'a', name: 'alpha' },
  { id: 'b', name: 'beta' },
];
function show(rowOpen: boolean, fold?: 'lines') {
  const { container } = render(
    <Table
      caption="Queue"
      columns={columns}
      rows={rows}
      rowKey={(r) => r.id}
      rowLabel={(r) => r.name}
      fold={fold}
      detail={{ label: 'Details', rowOpen, render: (r) => <p>more about {r.name}</p> }}
    />,
  );
  return container;
}

describe('Table detail.rowOpen', () => {
  it('has no button column; the row opens by click and by Enter and says it is expandable', () => {
    show(true);
    expect(screen.queryByRole('button', { name: /details/i })).toBeNull();
    const row = screen.getByText('alpha').closest('tr') as HTMLElement;
    expect(row.getAttribute('aria-expanded')).toBe('false');
    fireEvent.click(row);
    expect(row.getAttribute('aria-expanded')).toBe('true');
    expect(screen.getByText('more about alpha')).toBeTruthy();
    fireEvent.keyDown(row, { key: 'Enter' });
    expect(row.getAttribute('aria-expanded')).toBe('false');
    expect(screen.queryByText('more about alpha')).toBeNull();
  });

  it('without rowOpen the Details button stays and the row is not marked expandable', () => {
    show(false);
    expect(screen.getAllByRole('button', { name: /details/i }).length).toBe(2);
    const row = screen.getByText('alpha').closest('tr') as HTMLElement;
    expect(row.getAttribute('aria-expanded')).toBeNull();
  });
});

describe('Table fold="lines"', () => {
  it('marks the cells with their phone line and no table keeps it by default', () => {
    const on = show(true, 'lines');
    expect(on.querySelector('table')?.getAttribute('data-fold')).toBe('lines');
    expect(on.querySelector('td[data-line="2"]')).not.toBeNull();
    cleanup();
    const off = show(true);
    expect(off.querySelector('table')?.getAttribute('data-fold')).toBeNull();
    expect(off.querySelector('td[data-line]')).toBeNull();
  });
});

describe('Table compact', () => {
  it('is marked only with oneLine and compact together', () => {
    const base = { caption: 'Q', columns, rows, rowKey: (r: Row) => r.id };
    const a = render(<Table {...base} oneLine compact />).container;
    expect(a.querySelector('table')?.getAttribute('data-compact')).toBe('true');
    cleanup();
    const b = render(<Table {...base} compact />).container;
    expect(b.querySelector('table')?.getAttribute('data-compact')).toBeNull();
    cleanup();
    const c = render(<Table {...base} oneLine />).container;
    expect(c.querySelector('table')?.getAttribute('data-compact')).toBeNull();
  });
});
