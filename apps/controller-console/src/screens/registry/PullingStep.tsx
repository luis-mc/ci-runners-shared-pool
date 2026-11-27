// The pulling step of Add runners (REG5 R5.39, RES20 4.6): the job image comes from the registry, so the work line
// says what is pulled, how far, and when it is not coming. It reads `listRegistryPulls`; an answer that fails shows
// nothing, because the wizard does not depend on it.
import {
  LinkButton,
  Message,
  Progress,
  Text,
  formatDuration,
  formatSize,
} from '@crsp/ui';
import { useEffect, useState } from 'react';
import { unwrap, useClient, type Schemas } from '../../api/api';
import { TIMING } from '../../timing';
import { GB, shortKey } from './words';

type Pull = Schemas['RegistryPull'];

/** The pull of a machine now, asked again while `active`. */
export function usePull(machineId: string, active: boolean): Pull | undefined {
  const client = useClient();
  const [pull, setPull] = useState<Pull | undefined>(undefined);
  useEffect(() => {
    if (!machineId) return;
    let live = true;
    const ask = () =>
      void unwrap(
        client.GET('/api/v1/registry/pulls', {
          params: { query: { machine_id: machineId } },
        }),
      )
        .then((r) => live && setPull(r.items[0]))
        .catch(() => undefined);
    ask();
    if (!active) return () => void (live = false);
    const t = setInterval(ask, TIMING.planPollMs * 2);
    return () => {
      live = false;
      clearInterval(t);
    };
  }, [client, machineId, active]);
  return pull;
}

/** The Work line: pulling, waiting for the controller, or failed with "Build it here instead". */
export function PullingStep({
  machineId,
  machineName,
  active,
}: {
  machineId: string;
  machineName: string;
  active: boolean;
}) {
  const pull = usePull(machineId, active);
  if (!pull || pull.state === 'done') return null;
  if (pull.state === 'waiting_controller')
    return (
      <Message tone="note">
        {`Waiting for the controller to send the job image to ${machineName}. The pull goes on when it answers.`}
      </Message>
    );
  if (pull.state === 'failed')
    return (
      <Message
        tone="problem"
        action={<LinkButton to="/setup/images">Build it here instead</LinkButton>}
      >
        {`The job image could not be pulled, so no runner was added. Nothing was changed on ${machineName}. ${pull.error ?? ''}`.trim()}
      </Message>
    );
  const left =
    pull.seconds_left !== undefined
      ? `, about ${formatDuration(pull.seconds_left)} left`
      : '';
  const text = `Pulling the job image from the registry on ${machineName}, ${(pull.bytes_done / GB).toFixed(1)} of ${(pull.bytes_total / GB).toFixed(1)} GB${left}`;
  return (
    <Progress label={text} value={pull.bytes_done} max={Math.max(pull.bytes_total, 1)} />
  );
}

/** The Review line: the registry has a ready-made image for the machine's arch, so it is pulled, not built. */
export function ReadyMadeLine({ arch }: { arch: string }) {
  const client = useClient();
  const [line, setLine] = useState<string | undefined>(undefined);
  useEffect(() => {
    let live = true;
    const want =
      arch === 'x64' || arch === 'amd64'
        ? 'amd64'
        : arch === 'arm64'
          ? 'arm64'
          : undefined;
    if (!want) return;
    void unwrap(
      client.GET('/api/v1/registry/images', {
        params: { query: { arch: want, state: 'current' } },
      }),
    )
      .then((r) => {
        const first = r.items[0];
        if (live && first)
          setLine(
            `The registry has key ${shortKey(first.key)}, ${formatSize(first.size_bytes)}. A machine without a job image pulls it instead of building.`,
          );
      })
      .catch(() => undefined);
    return () => {
      live = false;
    };
  }, [client, arch]);
  return line ? <Text tone="muted">{line}</Text> : null;
}
