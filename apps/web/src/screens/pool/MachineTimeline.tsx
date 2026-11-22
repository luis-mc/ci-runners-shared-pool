// "Jobs over time" of the machine page (LOOK58): the shared slot timeline filtered to this machine.
import { SlotTimeline } from './SlotTimeline';

export function MachineTimeline({ machineId }: { machineId: string }) {
  return <SlotTimeline machineId={machineId} label="Jobs of this machine" />;
}
