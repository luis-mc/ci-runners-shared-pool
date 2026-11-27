// When this machine works (machines R5.61 to R5.64): the mode, the battery pause and the CPU cap.
// Every control is saved at once (UX-22) and follows the tray: both write the same settings.
import {
  Button,
  ChoiceField,
  Cluster,
  Facts,
  Message,
  Section,
  Stack,
  Status,
  Switch,
  Text,
  formatDateTime,
} from '@crsp/ui';
import { useEffect } from 'react';
import { unwrap, useClient, type Schemas } from '../../api/api';
import { useCommand } from '../../api/hooks';
import { useSession } from '../../auth/session';

type Machine = Schemas['Machine'];
type Settings = Schemas['MachineSettingsUpdate'];

const MODES = [
  { value: 'automatic', label: 'Automatic' },
  { value: 'always', label: 'Always' },
  { value: 'paused', label: 'Paused' },
] as const;

const MODE_HELP: Record<string, string> = {
  automatic: 'Takes jobs. Pauses on battery and while you have paused it from the tray.',
  always: 'Takes jobs on battery too. A pause from the tray still holds.',
  paused: 'Takes no new job. The jobs that run finish.',
};

const CAPS = [25, 50, 75, 100] as const;

type Reason = NonNullable<Machine['taking']['reason']>;

/** The words of the one computed state (OPS29): the word, the status kind and the note. Every part of the page reads this and nothing else. */
const TAKING: Record<
  Reason,
  { word: string; kind: 'ok' | 'waiting' | 'off'; note: string }
> = {
  offline: { word: 'Offline', kind: 'off', note: '' },
  stopped: {
    word: 'Not taking jobs',
    kind: 'off',
    note: 'You stopped new jobs from the console. The jobs that run finish. Resume takes jobs again.',
  },
  mode_paused: {
    word: 'Paused',
    kind: 'off',
    note: 'The mode is Paused. It takes no new job. The jobs that run finish.',
  },
  tray_paused: {
    word: 'Paused',
    kind: 'off',
    note: 'It was paused from the tray. It takes no new job. The jobs that run finish.',
  },
  on_battery: {
    word: 'On battery',
    kind: 'off',
    note: 'It takes no new job while it runs on battery. The jobs that run finish.',
  },
  busy: {
    word: 'Busy',
    kind: 'waiting',
    note: 'Its load is over the budget. A new job waits until the load drops.',
  },
  base_image_missing: {
    word: 'Needs a base image',
    kind: 'off',
    note: 'A backend it may use has no runner base image on the machine.',
  },
  disk_low: {
    word: 'Disk low',
    kind: 'off',
    note: 'Its disk is almost full. It takes no new job until there is room.',
  },
};

export interface TakingState {
  taking: boolean;
  reason: Reason | undefined;
  word: string;
  kind: 'ok' | 'waiting' | 'off';
  note: string;
}

/** The one state of a machine: taking jobs, or why not (UX-58). It reads the field the controller computes. */
export function takingState(m: Machine): TakingState {
  const reason = m.taking.taking_jobs ? undefined : (m.taking.reason ?? 'stopped');
  if (!reason) return { taking: true, reason, word: 'Taking jobs', kind: 'ok', note: '' };
  return { taking: false, reason, ...TAKING[reason] };
}

/** The state word of a machine for the list and the detail. */
export function PolicyStatus({ m }: { m: Machine }) {
  const t = takingState(m);
  return <Status kind={t.kind} word={t.word} />;
}

