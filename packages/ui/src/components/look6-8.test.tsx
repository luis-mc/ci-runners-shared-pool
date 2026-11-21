// @vitest-environment jsdom
// LOOK6-8: theme cards, field rows, keypad overlay footer and the file field.
import { act, cleanup, fireEvent, render, screen } from '@testing-library/react';
import { readFileSync } from 'node:fs';
import { join } from 'node:path';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { NumberField } from './Controls';
import { ChoiceField, FileField } from './Fields';
import { fileSize } from './FileField';
import { NumberKeypad } from '../pickers/NumberKeypad';

afterEach(cleanup);
const css = readFileSync(join(import.meta.dirname, '..', 'components.css'), 'utf8');

describe('theme cards (LOOK6)', () => {
  it('each card is one label with a radio, the name and both previews; the grid is 3, 2, 1', () => {
    const { container } = render(
      <ChoiceField
        label="Theme"
        value="a"
        onChange={() => {}}
        choices={[
          { value: 'a', label: 'Alpha', preview: 'default' },
          { value: 'b', label: 'Beta', preview: 'default' },
        ]}
      />,
    );
    expect(container.querySelector('.ds-choice')!.getAttribute('data-cards')).toBe(
      'true',
    );
    const card = container.querySelector('label[data-card]')!;
    expect(card.querySelector('input[type=radio]')).toBeTruthy();
    expect(card.textContent).toContain('Alpha');
    expect(card.querySelectorAll('.ds-theme-preview')).toHaveLength(2);
    expect(css).toContain('repeat(3, minmax(0, 1fr))');
    expect(css).toContain('repeat(2, minmax(0, 1fr))');
    expect(css).toContain('2px solid var(--ds-color-primary)');
  });
});

describe('field rows (LOOK6)', () => {
  it('equal auto-fill columns of 200 px, top-aligned, boxes on one line, one-line labels', () => {
    const i = css.indexOf('.ds-field-grid {');
    const b = css.slice(i, css.indexOf('}', i));
    expect(b).toContain('auto-fill');
    expect(b).toContain('12.5rem');
    expect(b).toContain('align-items: start');
    expect(css).toContain('grid-template-rows: subgrid');
    expect(css).toContain('.ds-field-grid .ds-field__label');
  });
});

describe('keypad overlay (LOOK7)', () => {
  it('Clear sits in the bottom-left key, one title, a range hint, one Cancel and one Done', () => {
    render(
      <NumberField label="Keep backups" value={5} min={1} max={30} onChange={() => {}} />,
    );
    fireEvent.click(screen.getByRole('button', { name: /Keep backups/ }));
    const dialog = screen.getByRole('dialog');
    expect(dialog.getAttribute('data-size')).toBe('keypad');
    const keys = [...dialog.querySelectorAll('.ds-keypad__key')].map(
      (k) => k.textContent,
    );
    expect(keys[9]).toBe('Clear');
    expect(keys[11]).toBe('⌫');
    expect(dialog.querySelectorAll('.ds-keypad__label')).toHaveLength(0);
    expect(dialog.querySelector('.ds-keypad__hint')!.textContent).toBe('From 1 to 30.');
    const names = [...dialog.querySelectorAll('button')].map((b) => b.textContent);
    expect(names.filter((n) => n === 'Cancel')).toHaveLength(1);
    expect(names.filter((n) => n === 'Done')).toHaveLength(1);
    expect(names.indexOf('Cancel')).toBeLessThan(names.indexOf('Done'));
  });

  it('Done in the footer commits the typed value', () => {
    const onChange = vi.fn();
    render(<NumberField label="Keep" value={5} min={1} max={30} onChange={onChange} />);
    fireEvent.click(screen.getByRole('button', { name: /Keep/ }));
    fireEvent.click(document.querySelector('[data-keypad-key="1"]')!);
    fireEvent.click(document.querySelector('[data-keypad-key="2"]')!);
    fireEvent.click(screen.getByRole('button', { name: 'Done' }));
    expect(onChange).toHaveBeenCalledWith(12);
  });

  it('a stand-alone keypad keeps its own title and footer', () => {
    const { container } = render(
      <NumberKeypad label="Reps" onDone={() => {}} onCancel={() => {}} />,
    );
    expect(container.querySelector('.ds-keypad__label')).toBeTruthy();
    expect(container.querySelectorAll('.ds-keypad__action')).toHaveLength(2);
  });
});

describe('FileField (LOOK8)', () => {
  const pem = (name = 'key.pem', body = 'x'.repeat(2048)) =>
    new File([body], name, { type: 'application/x-pem-file' });

  it('empty: a drop zone and a button; the real input is wired to the label', () => {
    render(<FileField label="Key file" help="From GitHub." onChange={() => {}} />);
    expect(screen.getByText('Drop the file here, or')).toBeTruthy();
    expect(screen.getByRole('button', { name: 'Choose a file' })).toBeTruthy();
    const input = screen.getByLabelText<HTMLInputElement>('Key file');
    expect(input.type).toBe('file');
    expect(input.getAttribute('aria-describedby')).toBeTruthy();
  });

  it('chosen: name, size, Change and remove; remove clears', () => {
    const onChange = vi.fn();
    render(<FileField label="Key file" accept=".pem" onChange={onChange} />);
    fireEvent.change(screen.getByLabelText('Key file'), { target: { files: [pem()] } });
    expect(screen.getByText('key.pem')).toBeTruthy();
    expect(screen.getByText('2 KB')).toBeTruthy();
    expect(screen.getByRole('button', { name: 'Change' })).toBeTruthy();
    fireEvent.click(screen.getByRole('button', { name: 'Remove key.pem' }));
    expect(onChange).toHaveBeenLastCalledWith(null);
    expect(screen.getByText('Drop the file here, or')).toBeTruthy();
  });

  it('refused: the caller reason shows and the choice is cleared', () => {
    const onChange = vi.fn();
    const { container } = render(
      <FileField
        label="Key file"
        onChange={onChange}
        validate={(f) => (f.size < 10 ? 'The file is empty.' : undefined)}
      />,
    );
    fireEvent.change(screen.getByLabelText('Key file'), {
      target: { files: [pem('a.pem', 'x')] },
    });
    expect(screen.getByText('The file is empty.')).toBeTruthy();
    expect(container.querySelector('.ds-file')!.getAttribute('data-state')).toBe(
      'refused',
    );
    expect(onChange).toHaveBeenLastCalledWith(null);
  });

  it('refuses a drop outside the accept filter, takes a matching one', () => {
    const onChange = vi.fn();
    const { container } = render(
      <FileField label="Key file" accept=".pem" onChange={onChange} />,
    );
    const zone = container.querySelector('.ds-file')!;
    act(() => {
      fireEvent.drop(zone, { dataTransfer: { files: [pem('a.txt')] } });
    });
    expect(screen.getByText('Choose a .pem file.')).toBeTruthy();
    fireEvent.drop(zone, { dataTransfer: { files: [pem('b.pem')] } });
    expect(onChange).toHaveBeenLastCalledWith(expect.objectContaining({ name: 'b.pem' }));
  });

  it('sizes read in words', () => {
    expect(fileSize(12)).toBe('12 B');
    expect(fileSize(1536)).toBe('1.5 KB');
    expect(fileSize(3 * 1024 * 1024)).toBe('3 MB');
  });
});
