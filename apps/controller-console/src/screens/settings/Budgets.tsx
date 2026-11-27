// Settings, Budgets (web-console 4.9, R5.27; machines R5.22; scheduling R5.8 to R5.10; observability R5.5): the
// load budget, slots and drain timeout that a new machine starts with, and the order of service of the queue.
// One form that ends with Cancel and Save (UX-24). A change here touches no machine that already joined, so no
// runner restarts and no running job is waited for (UX-62); the page says so in one line.
import {
  CheckRow,
  ErrorSummary,
  FieldGrid,
  Message,
  NumberField,
  OrderList,
  normaliseOrder,
  Section,
  Stack,
  Text,
} from '@crsp/ui';
import { useState } from 'react';
import { unwrap, useClient, type Schemas } from '../../api/api';
import { useCommand, useLoad } from '../../api/hooks';
import { useSession } from '../../auth/session';
import { Load } from '../common';
import { useTabPart } from './TabForm';

type Pool = Schemas['PoolSettings'];
type Queue = Schemas['QueueSettings'];
type Key = Queue['order'][number];

const KEYS: { value: Key; label: string }[] = [
  { value: 'priority', label: 'Repository priority' },
  { value: 'event_class', label: 'Event class' },
  { value: 'fair_share', label: 'Fair share' },
  { value: 'queued_at', label: 'Time queued' },
];
const KEY_ORDER = KEYS.map((k) => ({ id: k.value, label: k.label }));

/** Stored config with a repeated or missing key: keep the first of each, append the missing in the default order. */
export const normaliseQueue = (queue: Queue): Queue => ({
  ...queue,
  order: normaliseOrder(queue.order, KEY_ORDER) as Queue['order'],
});

/** The mistakes of a form, each in one sentence, before anything is sent. */
export function budgetProblems(pool: Pool, queue: Queue): string[] {
  const out: string[] = [];
  const within = (name: string, v: number, min: number, max: number, unit: string) => {
    if (!Number.isInteger(v) || v < min || v > max)
      out.push(`${name} must be from ${min}${unit} to ${max}${unit}.`);
  };
  within('Memory budget', pool.budget_memory_pct, 10, 95, '%');
  within('CPU budget', pool.budget_cpu_pct, 10, 95, '%');
  within('Job slots', pool.default_job_slots, 0, 64, '');
  within('Time to finish running jobs', pool.drain_timeout_minutes, 5, 1440, ' min');
  within('Wait before "no runner"', queue.no_runner_wait_minutes, 1, 60, ' min');
  if (new Set(queue.order).size !== KEYS.length)
    out.push('Each of the four order keys must be used once.');
  return out;
}

export function BudgetsSettings() {
  const client = useClient();
  const pool = useLoad(() => unwrap(client.GET('/api/v1/settings/pool')), []);
  const queue = useLoad(() => unwrap(client.GET('/api/v1/settings/queue')), []);
  const reload = () => {
    pool.reload();
    queue.reload();
  };
  return (
    <Load result={pool.result} what="the budgets" reload={reload}>
      {(p) => (
        <Load result={queue.result} what="the queue order" reload={reload}>
          {(q) => (
            <BudgetsForm
              key={JSON.stringify([p, q])}
              pool={p}
              queue={q}
              onSaved={reload}
            />
          )}
        </Load>
      )}
    </Load>
  );
}

