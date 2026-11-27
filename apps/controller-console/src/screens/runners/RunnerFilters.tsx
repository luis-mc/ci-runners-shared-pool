// The Runners filter bar (CRSP339, the owner's pick of option 3 with the rows of option 2). One row: search, Filters
// with the number set, the selection words. On a wide screen Filters opens the four joined-choice rows in the page; on
// a phone it opens a bottom sheet with the four choices and "Show N runners" (component 9, UX-35). The page passes
// the state and the setter; the shared parts do the layout (UX-7, UX-8, UX-11, UX-16, UX-22).
import {
  Button,
  Cluster,
  Dialog,
  ListToolbar,
  SearchBar,
  Segmented,
  SelectField,
  Stack,
  Tag,
  Text,
} from '@crsp/ui';
import { useId, useState, type ReactNode } from 'react';
import { useNarrow } from '../../shell/useNarrow';

export interface FilterState {
  q: string;
  os: string;
  iso: string;
  kind: string;
  size: string;
}

const GROUPS = [
  {
    key: 'os',
    name: 'System',
    any: 'Any system',
    options: [
      { value: 'linux', label: 'Linux' },
      { value: 'windows', label: 'Windows' },
      { value: 'macos', label: 'macOS' },
    ],
  },
  {
    key: 'iso',
    name: 'Isolation',
    any: 'Any isolation',
    options: [
      { value: 'container', label: 'container' },
      { value: 'VM', label: 'VM' },
      { value: 'microVM', label: 'microVM' },
    ],
  },
  {
    key: 'kind',
    name: 'Kind',
    any: 'Any kind',
    options: [
      { value: 'persistent', label: 'persistent' },
      { value: 'ephemeral', label: 'ephemeral' },
    ],
  },
  {
    key: 'size',
    name: 'Size',
    any: 'Any size',
    options: [
      { value: 'small', label: 'small' },
      { value: 'medium', label: 'medium' },
      { value: 'large', label: 'large' },
      { value: 'xl', label: 'xl' },
    ],
  },
] as const;

type Key = (typeof GROUPS)[number]['key'];

/** The filters that are set, as the words of their tags. */
export function activeFilters(state: FilterState): { key: Key; text: string }[] {
  return GROUPS.flatMap((g) => {
    const o = g.options.find((x) => x.value === state[g.key]);
    return o ? [{ key: g.key, text: `${g.name}: ${o.label}` }] : [];
  });
}

export function RunnerFilterBar(p: {
  state: FilterState;
  set: (key: 'q' | Key, value: string) => void;
  /** How many runners the filters show now. */
  shown: number;
  /** Select all N, Clear, "N selected" and the buttons that act on the selection. */
  selection: ReactNode;
}) {
  const narrow = useNarrow();
  const [open, setOpen] = useState(false);
  const panel = useId();
  const active = activeFilters(p.state);
  const clear = () => {
    for (const g of GROUPS) p.set(g.key, '');
  };
  return (
    <Stack>
      <ListToolbar
        label="Runners"
        search={
          <SearchBar
            value={p.state.q}
            onChange={(v) => p.set('q', v)}
            label="Search runners"
          />
        }
        filters={
          <Button
            onPress={() => setOpen(!open)}
            {...(narrow ? {} : { expanded: open, controls: panel })}
          >
            {active.length > 0 ? `Filters (${active.length})` : 'Filters'}
          </Button>
        }
        selection={p.selection}
      />
      {!narrow && open ? (
        <div id={panel}>
          <Cluster gap="loose">
            {GROUPS.map((g) => (
              <Stack key={g.key} gap="tight">
                <Text tone="muted">{g.name}</Text>
                <Segmented
                  label={g.name}
                  options={g.options}
                  value={p.state[g.key]}
                  onChange={(v) => p.set(g.key, v)}
                />
              </Stack>
            ))}
          </Cluster>
        </div>
      ) : null}
      {active.length > 0 && !(open && !narrow) ? (
        <Cluster>
          {active.map((a) => (
            <Tag key={a.key}>{a.text}</Tag>
          ))}
          <Button onPress={clear}>Clear filters</Button>
        </Cluster>
      ) : null}
      {narrow && open ? (
        <Dialog
          title="Filter runners"
          cancelLabel="Clear filters"
          onClose={() => {
            clear();
            setOpen(false);
          }}
          primary={{
            label: `Show ${p.shown} ${p.shown === 1 ? 'runner' : 'runners'}`,
            onPress: () => setOpen(false),
          }}
        >
          <Stack>
            {GROUPS.map((g) => (
              <SelectField
                key={g.key}
                label={g.name}
                width="full"
                value={p.state[g.key]}
                choices={[{ value: '', label: g.any }, ...g.options]}
                onChange={(v) => p.set(g.key, v)}
              />
            ))}
          </Stack>
        </Dialog>
      ) : null}
    </Stack>
  );
}