export function MachinePolicySection({
  m,
  onChanged,
}: {
  m: Machine;
  onChanged: () => void;
}) {
  const client = useClient();
  const session = useSession();
  const why = session.whyNot('admin');
  const s = m.settings;
  const mode = s.mode ?? 'automatic';
  const cap = s.cpu_cap_pct ?? 100;
  const policy = m.policy;
  const heldByUser = Boolean(s.pause_until_resumed || s.pause_until);
  const capUnsupported = m.os === 'macos' || policy?.cpu_cap_state === 'not_supported';

  // The agent answers after the setting reaches it: read again until the state settles.
  const applying = policy?.cpu_cap_state === 'applying';
  useEffect(() => {
    if (!applying) return;
    const t = setInterval(onChanged, 2000);
    return () => clearInterval(t);
  }, [applying, onChanged]);

  const change = useCommand(async (body: Settings) => {
    await unwrap(
      client.PATCH('/api/v1/machines/{machineId}', {
        params: { path: { machineId: m.id } },
        body,
      }),
    );
    onChanged();
  });
  const resume = useCommand(async () => {
    const body: Settings = { pause: 'none' };
    if (mode === 'paused') body.mode = 'automatic';
    await unwrap(
      client.PATCH('/api/v1/machines/{machineId}', {
        params: { path: { machineId: m.id } },
        body,
      }),
    );
    onChanged();
  });

  const t = takingState(m);
  return (
    <Section title="When it works">
      <Stack>
        <Facts
          items={[
            { name: 'Now', value: <PolicyStatus m={m} /> },
            ...(s.pause_until
              ? [{ name: 'Paused until', value: formatDateTime(s.pause_until) }]
              : []),
            ...(s.pause_until_resumed
              ? [{ name: 'Paused', value: 'Until you resume' }]
              : []),
            ...(policy?.battery_known
              ? [
                  {
                    name: 'Power',
                    value: policy.on_battery ? 'On battery' : 'On mains',
                  },
                ]
              : []),
          ]}
        />
        {t.note ? (
          <Message tone="note" dismissible={false}>
            {t.note}
          </Message>
        ) : null}
        <ChoiceField
          label="Mode"
          help={MODE_HELP[mode]}
          value={mode}
          choices={MODES.map((c) => ({
            ...c,
            ...(!session.can('admin') ? { disabledReason: why } : {}),
          }))}
          onChange={(v) => void change.run({ mode: v as Schemas['MachineMode'] })}
        />
        <Switch
          label="Pause on battery"
          on={mode === 'always' ? false : (s.pause_on_battery ?? true)}
          busy={change.busy}
          disabledReason={mode === 'always' ? 'Always ignores the battery.' : why}
          onChange={(on) => void change.run({ pause_on_battery: on })}
        />
        {mode === 'always' ? null : (
          <Text>
            {s.pause_on_battery === false
              ? 'Off: the machine keeps taking jobs on battery.'
              : 'On: Automatic takes no new job while the machine runs on battery.'}
          </Text>
        )}
        <ChoiceField
          label="CPU for runners"
          help={
            capUnsupported
              ? 'Not supported on macOS yet. The runners use all the CPU.'
              : 'The most CPU the runner processes may use. The load budget stays as it is.'
          }
          value={String(cap)}
          choices={CAPS.map((c) => ({
            value: String(c),
            label: `${c}%`,
            ...(capUnsupported
              ? { disabledReason: 'Not supported on macOS yet.' }
              : !session.can('admin')
                ? { disabledReason: why }
                : {}),
          }))}
          onChange={(v) =>
            void change.run({ cpu_cap_pct: Number(v) as Schemas['CpuCapPct'] })
          }
        />
        {applying ? (
          <Message tone="note" dismissible={false}>
            Setting the CPU cap.
          </Message>
        ) : null}
        {policy?.cpu_cap_state === 'failed' ? (
          <Message tone="problem">
            The CPU cap was not set. {policy.cpu_cap_detail ?? ''}
          </Message>
        ) : null}
        {change.error ? (
          <Message tone="problem">Could not save. {change.error}</Message>
        ) : null}
        {heldByUser || mode === 'paused' ? (
          <Cluster>
            <Button
              onPress={() => void resume.run()}
              busy={resume.busy}
              busyLabel="Resuming"
              disabled={!session.can('admin')}
              reason={why}
            >
              Resume
            </Button>
          </Cluster>
        ) : null}
        {resume.error ? (
          <Message tone="problem">Could not resume. {resume.error}</Message>
        ) : null}
      </Stack>
    </Section>
  );
}
