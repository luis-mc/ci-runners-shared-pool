// Test helper: queries inside one section of a Settings tab (a tab holds several sections).
import { within } from '@testing-library/react';

export function section(id: string) {
  const el = document.getElementById(id);
  if (!el) throw new Error(`No Settings section ${id} on the page`);
  return within(el);
}
