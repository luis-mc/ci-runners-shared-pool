import { readFileSync } from 'node:fs';
import { join } from 'node:path';
import { describe, expect, it } from 'vitest';
import {
  countBreaches,
  judgeBreaches,
  scanApps,
  scanText,
  type KnownEntry,
} from './action-bar-scan.ts';

// One action bar (design system UX-71, UX-4). A screen draws its primary button inside the shared action bar and
// draws one. Today's breaches are the known list `action-bar.known.json` (file, kind, count). It is a ratchet:
// a new breach fails, and a listed breach that is gone but still listed fails too.
const root = join(import.meta.dirname, '..', '..');
const known = (
  JSON.parse(
    readFileSync(join(import.meta.dirname, 'action-bar.known.json'), 'utf8'),
  ) as {
    entries: KnownEntry[];
  }
).entries;

describe('one action bar for the main action', () => {
  it('no new primary outside the action bar, and no listed one that is already gone (the list only shrinks)', () => {
    const { added, stale } = judgeBreaches(countBreaches(scanApps(root)), known);
    expect(added, 'a primary button outside <ActionBar primary={...}>').toEqual([]);
    expect(stale, 'fixed breach still listed: shrink the known list').toEqual([]);
  });

  describe('the scan itself', () => {
    const file = 'apps/controller-console/src/screens/a.tsx';
    const count = (text: string) => scanText(file, text);
    it('a planted primary outside the bar fails', () => {
      const actual = countBreaches(count('<Button kind="primary">Go</Button>'));
      expect(judgeBreaches(actual, []).added).toHaveLength(1);
    });
    it('a primary inside the bar passes', () => {
      expect(count('<ActionBar primary={<Button kind="primary">Go</Button>} />')).toEqual(
        [],
      );
    });
    it('a conditional primary inside the bar passes, outside it fails', () => {
      expect(
        count(
          "<ActionBar primary={<Button kind={x ? 'primary' : 'default'}>Go</Button>} />",
        ),
      ).toEqual([]);
      expect(count("<Button kind={x ? 'primary' : 'default'}>Go</Button>")).toHaveLength(
        1,
      );
    });
    it('two primaries in one bar fail', () => {
      expect(
        count(
          '<ActionBar primary={<><Button kind="primary">A</Button><Button kind="primary">B</Button></>} />',
        ).map((b) => b.kind),
      ).toEqual(['many']);
    });
    it('a primary next to the bar, in the secondary slot, fails', () => {
      expect(
        count(
          '<ActionBar secondary={<Button kind="primary">A</Button>} primary={<Button kind="primary">B</Button>} />',
        ).map((b) => b.kind),
      ).toEqual(['outside']);
    });
    it('tests and shared UI are not judged', () => {
      expect(
        scanText('apps/controller-console/src/a.test.tsx', '<Button kind="primary"/>'),
      ).toEqual([]);
      expect(scanText('packages/ui/src/a.tsx', '<Button kind="primary"/>')).toEqual([]);
    });
    it('a listed breach passes', () => {
      const entry = { file, kind: 'outside', count: 1 };
      const actual = countBreaches(count('<Button kind="primary">Go</Button>'));
      expect(judgeBreaches(actual, [entry])).toEqual({ added: [], stale: [] });
    });
    it('a listed breach that is gone is stale', () => {
      const entry = { file, kind: 'outside', count: 1 };
      expect(judgeBreaches(countBreaches(count('<p/>')), [entry]).stale).toHaveLength(1);
    });
  });
});
