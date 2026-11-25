// Take a machine out of the pool (machines R5.40 to R5.42): the question names every item that goes, then the
// machine drains and its key is revoked. An offline machine loses its key at once and its runners are deregistered
// from GitHub; its files stay until crsp is removed there. Words: content.md, Remove from the pool words.
import { Button, Message, Pane, Text } from '@crsp/ui';
import { unwrap, useClient, ApiError } from '../../api/api';
import { useCommand, useQuestion } from '../../api/hooks';
import { Ask } from '../common';

export default function MachineLeavePool({
  machineId,
  name,
  online,
  disabled,
  onRemoved,
}: {
  machineId: string;
  name: string;
  online: boolean;
  disabled: boolean;
  onRemoved: () => void;
}) {
  const client = useClient();
  const remove = (confirm: boolean) =>
    unwrap(
      client.DELETE('/api/v1/machines/{machineId}', {
        params: { path: { machineId }, query: confirm ? { confirm: true } : {} },
      }),
    );
  const q = useQuestion(() => remove(true), onRemoved);
  // The first press asks the server what goes: without a yes it answers with the list and changes nothing.
  const look = useCommand(async () => {
    try {
      await remove(false);
    } catch (e) {
      if (e instanceof ApiError && e.code === 'needs_confirmation') {
        q.ask(e.details.map((d) => d.text ?? d.id ?? '').filter(Boolean));
        return;
      }
      throw e;
    }
  });
  return (
    <>
      <Pane
        title="Remove from the pool"
        footer={
          <Button
            kind="danger"
            onPress={() => void look.run()}
            busy={look.busy}
            busyLabel="Reading what goes"
            disabled={disabled}
            reason="Only an admin can remove a machine."
          >
            Remove from the pool
          </Button>
        }
      >
        <Text tone="muted">
          {online
            ? `${name} stops taking jobs, waits for the running jobs, then its runners, images, caches, settings and key go. A running job is never cut.`
            : `${name} is offline. Its key is revoked at once and its runners are deregistered from GitHub. Its own files stay until crsp is removed from it.`}
        </Text>
      </Pane>
      <Ask
        title={`Remove ${name} from the pool?`}
        action="Remove"
        busyLabel="Removing"
        q={q.question}
        busy={q.busy}
        error={q.error}
        onCancel={q.cancel}
        onConfirm={() => void q.confirm()}
      />
      {look.error ? <Message tone="problem">Could not read what goes. {look.error}</Message> : null}
    </>
  );
}
