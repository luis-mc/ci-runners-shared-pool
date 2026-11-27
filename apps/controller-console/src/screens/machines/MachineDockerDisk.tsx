// The Docker disk compact of one machine (caches R5.51 to R5.54, machines R5.66): the policy and its numbers.
// Every control is saved at once (UX-22). Shared parts only; the words are in content.md ("Docker disk words").
import {
  Button,
  ChoiceField,
  Cluster,
  Facts,
  Message,
  NumberField,
  Section,
  Stack,
  Status,
  Text,
  type StatusKind,
  formatDateTime,
  formatSize,
} from '@crsp/ui';
import { unwrap, useClient, type Schemas } from '../../api/api';
import { useCommand } from '../../api/hooks';
import { useSession } from '../../auth/session';
import { CompactNow } from './CompactNow';

type Machine = Schemas['Machine'];
type Settings = Schemas['MachineSettingsUpdate'];

const MODES = [
  { value: 'auto', label: 'Automatic' },
  { value: 'on', label: 'On' },
  { value: 'off', label: 'Off' },
] as const;

const MODE_HELP: Record<string, string> = {
  auto: 'On for a Windows machine with a Docker Desktop disk file. Off for the others.',
  on: 'Makes the disk smaller when the numbers below say so.',
  off: 'Never makes the disk smaller by itself.',
};

/** Why the last look did not make the disk smaller, in words (content.md, Docker disk words). */
export function compactWhy(reason: string | undefined): string {
  switch (reason) {
    case 'run':
      return 'Making the disk smaller';
    case 'off':
      return 'Off';
    case 'offline':
      return 'Waiting: the machine is offline';
    case 'not_active':
      return 'Waiting: the machine is paused or not taking jobs';
    case 'no_report':
      return 'Waiting for the agent to look at the disk';
    case 'no_admin':
      return 'Cannot: the agent has no administrator rights';
    case 'below_threshold':
      return 'Waiting: less than the limit can be reclaimed';
    case 'busy':
      return 'Waiting: jobs run or wait here';
    case 'not_idle_long_enough':
      return 'Waiting: not idle long enough';
    case 'done_today':
      return 'Done today';
    case 'already_running':
      return 'Making the disk smaller';
    case 'change_open':
      return 'Waiting: another change is running';
    default:
      return 'Not looked at yet';
  }
}

function statusKind(reason: string | undefined, enabled: boolean): StatusKind {
  if (!enabled) return 'off';
  if (reason === 'no_admin') return 'failed';
  if (reason === 'run' || reason === 'already_running') return 'ok';
  return 'waiting';
}

export function MachineDockerDiskSection({
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
  const st = m.docker_compact_status;
  const mode = s.docker_compact ?? 'auto';
  const change = useCommand(async (body: Settings) => {
    await unwrap(
      client.PATCH('/api/v1/machines/{machineId}', {
        params: { path: { machineId: m.id } },
        body,
      }),
    );
    onChanged();
  });
  // A machine with no Docker Desktop disk file and no choice made has nothing to show.
  if (!st?.disk_present && mode === 'auto') return null;
  const locked = !session.can('admin') ? { disabledReason: why } : {};
  return (
    <Section title="Docker disk">
      <Stack>
        <Facts
          items={[
            ...(st
              ? [
                  {
                    name: 'Now',
                    value: (
                      <Status
                        kind={statusKind(st.reason, st.enabled)}
                        word={compactWhy(st.enabled ? st.reason : 'off')}
                      />
                    ),
                  },
                ]
              : []),
            ...(st?.disk_present && st.file_bytes !== undefined
              ? [{ name: 'Disk file', value: formatSize(st.file_bytes) }]
              : []),
            ...(st?.disk_present && st.used_bytes !== undefined
              ? [{ name: 'Docker uses', value: formatSize(st.used_bytes) }]
              : []),
            ...(st?.disk_present && st.reclaimable_bytes !== undefined
              ? [{ name: 'Can be reclaimed', value: formatSize(st.reclaimable_bytes) }]
              : []),
            ...(st?.last_at
              ? [
                  {
                    name: 'Last time',
                    value: `${formatDateTime(st.last_at)}${st.last_result ? `, ${st.last_result}` : ''}`,
                  },
                ]
              : []),
          ]}
        />
        {st?.enabled && st.disk_present && !st.can_compact ? (
          <Message tone="problem">
            The agent on this machine does not run as administrator, so it cannot make
            Docker's disk smaller. Install the agent again from an administrator window,
            or turn this off.
          </Message>
        ) : null}
        <ChoiceField
          label="Make Docker's disk smaller"
          help={MODE_HELP[mode]}
          value={mode}
          choices={MODES.map((c) => ({ ...c, ...locked }))}
          onChange={(v) =>
            void change.run({ docker_compact: v as Schemas['DockerCompactMode'] })
          }
        />
        <NumberField
          label="Start when this much can be reclaimed"
          value={s.docker_compact_threshold_gib ?? 30}
          unit=" GiB"
          min={1}
          max={100000}
          onChange={(n) => void change.run({ docker_compact_threshold_gib: n })}
        />
        <NumberField
          label="Wait until the machine has been idle for"
          help="No job runs or waits for its runners."
          value={s.docker_compact_idle_minutes ?? 30}
          unit=" min"
          min={1}
          max={1440}
          onChange={(n) => void change.run({ docker_compact_idle_minutes: n })}
        />
        <Text>
          It starts at any hour, once the machine has been idle for that long and that
          much can be reclaimed, and at most once a day. New jobs wait in the queue while
          the disk is made smaller. No job is stopped.
        </Text>
        {change.error ? (
          <Message tone="problem">Could not save. {change.error}</Message>
        ) : null}
        <CompactNow m={m} onStarted={onChanged} />
        {st?.enabled && st.disk_present && !st.can_compact ? (
          <Cluster>
            <Button
              onPress={() => void change.run({ docker_compact: 'off' })}
              busy={change.busy}
              busyLabel="Saving"
              disabled={!session.can('admin')}
              reason={why}
            >
              Turn off
            </Button>
          </Cluster>
        ) : null}
      </Stack>
    </Section>
  );
}
