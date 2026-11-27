// Settings, Repositories: cloud overflow (github-integration R5.114, CRSP281). The owner puts the targets a queued
// job may go to in order (GitHub-hosted runners, AWS EC2 runners), turns each on, names its label, and may set
// the minutes a repository may use there each month. Shared parts only (UX-2, UX-22, UX-71); the words are in
// design system `next-version/content.md` (Cloud overflow words). The tab's one Save saves it.
import {
  CheckRow,
  ErrorSummary,
  FieldGrid,
  Message,
  NumberField,
  OrderList,
  Pane,
  Section,
  Stack,
  Text,
  TextField,
} from '@crsp/ui';
import { useState } from 'react';
import { unwrap, useClient, type Schemas } from '../../api/api';
import { useCommand, useLoad } from '../../api/hooks';
import { useSession } from '../../auth/session';
import { Load } from '../common';
import { useTabPart } from './TabForm';

type Target = Schemas['OverflowTarget'];
type Kind = Target['kind'];
type System = NonNullable<Target['systems']>[number];

const KINDS: { id: Kind; label: string }[] = [
  { id: 'github_hosted', label: 'GitHub-hosted runners' },
  { id: 'aws_ec2', label: 'AWS EC2 runners' },
];
const SYSTEMS: { id: System; label: string }[] = [
  { id: 'linux', label: 'Linux' },
  { id: 'windows', label: 'Windows' },
  { id: 'macos', label: 'macOS' },
];
const NAME = Object.fromEntries(KINDS.map((k) => [k.id, k.label])) as Record<
  Kind,
  string
>;

/** The saved list, with a row for each kind: the saved order first, a missing kind last and off. */
export function completeTargets(saved: readonly Target[] | undefined): Target[] {
  const out = [...(saved ?? [])];
  for (const k of KINDS)
    if (!out.some((t) => t.kind === k.id)) out.push({ kind: k.id, enabled: false });
  return out.map((t) => ({
    kind: t.kind,
    enabled: t.enabled,
    label: t.label ?? '',
    cap_minutes: t.cap_minutes ?? 0,
    systems: t.systems ?? [],
  }));
}

/** Systems the target serves; an empty list means all. */
const serves = (t: Target, s: System) => !t.systems?.length || t.systems.includes(s);

