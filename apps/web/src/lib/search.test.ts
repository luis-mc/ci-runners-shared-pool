// Docs search (website R5.19, T8): the index and the matching are plain functions.
import { describe, expect, it } from 'vitest';
import { buildIndex, search, tokenize } from './search.ts';

const index = buildIndex([
  {
    path: '/docs/a',
    title: 'Install guide',
    headings: ['Checksum'],
    text: 'Download and install the controller. constructor toString',
  },
  {
    path: '/docs/b',
    title: 'Scheduling',
    headings: ['Slots'],
    text: 'A machine has slots. Install is not here but the word controller is.',
  },
]);

describe('docs search', () => {
  it('finds a known doc and ranks a title hit first', () => {
    expect(search(index, 'install')).toEqual(['/docs/a', '/docs/b']);
    expect(search(index, 'sched')).toEqual(['/docs/b']);
  });
  it('needs every word and matches by the start of a word', () => {
    expect(search(index, 'install controller')).toEqual(['/docs/a', '/docs/b']);
    expect(search(index, 'install slots')).toEqual(['/docs/b']);
    expect(search(index, 'zzz')).toEqual([]);
    expect(search(index, '')).toEqual([]);
  });
  it('handles words that are names on every object', () => {
    expect(search(index, 'constructor')).toEqual(['/docs/a']);
    expect(tokenize('A-B 12 x')).toEqual(['12']);
  });
});
