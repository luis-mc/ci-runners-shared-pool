// The GitHub grant in the console (github-integration R5.86, R5.87; web-console R5.93): every place that needs a
// missing App permission shows this one part. It reads both sides of GitHub through the API, shows one step list,
// and tells the page when everything is granted. It passes data only: the steps, the lock and the primary action
// come from the shared part of @crsp/ui.
import { GithubGrant, Stack, type GrantState } from '@crsp/ui';
import { errorText, unwrap, useClient, type Schemas } from '../api/api';
import { useLoad } from '../api/hooks';
import { useSession } from '../auth/session';

type Grant = Schemas['GithubGrant'];

export function toState(g: Grant): GrantState {
  return {
    ready: g.ready,
    next: g.next,
    missing: g.missing,
    steps: g.steps.map((s) => ({
      id: s.id,
      done: s.done,
      permissions: s.permissions,
      url: s.url,
    })),
  };
}

export function GrantGuide({
  permissions,
  repositoryId,
  installationId,
  initial,
  onReady,
}: {
  /** GitHub's permission names the task needs. */
  permissions: readonly string[];
  repositoryId?: number | undefined;
  installationId?: number | undefined;
  /** The grant the server already sent with its answer, shown until the first read. */
  initial?: Grant | undefined;
  /** Called once when everything is granted: run the original action, or read the page again. */
  onReady?: (() => void) | undefined;
}) {
  const client = useClient();
  const query = permissions.join(',');
  const { result, reload } = useLoad(
    () =>
      unwrap(
        client.GET('/api/v1/github/grant', {
          params: {
            query: {
              permissions: query,
              ...(repositoryId ? { repository_id: repositoryId } : {}),
              ...(installationId ? { installation_id: installationId } : {}),
            },
          },
        }),
      ),
    [query, repositoryId, installationId],
  );
  const data =
    result.state === 'ready'
      ? result.data
      : result.state === 'failed'
        ? result.data
        : undefined;
  const grant = data ?? initial;
  return (
    <GithubGrant
      state={grant ? toState(grant) : undefined}
      onCheck={reload}
      {...(onReady ? { onReady } : {})}
      checking={result.state === 'loading' && grant !== undefined}
      error={result.state === 'failed' ? errorText(result.error) : undefined}
      needed={permissions}
    />
  );
}

/**
 * The guide for the installations that lack a permission the App asks for. It shows nothing for an installation
 * that has everything, and nothing for a person who is not an admin (the grant is an admin's to give).
 */
export function MissingPermissionsGuide({
  onReady,
}: {
  onReady?: (() => void) | undefined;
}) {
  const client = useClient();
  const session = useSession();
  const admin = session.can('admin');
  const { result, reload } = useLoad(
    () =>
      admin
        ? unwrap(client.GET('/api/v1/github/installations'))
        : Promise.resolve(undefined),
    [admin],
  );
  const data = result.state === 'ready' ? result.data : undefined;
  const lacking = data?.items.find(
    (i) => (i.missing_permissions?.length ?? 0) > 0 && !i.suspended,
  );
  if (!admin || !lacking) return null;
  return (
    <Stack>
      <GrantGuide
        permissions={lacking.missing_permissions ?? []}
        installationId={lacking.id}
        onReady={() => {
          reload();
          onReady?.();
        }}
      />
    </Stack>
  );
}
