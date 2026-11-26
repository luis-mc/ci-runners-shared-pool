// CRSP299: three design options for the Runners filter bar, switched by the temporary query ?option=1..3.
// Shared parts only (UX-7, UX-8, UX-11, UX-16, UX-22; CORE-1..7). Removed once the owner picks one.
import {
  Button,
  Cluster,
  Dialog,
  FieldGrid,
  ListToolbar,
  SearchBar,
  Segmented,
  SelectField,
  Stack,
  Tag,
  Text,
} from '@crsp/ui';
import { useState, type ReactNode } from 'react';

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

export interface BarProps {
  state: FilterState;
  set: (key: 'q' | Key, value: string) => void;
  shown: number;
  selection: ReactNode;
}

function search(p: BarProps) {
  return (
    <SearchBar value={p.state.q} onChange={(v) => p.set('q', v)} label="Search runners" />
  );
}

/** Option 1, "Drop-downs": the four filters are drop-downs side by side in one grid; search and selection stay in the toolbar. */
export function DropDownBar(p: BarProps) {
  return (
    <ListToolbar
      label="Runners"
      search={search(p)}
      filters={
        <FieldGrid>
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
        </FieldGrid>
      }
      selection={p.selection}
    />
  );
}

/** Option 2, "Joined choices": each filter is a joined button row (All first, component 12), named above. */
export function JoinedBar(p: BarProps) {
  return (
    <ListToolbar
      label="Runners"
      search={search(p)}
      filters={
        <FieldGrid>
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
        </FieldGrid>
      }
      selection={p.selection}
    />
  );
}

/** Option 3, "Filter sheet": search, one Filters button with the active count, active filters as tags, the choices in a sheet (component 11, UX-35). */
export function SheetBar(p: BarProps) {
  const [open, setOpen] = useState(false);
  const active = GROUPS.flatMap((g) => {
    const o = g.options.find((x) => x.value === p.state[g.key]);
    return o ? [{ key: g.key, text: `${g.name}: ${o.label}` }] : [];
  });
  const clear = () => {
    for (const g of GROUPS) p.set(g.key, '');
  };
  return (
    <Stack>
      <ListToolbar
        label="Runners"
        search={search(p)}
        filters={
          <Button onPress={() => setOpen(true)}>
            {active.length > 0 ? `Filters (${active.length})` : 'Filters'}
          </Button>
        }
        selection={p.selection}
      />
      {active.length > 0 ? (
        <Cluster>
          {active.map((a) => (
            <Tag key={a.key}>{a.text}</Tag>
          ))}
          <Button onPress={clear}>Clear filters</Button>
        </Cluster>
      ) : null}
      {open ? (
        <Dialog
          title="Filter runners"
          cancelLabel="Clear filters"
          onClose={() => {
            clear();
            setOpen(false);
          }}
          primary={{ label: `Show ${p.shown} runners`, onPress: () => setOpen(false) }}
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

/** Picks the design of ?option=1..3. */
export function OptionBar({ which, ...rest }: BarProps & { which: '1' | '2' | '3' }) {
  if (which === '1') return <DropDownBar {...rest} />;
  if (which === '2') return <JoinedBar {...rest} />;
  return <SheetBar {...rest} />;
}