/** The mistakes of the form, each in one sentence, before anything is sent. */
export function overflowProblems(targets: readonly Target[]): string[] {
  const out: string[] = [];
  for (const t of targets) {
    const label = (t.label ?? '').trim();
    if (/[\s,"\\]/.test(label) || label.length > 100)
      out.push(
        `The label of ${NAME[t.kind]} must be one label of up to 100 characters, without spaces, commas or quotes.`,
      );
    else if (t.kind === 'aws_ec2' && t.enabled && !label)
      out.push('AWS EC2 runners need a label while they are on.');
    const cap = t.cap_minutes ?? 0;
    if (!Number.isInteger(cap) || cap < 0 || cap > 1000000)
      out.push(`Minutes of ${NAME[t.kind]} must be from 0 to 1000000.`);
  }
  return out;
}

const same = (a: readonly Target[], b: readonly Target[]) =>
  JSON.stringify(a) === JSON.stringify(b);

export function OverflowSettings() {
  const client = useClient();
  const session = useSession();
  const { result, reload } = useLoad(() => unwrap(client.GET('/api/v1/settings')), []);
  return (
    <Section title="Cloud overflow">
      <Load result={result} what="cloud overflow" reload={reload}>
        {(s) => (
          <OverflowForm
            key={JSON.stringify(s.overflow_targets ?? [])}
            saved={completeTargets(s.overflow_targets)}
            onSaved={reload}
            canEdit={session.can('owner')}
            why={session.whyNot('owner')}
          />
        )}
      </Load>
    </Section>
  );
}

function OverflowForm({
  saved,
  onSaved,
  canEdit,
  why,
}: {
  saved: Target[];
  onSaved: () => void;
  canEdit: boolean;
  why: string | undefined;
}) {
  const client = useClient();
  const [targets, setTargets] = useState<Target[]>(saved);
  const [errors, setErrors] = useState<string[]>([]);
  const [done, setDone] = useState(false);
  const change = (kind: Kind, patch: Partial<Target>) =>
    setTargets(targets.map((t) => (t.kind === kind ? { ...t, ...patch } : t)));
  const was = (kind: Kind) => saved.find((t) => t.kind === kind);
  const changedField = (t: Target, f: keyof Target) =>
    JSON.stringify(was(t.kind)?.[f]) !== JSON.stringify(t[f]);
  const orderChanged =
    saved.map((t) => t.kind).join() !== targets.map((t) => t.kind).join();
  const changes =
    targets.reduce(
      (n, t) =>
        n +
        (['enabled', 'label', 'cap_minutes', 'systems'] as const).filter((f) =>
          changedField(t, f),
        ).length,
      0,
    ) + (orderChanged ? 1 : 0);
  const save = useCommand(async () => {
    setDone(false);
    const problems = overflowProblems(targets);
    setErrors(problems);
    if (problems.length) return false;
    await unwrap(
      client.PATCH('/api/v1/settings', {
        body: {
          overflow_targets: targets.map((t) => ({ ...t, label: (t.label ?? '').trim() })),
        },
      }),
    );
    setDone(true);
    onSaved();
    return true;
  });
  useTabPart('overflow', {
    changes: same(saved, targets) ? 0 : changes,
    save: async () => (await save.run()) === true,
    reset: () => {
      setTargets(saved);
      setErrors([]);
      setDone(false);
      save.clear();
    },
    blocked: canEdit ? undefined : (why ?? 'Only the owner can change this.'),
  });
  return (
    <Stack>
      <Text>
        When no crsp machine can take the jobs of a repository that turned its fallback
        on, they go to the first target below that is on, serves the system and has
        minutes left. A target with no minutes left is skipped. This can cost money.
      </Text>
      <OrderList
        label="Overflow order"
        items={KINDS}
        value={targets.map((t) => t.kind)}
        onChange={(order) =>
          setTargets(order.map((k) => targets.find((t) => t.kind === k) as Target))
        }
      />
      {targets.map((t) => (
        <Pane key={t.kind} title={NAME[t.kind]} level={3}>
          <Stack>
            <CheckRow
              label={`Use ${NAME[t.kind]}`}
              checked={t.enabled}
              changed={changedField(t, 'enabled')}
              onChange={(v) => change(t.kind, { enabled: v })}
            />
            <FieldGrid>
              <TextField
                label={`Label of ${NAME[t.kind]}`}
                help={
                  t.kind === 'github_hosted'
                    ? 'Leave it empty for ubuntu-latest, windows-latest and macos-latest.'
                    : 'The label your EC2 runners carry in the workflow.'
                }
                value={t.label ?? ''}
                changed={changedField(t, 'label')}
                mono
                onChange={(v) => change(t.kind, { label: v })}
              />
              <NumberField
                label={`Minutes of ${NAME[t.kind]} per repository and month`}
                help="0 means no limit. When a repository has used them, the next target is tried."
                value={t.cap_minutes ?? 0}
                changed={changedField(t, 'cap_minutes')}
                unit=" min"
                min={0}
                max={1000000}
                onChange={(v) => change(t.kind, { cap_minutes: v })}
              />
            </FieldGrid>
            {SYSTEMS.map((s) => (
              <CheckRow
                key={s.id}
                label={`${NAME[t.kind]} serve ${s.label}`}
                checked={serves(t, s.id)}
                onChange={(v) => {
                  const now = SYSTEMS.map((x) => x.id).filter((id) =>
                    id === s.id ? v : serves(t, id),
                  );
                  change(t.kind, { systems: now.length === SYSTEMS.length ? [] : now });
                }}
              />
            ))}
          </Stack>
        </Pane>
      ))}
      <ErrorSummary errors={errors} />
      {done ? <Message tone="done">Cloud overflow saved.</Message> : null}
      {save.error ? <Message tone="problem">Could not save. {save.error}</Message> : null}
    </Stack>
  );
}
