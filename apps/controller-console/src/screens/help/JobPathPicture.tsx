// The picture of how a job gets run (LOOK44): the Help page draws it with a jump to the cards, the first-run Overview reads it.
import { JobPath } from '@crsp/ui';
import { PATH_GROUPS, PATH_PARTS } from './helpContent';

export function JobPathPicture({
  lit,
  examples,
  onSelect,
}: {
  lit?: readonly string[] | undefined;
  /** A live example for each part, by part id: shown under the name instead of the short meaning (LOOK48). */
  examples?: Readonly<Record<string, string>> | undefined;
  onSelect?: ((id: string) => void) | undefined;
}) {
  const parts = examples
    ? PATH_PARTS.map((p) => ({ ...p, example: examples[p.id] }))
    : PATH_PARTS;
  return (
    <JobPath
      label="How a job gets run"
      groups={PATH_GROUPS}
      parts={parts}
      lit={lit}
      onSelect={onSelect}
    />
  );
}
