// Pool per organization (github-integration R5.21): for each organization the App is installed on, one switch turns
// organization runners on, in a runner group named for the pool and visible to the repositories whose pool is on.
// The switch saves at once (UX-22). Only an owner of the organization on GitHub may turn it on; GitHub answers that
// and the page shows the answer. A personal account has no switch: its runners stay with the repository.
import {
  Message,
  Section,
  Stack,
  Status,
  Switch,
  Table,
  Text,
  type Column,
} from '@crsp/ui';
import { unwrap, useClient, type Schemas } from '../../api/api';
import { useCommand, useLoad } from '../../api/hooks';
import { useSession } from '../../auth/session';
import { Load } from '../common';

type OrgPool = Schemas['OrgPool'];

/** The runners of an installation, as a status (content.md, pool per organization words). */
export function orgPoolStatus(o: OrgPool): { kind: 'ok' | 'off'; word: string } {
  return o.enabled
    ? { kind: 'ok', word: 'organization runners' }
    : { kind: 'off', word: 'repository runners' };
}

function OrgSwitch({ o, onChanged }: { o: OrgPool; onChanged: () => void }) {
  const client = useClient();
  const session = useSession();
  const set = useCommand(async (enabled: boolean) => {
    await unwrap(
      client.PUT('/api/v1/github/installations/{installationId}/org-pool', {
        params: { path: { installationId: o.installation_id } },
        body: { enabled },
      }),
    );
    onChanged();
  });
  const why = !session.can('admin')
    ? session.whyNot('admin')
    : !o.eligible && !o.enabled
      ? (o.reason ?? 'The pool per organization is not available here.')
      : undefined;
  return (
    <Stack>
      <Switch
        label={`Organization runners for ${o.organization}`}
        on={o.enabled}
        busy={set.busy}
        disabledReason={why}
        onChange={(on) => void set.run(on)}
      />
      {set.error ? (
        <Message tone="problem">Could not change the pool. {set.error}</Message>
      ) : null}
    </Stack>
  );
}

export function OrgPools({ version }: { version: unknown }) {
  const client = useClient();
  const { result, reload } = useLoad(
    () => unwrap(client.GET('/api/v1/github/org-pools')),
    [version],
  );
  return (
    <Load result={result} what="organizations" reload={reload}>
      {(data) => {
        const orgs = data.items.filter((o) => o.is_organization);
        if (orgs.length === 0) return null;
        const columns: Column<OrgPool>[] = [
          {
            id: 'org',
            header: 'Organization',
            cell: (o) => o.organization,
            sort: (o) => o.organization,
          },
          {
            id: 'runners',
            header: 'Runners',
            cell: (o) => {
              const s = orgPoolStatus(o);
              return <Status kind={s.kind} word={s.word} />;
            },
          },
          {
            id: 'group',
            header: 'Runner group',
            cell: (o) => (o.enabled ? o.group_name : 'none'),
          },
          {
            id: 'repos',
            header: 'Repositories in the group',
            cell: (o) => (o.enabled ? o.repositories.join(', ') || 'none' : 'none'),
          },
          {
            id: 'switch',
            header: 'Use organization runners',
            cell: (o) => <OrgSwitch o={o} onChanged={reload} />,
          },
        ];
        return (
          <Section title="Pool per organization">
            <Stack>
              <Text>
                Organization runners work in one runner group that only the repositories
                with the pool on can use. Only an owner of the organization on GitHub can
                turn this on. Personal accounts keep repository runners.
              </Text>
              <Table
                caption="Pool per organization"
                columns={columns}
                rows={orgs}
                rowKey={(o) => String(o.installation_id)}
              />
            </Stack>
          </Section>
        );
      }}
    </Load>
  );
}
