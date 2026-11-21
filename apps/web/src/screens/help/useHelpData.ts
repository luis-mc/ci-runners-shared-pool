// The real numbers the Help page shows (LOOK48): the machines, the queue and the runners, read from the API. Help
// never fails because of them: a part with no data shows its short meaning and its picture draws the example.
import type { Schemas } from '../../api/api';
import { unwrap, useClient } from '../../api/api';
import { useLoad } from '../../api/hooks';
import { useMachines } from '../../api/machines';
import { useVersion } from '../../live/LiveProvider';

export interface HelpData {
  machines: readonly Schemas['Machine'][];
  /** The machine the pictures draw: the first one that is online, else the first. */
  machine: Schemas['Machine'] | undefined;
  /** One live example for each part of the picture; a part with no data has none. */
  examples: Readonly<Record<string, string>>;
  /** The repositories the pool serves, as far as the queue and the running jobs name them. */
  repositories: readonly string[];
}

export function useHelpData(): HelpData {
  const client = useClient();
  const machinesList = useMachines().result;
  const jv = useVersion('jobs');
  const rv = useVersion('runners');
  const overview = useLoad(() => unwrap(client.GET('/api/v1/overview')), [jv]).result;
  const runners = useLoad(() => unwrap(client.GET('/api/v1/runners')), [rv]).result;

  const machines = machinesList.state === 'ready' ? machinesList.data.items : [];
  const machine = machines.find((m) => m.online) ?? machines[0];
  const o = overview.state === 'ready' ? overview.data : undefined;
  const job = o?.running[0] ?? o?.queued?.[0];
  const runnerList = runners.state === 'ready' ? runners.data.items : [];
  const runner = runnerList.find((r) => r.current_job) ?? runnerList[0];

  const examples: Record<string, string> = {};
  if (job) {
    examples.repository = job.repository;
    examples.run = `#${job.run_id}${job.trigger ? `, ${job.trigger}` : ''}`;
    examples.job = `${job.job_name} + labels`;
  }
  if (o)
    examples.queue = o.queued_count === 0 ? 'none waiting' : `${o.queued_count} waiting`;
  if (machines.length > 0) {
    const total = machines.reduce((n, m) => n + m.settings.job_slots, 0);
    const used = machines.reduce((n, m) => n + m.slots_used, 0);
    examples.slot = `${used} of ${total} busy`;
  }
  if (runner) examples.runner = runner.name;
  if (machine) examples.machine = machine.name;

  const repositories = [
    ...new Set([...(o?.running ?? []), ...(o?.queued ?? [])].map((j) => j.repository)),
  ];
  return { machines, machine, examples, repositories };
}
