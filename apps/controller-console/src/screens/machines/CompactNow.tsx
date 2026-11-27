// Compact now (caches R5.58): the owner starts the Docker disk compact that the policy would start later. It asks first and
// names what can be reclaimed (UX-9, UX-62); the button is busy while the compact runs (UX-13). Shared parts only; the
// words are in content.md ("Docker disk words").
import { Button, formatSize } from '@crsp/ui';
import { unwrap, useClient, type Schemas } from '../../api/api';
import { useQuestion } from '../../api/hooks';
import { useSession } from '../../auth/session';
import { Ask } from '../common';

type Machine = Schemas['Machine'];

/** The lines of the question card: the size that can be reclaimed, then what happens to jobs. */
export function compactItems(reclaimable: number | undefined): string[] {
  return [
    reclaimable === undefined
      ? 'Docker disk: the size that can be reclaimed is not known yet'
      : `Can be reclaimed: ${formatSize(reclaimable)}`,
    'New jobs wait in the queue; no job is stopped',
    'Docker restarts, and the machine takes jobs again when it is done',
  ];
}

export function CompactNow({ m, onStarted }: { m: Machine; onStarted: () => void }) {
  const client = useClient();
  const session = useSession();
  const st = m.docker_compact_status;
  const running = st?.running === true;
  const ask = useQuestion(async () => {
    await unwrap(
      client.POST('/api/v1/machines/{machineId}/docker-compact', {
        params: { path: { machineId: m.id }, query: { confirm: true } },
      }),
    );
  }, onStarted);
  if (!st?.disk_present) return null;
  const admin = session.can('admin');
  const reason = !admin
    ? session.whyNot('admin')
    : !st.can_compact
      ? 'The agent has no administrator rights.'
      : running
        ? 'The disk is being made smaller.'
        : undefined;
  return (
    <>
      <Button
        onPress={() => ask.ask(compactItems(st.reclaimable_bytes))}
        busy={running || ask.busy}
        busyLabel="Making the disk smaller"
        disabled={!admin || !st.can_compact}
        reason={reason}
      >
        Make the disk smaller now
      </Button>
      <Ask
        title={`Make Docker's disk smaller on ${m.name}?`}
        intro="The machine is drained first. The disk is made smaller when the running jobs have ended."
        action="Make smaller"
        busyLabel="Starting"
        q={ask.question}
        busy={ask.busy}
        error={ask.error}
        onCancel={ask.cancel}
        onConfirm={() => void ask.confirm()}
      />
    </>
  );
}
