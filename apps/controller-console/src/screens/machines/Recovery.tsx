// R5.33: the Recovery section of a machine whose agent needs recovery (operations R5.44). One action, admin only.
import { Button, Facts, Message, Section, Stack, formatPast } from '@crsp/ui';
import { useState } from 'react';
import { unwrap, useClient, type Schemas } from '../../api/api';
import { useCommand } from '../../api/hooks';
import { useSession } from '../../auth/session';

type Machine = Schemas['Machine'];

const WHY: Record<Schemas['MachineRecovery']['reason'], string> = {
  old_protocol: 'The agent on this machine is too old to work with the controller.',
  stuck: 'The agent on this machine was asked to update an hour ago and did not.',
};

export function RecoverySection({ m, onChanged }: { m: Machine; onChanged: () => void }) {
  const client = useClient();
  const session = useSession();
  const recovery = m.recovery;
  const [started, setStarted] = useState(false);
  const recover = useCommand(async () => {
    await unwrap(
      client.POST('/api/v1/machines/{machineId}/recovery', {
        params: { path: { machineId: m.id } },
      }),
    );
    setStarted(true);
    onChanged();
  });
  if (!recovery) return null;
  const ready = started || recovery.armed;
  const admin = session.can('admin');
  return (
    <Section title="Recovery">
      <Stack>
        <Message tone="problem">{WHY[recovery.reason]}</Message>
        <Facts
          items={[
            { name: 'Agent has', value: recovery.has },
            { name: 'Agent needs', value: recovery.needs },
            { name: 'Since', value: formatPast(recovery.since) },
          ]}
        />
        {ready ? (
          <Message tone="done">
            Recovery is ready. The agent gets the update on its next call.
          </Message>
        ) : null}
        {recover.error ? (
          <Message tone="problem">Could not start recovery. {recover.error}</Message>
        ) : null}
        <Button
          onPress={() => void recover.run()}
          busy={recover.busy}
          busyLabel="Recovering"
          disabled={!admin || ready}
          reason={!admin ? session.whyNot('admin') : undefined}
        >
          Recover
        </Button>
      </Stack>
    </Section>
  );
}
