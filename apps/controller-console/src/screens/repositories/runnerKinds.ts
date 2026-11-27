// Which runners take a repository's jobs (github-integration R5.60, web-console R5.102): the words, the choices
// and the selection. The policy says the kind and whether the owner chose it; a public repository defaults to
// Ephemeral only, a private one to Any runner.
import type { Schemas } from '../../api/api';

type Policy = Schemas['RepositoryPolicy'];
export type KindSelection = 'default' | 'ephemeral_only' | 'any_runner';

export const KIND_WORDS = {
  ephemeral_only: 'Ephemeral only',
  any_runner: 'Any runner',
} as const;

export const NO_CAPACITY = 'No ephemeral runner capacity: jobs would wait.';
export const ANY_RUNNER_WARNING =
  'Jobs from a public repository can then run on a persistent runner, which keeps files between jobs. Anyone who can open a pull request may reach it.';

/** The kind as the list and the facts say it; "(default)" while the owner has not chosen. */
export function runnerKindText(
  p: Pick<Policy, 'runner_kinds' | 'runner_kinds_set'>,
): string {
  const kind = KIND_WORDS[p.runner_kinds ?? 'any_runner'];
  return p.runner_kinds_set ? kind : `${kind} (default)`;
}

/** What the choice shows: the owner's pick, or "default" while the visibility's default stands. */
export function kindSelection(
  p: Pick<Policy, 'runner_kinds' | 'runner_kinds_set'>,
): KindSelection {
  return p.runner_kinds_set ? (p.runner_kinds ?? 'default') : 'default';
}

export function kindChoices(r: {
  visibility: string;
}): { value: KindSelection; label: string }[] {
  const def =
    r.visibility === 'public' ? KIND_WORDS.ephemeral_only : KIND_WORDS.any_runner;
  return [
    { value: 'default', label: `Use the default (${def})` },
    { value: 'ephemeral_only', label: KIND_WORDS.ephemeral_only },
    { value: 'any_runner', label: KIND_WORDS.any_runner },
  ];
}
