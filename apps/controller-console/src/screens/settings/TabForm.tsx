// One form per Settings tab (owner decision D26, UX-71): the sections of a tab that hold settings register what
// they changed here, and the one action bar at the foot of the tab says "Unsaved changes (N)" and carries one Save
// for the whole tab. A section that is not saving (Add channel, Choose an address, Back up now) keeps a normal
// button inside its own section.
import { EditBar, Stack } from '@crsp/ui';
import {
  createContext,
  useCallback,
  useContext,
  useEffect,
  useRef,
  useState,
  type ReactNode,
} from 'react';

export interface TabPart {
  /** How many things the person changed in this section; 0 when clean. */
  changes: number;
  /** Saves the section. True when it saved; false when it did not (the section shows why itself). */
  save: () => Promise<boolean>;
  /** Puts the section back to what is saved. */
  reset: () => void;
  /** Why this section cannot be saved by this person (a role), if so. */
  blocked?: string | undefined;
}

type Register = (id: string, part: TabPart | null) => void;
const Registry = createContext<Register | undefined>(undefined);

/** A section of a Settings tab hands over its state; the tab's bar does the saving. */
export function useTabPart(id: string, part: TabPart): void {
  const register = useContext(Registry);
  useEffect(() => {
    register?.(id, part);
  });
  useEffect(() => () => register?.(id, null), [register, id]);
}

export function TabForm({ children }: { children: ReactNode }) {
  const parts = useRef(new Map<string, TabPart>());
  const [, bump] = useState(0);
  const [busy, setBusy] = useState(false);
  const register = useCallback<Register>((id, part) => {
    const before = parts.current.get(id);
    if (part) parts.current.set(id, part);
    else parts.current.delete(id);
    if (before?.changes !== part?.changes || before?.blocked !== part?.blocked)
      bump((n) => n + 1);
  }, []);
  const dirty = [...parts.current.values()].filter((p) => p.changes > 0);
  const total = dirty.reduce((n, p) => n + p.changes, 0);
  const blocked = dirty.find((p) => p.blocked)?.blocked;
  const saveAll = async () => {
    if (busy || total === 0 || blocked) return;
    setBusy(true);
    try {
      for (const p of [...parts.current.values()]) {
        if (p.changes === 0) continue;
        if (!(await p.save())) break;
      }
    } finally {
      setBusy(false);
    }
  };
  return (
    <Registry.Provider value={register}>
      <form
        onSubmit={(e) => {
          e.preventDefault();
          void saveAll();
        }}
      >
        <Stack gap="section">
          {children}
          <EditBar
            changes={total}
            onSave={() => void saveAll()}
            onCancel={() => parts.current.forEach((p) => p.reset())}
            busy={busy}
            blocked={Boolean(blocked)}
            why={blocked}
          />
        </Stack>
      </form>
    </Registry.Provider>
  );
}
