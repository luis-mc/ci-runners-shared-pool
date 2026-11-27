import { existsSync, readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { describe, expect, it } from 'vitest';

// TRAY5 D18: the console tabs and the web update page show the crsp icon, not the browser's globe.
describe('index.html tab icon', () => {
  const root = resolve(__dirname, '..');
  const html = readFileSync(resolve(root, 'index.html'), 'utf8');

  it('links the crsp icon', () => {
    const m = /<link rel="icon" type="image\/png" href="\/([^"]+)"/.exec(html);
    expect(m).not.toBeNull();
    expect(existsSync(resolve(root, 'public', m![1]!))).toBe(true);
  });
});
