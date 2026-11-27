// The doors into "Get this machine ready" (DEP13): Add runners, Add machine and Setup show this note where the
// machine cannot run what was chosen. The choices stay in the address that leads back (DEP13).
import { LinkButton, Message } from '@crsp/ui';
import type { Schemas } from '../../api/api';
import type { RunnerKind, RunnerOS } from './plan';

type Machine = Schemas['Machine'];

/** The address of the wizard for a machine and a runner choice; `back` is where Continue leads (an address in the console). */
export function readyAddress(
  machineId: string,
  os: RunnerOS,
  kind: RunnerKind,
  back?: string,
): string {
  const q = new URLSearchParams({ os, kind });
  if (back) q.set('return', back);
  return `/setup/machines/${machineId}/ready?${q.toString()}`;
}

/** A Linux runner runs in a container: the machine needs a container engine that answers. */
export function needsEngine(m: Pick<Machine, 'backends'>, os: RunnerOS): boolean {
  return os === 'linux' && !m.backends.some((b) => b.name === 'docker' && b.usable);
}

export function ReadyDoor({
  machine,
  os,
  kind,
  back,
  words,
}: {
  machine: Pick<Machine, 'id' | 'name'>;
  os: RunnerOS;
  kind: RunnerKind;
  back?: string | undefined;
  words?: string | undefined;
}) {
  return (
    <Message
      tone="needs-you"
      action={
        <LinkButton to={readyAddress(machine.id, os, kind, back)}>
          Get this machine ready
        </LinkButton>
      }
    >
      {words ?? `${machine.name} cannot run Linux containers yet.`}
    </Message>
  );
}
