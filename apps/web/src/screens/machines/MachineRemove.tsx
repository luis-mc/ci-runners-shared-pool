// Remove crsp from a machine: the pane and the RemoveDialog. Loaded on demand (not in the first JavaScript).
import { Button, Message, Pane, Text } from '@crsp/ui';
import { RemoveDialog, lw } from '@crsp/ui/lifecycle';
import { useState } from 'react';
import { useCommand } from '../../api/hooks';
import { useLifecycle } from '../../api/lifecycle';

export default function MachineRemove({
  machineId,
  disabled,
  onRemoved,
}: {
  machineId: string;
  disabled: boolean;
  onRemoved: () => void;
}) {
  const lifecycle = useLifecycle();
  const [open, setOpen] = useState(false);
  const remove = useCommand(async (deleteData: boolean) => {
    await lifecycle.remove(deleteData, machineId);
    setOpen(false);
    onRemoved();
  });
  return (
    <>
      <Pane
        title={lw('remove.title', { product: 'crsp' })}
        footer={
          <Button kind="danger" onPress={() => setOpen(true)} disabled={disabled}>
            {lw('remove.confirm', { product: 'crsp' })}
          </Button>
        }
      >
        <Text tone="muted">{lw('remove.choose')}</Text>
      </Pane>
      {open ? (
        <RemoveDialog
          product="crsp"
          busy={remove.busy}
          onCancel={() => setOpen(false)}
          onRemove={(c) => void remove.run(c === 'purge')}
        />
      ) : null}
      {remove.error ? <Message tone="problem">{remove.error}</Message> : null}
    </>
  );
}
