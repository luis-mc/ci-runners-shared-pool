// UX-2 (LOOK35): the top menu marks the open item by aria-current only; no class `active` that no rule styles.
import { cleanup, render, screen } from '@testing-library/react';
import { MemoryRouter } from 'react-router-dom';
import { afterEach, describe, expect, it } from 'vitest';
import { TopMenu } from './Shell';

afterEach(cleanup);

describe('TopMenu', () => {
  it('marks the open item with aria-current and never adds the class active', () => {
    render(
      <MemoryRouter initialEntries={['/pool']}>
        <TopMenu
          items={[
            { to: '/', label: 'Overview', end: true },
            { to: '/pool', label: 'Pool' },
          ]}
        />
      </MemoryRouter>,
    );
    const open = screen.getByRole('link', { name: 'Jobs' });
    const other = screen.getByRole('link', { name: 'Overview' });
    expect(open.getAttribute('aria-current')).toBe('page');
    expect(other.getAttribute('aria-current')).toBeNull();
    expect(open.className).toBe('ds-topmenu__link');
    expect(other.className).toBe('ds-topmenu__link');
  });
});