function BudgetsForm({
  pool,
  queue,
  onSaved,
}: {
  pool: Pool;
  queue: Queue;
  onSaved: () => void;
}) {
  const client = useClient();
  const session = useSession();
  const [p, setP] = useState<Pool>(pool);
  const [q, setQ] = useState<Queue>(normaliseQueue(queue));
  const [errors, setErrors] = useState<string[]>([]);
  const [saved, setSaved] = useState(false);
  const savedQueue = normaliseQueue(queue);
  const poolChanged = (k: keyof Pool) => JSON.stringify(pool[k]) !== JSON.stringify(p[k]);
  const queueChanged = (k: keyof Queue) =>
    JSON.stringify(savedQueue[k]) !== JSON.stringify(q[k]);
  const changeCount =
    (Object.keys(pool) as (keyof Pool)[]).filter(poolChanged).length +
    (Object.keys(queue) as (keyof Queue)[]).filter(queueChanged).length;
  const save = useCommand(async () => {
    const problems = budgetProblems(p, q);
    setErrors(problems);
    setSaved(false);
    if (problems.length) return false;
    await unwrap(client.PUT('/api/v1/settings/pool', { body: p }));
    await unwrap(client.PUT('/api/v1/settings/queue', { body: q }));
    setSaved(true);
    onSaved();
    return true;
  });
  useTabPart('budgets', {
    changes: changeCount,
    save: async () => (await save.run()) === true,
    reset: () => {
      setP(pool);
      setQ(normaliseQueue(queue));
      setErrors([]);
      setSaved(false);
      save.clear();
    },
    blocked: session.can('admin') ? undefined : session.whyNot('admin'),
  });
  return (
    <div>
      <Stack>
        <Section title="New machines">
          <Stack>
            <Text>
              These are the settings a machine gets when it joins. A machine that already
              joined keeps its own, so no runner restarts and no running job is waited
              for.
            </Text>
            <FieldGrid>
              <NumberField
                label="Memory budget"
                value={p.budget_memory_pct}
                changed={poolChanged('budget_memory_pct')}
                unit="%"
                min={10}
                max={95}
                onChange={(v) => setP({ ...p, budget_memory_pct: v })}
              />
              <NumberField
                label="CPU budget"
                value={p.budget_cpu_pct}
                changed={poolChanged('budget_cpu_pct')}
                unit="%"
                min={10}
                max={95}
                onChange={(v) => setP({ ...p, budget_cpu_pct: v })}
              />
              <NumberField
                label="Job slots"
                value={p.default_job_slots}
                changed={poolChanged('default_job_slots')}
                min={0}
                max={64}
                onChange={(v) => setP({ ...p, default_job_slots: v })}
              />
              <NumberField
                label="Time to finish running jobs"
                help="How long running jobs may take to finish when a machine stops new jobs."
                value={p.drain_timeout_minutes}
                changed={poolChanged('drain_timeout_minutes')}
                unit=" min"
                min={5}
                max={1440}
                onChange={(v) => setP({ ...p, drain_timeout_minutes: v })}
              />
            </FieldGrid>
            <CheckRow
              label="Size the slots from the machine"
              help="Uses its cores and memory within the budget. Job slots is used when the machine does not say how big it is."
              checked={p.size_slots_from_machine}
              changed={poolChanged('size_slots_from_machine')}
              onChange={(v) => setP({ ...p, size_slots_from_machine: v })}
            />
          </Stack>
        </Section>
        <Section title="Queue order">
          <Stack>
            <Text>The first key decides first. A later key breaks the ties.</Text>
            <OrderList
              label="Queue order"
              items={KEY_ORDER}
              value={q.order}
              onChange={(order) => setQ({ ...q, order: order as Queue['order'] })}
            />
            <CheckRow
              label="Fair share"
              help="Repositories take turns inside one priority."
              checked={q.fair_share}
              changed={queueChanged('fair_share')}
              onChange={(v) => setQ({ ...q, fair_share: v })}
            />
            <NumberField
              label='Wait before "no runner"'
              help="Minutes a job waits before the console says no online runner has its labels."
              value={q.no_runner_wait_minutes}
              changed={queueChanged('no_runner_wait_minutes')}
              unit=" min"
              min={1}
              max={60}
              onChange={(v) => setQ({ ...q, no_runner_wait_minutes: v })}
            />
          </Stack>
        </Section>
        <ErrorSummary errors={errors} />
        {saved ? <Message tone="done">Settings saved.</Message> : null}
        {save.error ? (
          <Message tone="problem">Could not save. {save.error}</Message>
        ) : null}
      </Stack>
    </div>
  );
}
