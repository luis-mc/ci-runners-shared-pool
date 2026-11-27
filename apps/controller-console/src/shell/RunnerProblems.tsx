// The Problem banner of a runner service that did not start (runners R5.89, web-console R5.98). The agent has
// already given the service account the right to log on as a service again and tried twice; what is left is
// told here, with the machine, the runner and the reason Windows gave. Start asks the agent to do it again now
// (the same call as Redetect). The banner is gone when the machine's next report says the service runs.
import { Button, Message } from '@crsp/ui';
import { unwrap, useClient, type Schemas } from '../api/api';
import { useCommand } from '../api/hooks';
import { useSession } from '../auth/session';

type Machine = Schemas['Machine'];
type Problem = NonNullable<Machine['runner_problems']>[number];

/** Every runner problem of the online machines, machine first, in list order. */
export function runnerProblems(
  machines: readonly Machine[],
): { machine: Machine; problem: Problem }[] {
  return machines.flatMap((machine) =>
    machine.online
      ? (machine.runner_problems ?? []).map((problem) => ({ machine, problem }))
      : [],
  );
}

export function RunnerProblemBanner({
  machine,
  problem,
  onStarted,
}: {
  machine: Machine;
  problem: Problem;
  onStarted: () => void;
}) {
  const client = useClient();
  const session = useSession();
  const admin = session.can('admin');
  const start = useCommand(async () => {
    await unwrap(
      client.POST('/api/v1/machines/{machineId}/redetect', {
        params: { path: { machineId: machine.id } },
      }),
    );
    onStarted();
  });
  return (
    <Message
      tone="problem"
      action={
        <Button
          onPress={() => void start.run()}
          busy={start.busy}
          busyLabel="Starting"
          disabled={!admin}
          reason={!admin ? session.whyNot('admin') : undefined}
        >
          Start
        </Button>
      }
    >
      {`${problem.runner} on ${machine.name} did not start. ${problem.reason}`}
      {start.error ? ` Could not ask the agent. ${start.error}` : ''}
    </Message>
  );
}
