// CRSP373: at phone width the header status text wraps in its own column; it never runs under the Commands button.
import { readFileSync } from 'node:fs';
import { join } from 'node:path';
import { describe, expect, it } from 'vitest';

const css = readFileSync(join(import.meta.dirname, '..', 'components.css'), 'utf8');

describe('header on a phone', () => {
  it('lets a status in the header wrap instead of staying on one line', () => {
    const phone = css.slice(css.indexOf('@media (max-width: 599px)'));
    const rule = /\.ds-header__state \.ds-status \{([^}]*)\}/.exec(phone);
    expect(rule).not.toBeNull();
    expect(rule![1]).toMatch(/white-space:\s*normal/);
    expect(rule![1]).toMatch(/min-inline-size:\s*0/);
  });
});
