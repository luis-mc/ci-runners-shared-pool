// The words and the grouping of Pool > Queue Analysis (CRSP391): one word for why a job waits, the shared
// message for jobs that wait for the same reason, and how long a job has waited.
import { formatDuration } from '@crsp/ui';
import { isReserved, type Job } from '../jobs/words';

export type Reason = 'room' | 'reserved' | 'none' | 'drained';

export const QUEUE_WORDS = {
  all: 'All',
  room: 'Waiting for room',
  reserved: 'Reserved',
  none: 'No runner fits',
  drained: 'Machine not taking jobs',
} as const;

/** The one reason a queued job is shown under. "Waiting for room" only when a runner that fits exists. */
export function queueReason(j: Pick<Job, 'state' | 'wait_reason'>): Reason {
  if (isReserved(j)) return 'reserved';
  if (j.wait_reason === 'no_runner_with_labels') return 'none';
  if (j.wait_reason === 'machine_drained') return 'drained';
  return 'room';
}

/** The labels of a job that no runner carries, as the controller named them. */
export function missingOf(j: Pick<Job, 'wait_text' | 'labels'>): string[] {
  return (j.wait_text ?? '')
    .split(',')
    .map((x) => x.trim())
    .filter((x) => x !== '' && j.labels.includes(x));
}

export interface QueueGroup {
  key: string;
  words: string;
  link: string;
  to: string;
}

const plural = (n: number, one: string, many: string): string => (n === 1 ? one : many);

/**
 * One message for each set of jobs that wait for the same reason and labels, shown once above the table
 * instead of once in every row.
 */
export function queueGroups(queue: readonly Job[]): QueueGroup[] {
  const none = new Map<string, number>();
  let drained = 0;
  for (const j of queue) {
    const r = queueReason(j);
    if (r === 'none') {
      const key = missingOf(j).join(', ') || j.labels.join(', ');
      none.set(key, (none.get(key) ?? 0) + 1);
    } else if (r === 'drained') drained += 1;
  }
  const out: QueueGroup[] = [...none].map(([labels, n]) => ({
    key: `none:${labels}`,
    words: `${n} ${plural(n, 'job asks', 'jobs ask')} for ${plural(labels.split(',').length, 'the label', 'the labels')} ${labels}, which no runner has. Check the repository's runs-on variable.`,
    link: 'Open Setup > Pool setup',
    to: '/setup/pool',
  }));
  if (drained > 0)
    out.push({
      key: 'drained',
      words: `${drained} ${plural(drained, 'job waits', 'jobs wait')} on a machine that is not taking jobs. Let the machine take jobs again, or add a runner on another machine.`,
      link: 'Open Setup > Machines',
      to: '/setup/machines',
    });
  return out;
}

/** How long a queued job has waited: "4 h 10 min", "3 d 2 h"; "not known" when GitHub never said when it queued. */
export function waitedWords(
  j: Pick<Job, 'queued_at' | 'created_at'>,
  now: number,
): string {
  const at = j.queued_at ?? j.created_at;
  if (!at) return 'not known';
  const seconds = Math.max(0, (now - new Date(at).getTime()) / 1000);
  const hours = Math.floor(seconds / 3600);
  if (hours >= 48) return `${Math.floor(hours / 24)} d ${hours % 24} h`;
  return formatDuration(seconds < 60 ? seconds : Math.floor(seconds / 60) * 60);
}
