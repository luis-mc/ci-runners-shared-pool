// The "Enable for public repositories" setting as the screens use it (github-integration R5.54 to R5.58,
// web-console R5.68 to R5.70): reading it, the words about it, and the switch with its question card.
import { QuestionCard } from '@crsp/ui';
import { useState, type ReactNode } from 'react';
import { unwrap, useClient, type Schemas } from '../../api/api';
import { useCommand, useLoad } from '../../api/hooks';

export type PublicRepositories = Schemas['PublicRepositories'];
type Repository = Schemas['Repository'];

/** Where the setting lives. */
export const PUBLIC_SETTING_PATH = '/settings/repositories';

export const PUBLIC_OFF_REASON =
  'Public repositories are off, so the pool does not serve this one.';

/** The active repository types, in the words the Repositories page shows. */
export function activeTypes(enabled: boolean): string {
  return enabled ? 'Private & Public' : 'Private (default)';
}

export function visibilityWord(r: Pick<Repository, 'visibility'>): string {
  return r.visibility === 'public'
    ? 'Public'
    : r.visibility === 'internal'
      ? 'Internal'
      : 'Private';
}

/**
 * False only when the controller read the user's rights and they are not admin.
 * An unread flag (no GitHub sign-in in memory after a restart) is not "no":
 * the change checks it again and fails with the real reason (OPS15, R5.19).
 */
export function mayTry(
  r: Pick<Repository, 'user_is_github_admin' | 'user_admin_known'>,
): boolean {
  return r.user_is_github_admin || !r.user_admin_known;
}

/** True when the repository is public and the setting is known to be off. */
export function publicWhileOff(
  r: Pick<Repository, 'visibility'>,
  enabled: boolean | undefined,
): boolean {
  return r.visibility === 'public' && enabled === false;
}

/** The setting, read once. `enabled` is undefined until it is known (nothing is greyed meanwhile). */
export function usePublicRepositories(): {
  setting: PublicRepositories | undefined;
  enabled: boolean | undefined;
  reload: () => void;
} {
  const client = useClient();
  const { result, reload } = useLoad(
    () => unwrap(client.GET('/api/v1/settings/public-repositories')),
    [],
  );
  const setting =
    result.state === 'ready'
      ? result.data
      : result.state === 'failed'
        ? result.data
        : undefined;
  return { setting, enabled: setting?.enabled, reload };
}

const plural = (n: number, one: string, many: string) => `${n} ${n === 1 ? one : many}`;

/** What the question card says before the setting is switched. */
export function questionItems(next: boolean, publicOnPool: number): string[] {
  if (next)
    return [
      'Anyone can open a pull request on a public repository.',
      'A self-hosted runner runs that code on your machines.',
      'Jobs from forks stay refused.',
      'Secrets, caches and files on the machine could be reached.',
      'Keeping your repositories private is safer.',
    ];
  return publicOnPool === 0
    ? ['No public repository uses the pool now. Nothing is stopped.']
    : [
        `${plural(publicOnPool, 'public repository uses', 'public repositories use')} the pool now. ${
          publicOnPool === 1 ? 'Its' : 'Their'
        } runners are drained and removed, and ${publicOnPool === 1 ? 'its' : 'their'} pool is switched off. The settings are kept.`,
      ];
}

/**
 * The switch with its question card: asking first (UX-9, UX-62), then one PUT. The page shows `dialog` and calls
 * `ask(next)`; `stopped` is how many pools the last change switched off.
 */
export function usePublicSwitch(
  setting: PublicRepositories | undefined,
  onDone: () => void,
): {
  ask: (next: boolean) => void;
  dialog: ReactNode;
  busy: boolean;
  error: string | undefined;
  stopped: number | undefined;
} {
  const client = useClient();
  const [target, setTarget] = useState<boolean | undefined>(undefined);
  const [stopped, setStopped] = useState<number | undefined>(undefined);
  const save = useCommand(async (enabled: boolean) => {
    const r = await unwrap(
      client.PUT('/api/v1/settings/public-repositories', { body: { enabled } }),
    );
    setStopped(r.stopped);
    setTarget(undefined);
    onDone();
  });
  const dialog =
    target === undefined ? null : (
      <QuestionCard
        title="Are you sure?"
        intro={
          target
            ? 'Turning this on lets the pool serve public repositories.'
            : 'Turning this off stops the pool for public repositories.'
        }
        items={[
          ...questionItems(target, setting?.public_on_pool ?? 0),
          ...(save.error ? [`Problem: ${save.error}`] : []),
        ]}
        action={target ? 'Turn on' : 'Turn off'}
        busyLabel={target ? 'Turning on' : 'Turning off'}
        busy={save.busy}
        onCancel={() => {
          save.clear();
          setTarget(undefined);
        }}
        onConfirm={() => void save.run(target)}
      />
    );
  return {
    ask: (next) => {
      save.clear();
      setStopped(undefined);
      setTarget(next);
    },
    dialog,
    busy: save.busy,
    error: target === undefined ? save.error : undefined,
    stopped,
  };
}
