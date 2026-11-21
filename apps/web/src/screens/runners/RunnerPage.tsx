// One runner (runners R5.89, R5.90): its kind, and for a persistent runner the setting "Keep downloaded
// dependencies" (saved at once), the size of its own dependency cache with "Clear now", and why it is not clean.
import {
  Breadcrumbs,
  Button,
  CheckRow,
  EditBar,
  Facts,
  Message,
  Page,
  PageHeader,
  Section,
  Stack,
  Text,
  TextLink,
  formatSize,
} from '@crsp/ui';
import { useEffect, useState } from 'react';
import { useParams } from 'react-router-dom';
import { unwrap, useClient, type Schemas } from '../../api/api';
import { useCommand, useLoad } from '../../api/hooks';
import { useSession } from '../../auth/session';
import { useVersion } from '../../live/LiveProvider';
import { Load } from '../common';
import { KIND_NAMES } from './kinds';

type Runner = Schemas['Runner'];

/** A persistent runner says whether it keeps downloaded dependencies; an ephemeral runner has nothing to keep. */
export function keepsText(
  r: Pick<Runner, 'kind' | 'keep_dependencies'>,
): string | undefined {
  if (r.kind !== 'persistent') return undefined;
  return `Keeps dependencies: ${r.keep_dependencies === false ? 'no' : 'yes'}`;
}

/** The runner's problem when its clean-up failed (R5.90). */
export function uncleanText(reason: string): string {
  const t = reason.trim().replace(/[.!?]$/, '');
  return `Not clean: ${t}. It takes no new job until it is clean.`;
}

export const TEMP_LINE = 'Temporary files of each job are always removed.';

export function RunnerPage() {
  const { id = '' } = useParams();
  const client = useClient();
  const v = useVersion('runners');
  const { result, reload } = useLoad(
    () =>
      unwrap(
        client.GET('/api/v1/runners/{runnerId}', { params: { path: { runnerId: id } } }),
      ),
    [id, v],
  );
  return (
    <Page>
      <Breadcrumbs
        trail={[
          { to: '/pool/runners', label: 'Runners' },
          { label: result.state === 'ready' ? result.data.name : 'Runner' },
        ]}
      />
      {result.state === 'ready' ||
      (result.state === 'failed' && result.data !== undefined) ? null : (
        <PageHeader title="Runner" />
      )}
      <Load result={result} what="the runner" reload={reload}>
        {(r) => <RunnerBody r={r} onChanged={reload} />}
      </Load>
    </Page>
  );
}

function RunnerBody({ r, onChanged }: { r: Runner; onChanged: () => void }) {
  const client = useClient();
  const session = useSession();
  const admin = session.can('admin');
  const why = session.whyNot('admin');
  const persistent = r.kind === 'persistent';
  const saved = r.keep_dependencies !== false;
  const [keeps, setKeeps] = useState(saved);
  useEffect(() => setKeeps(saved), [saved]);

  const save = useCommand(async () => {
    await unwrap(
      client.PATCH('/api/v1/runners/{runnerId}/settings', {
        params: { path: { runnerId: r.id } },
        body: { keep_dependencies: keeps },
      }),
    );
    onChanged();
  });
  const clear = useCommand(async () => {
    await unwrap(
      client.POST('/api/v1/runners/{runnerId}/clear-cache', {
        params: { path: { runnerId: r.id } },
      }),
    );
    onChanged();
  });

  return (
    <Stack>
      <PageHeader title={r.name} />
      {r.unclean_reason ? (
        <Message tone="problem">{uncleanText(r.unclean_reason)}</Message>
      ) : null}
      <Section title="Runner">
        <Facts
          items={[
            { name: 'Type', value: KIND_NAMES[r.kind] },
            { name: 'State', value: r.state },
            {
              name: 'Machine',
              value: (
                <TextLink to={`/pool/machines/${r.machine_id}`}>
                  {r.machine_name}
                </TextLink>
              ),
            },
            { name: 'System', value: `${r.os} ${r.arch}` },
            { name: 'Labels', value: r.labels.join(', ') },
          ]}
        />
      </Section>
      {persistent ? (
        <Section title="Dependencies">
          <Stack>
            <CheckRow
              label="Keep downloaded dependencies"
              checked={keeps}
              changed={keeps !== saved}
              onChange={setKeeps}
              {...(!admin ? { disabledReason: why } : {})}
            />
            <Text>
              {keeps
                ? `${TEMP_LINE} Downloaded dependencies stay for the next job.`
                : `${TEMP_LINE} Downloaded dependencies are removed after each job too.`}
            </Text>
            {save.error ? (
              <Message tone="problem">Could not save. {save.error}</Message>
            ) : null}
            <Facts
              items={[
                {
                  name: 'Dependency cache size',
                  value:
                    r.dependency_cache_bytes === undefined
                      ? 'Not measured yet'
                      : formatSize(r.dependency_cache_bytes),
                },
              ]}
            />
            <Button
              onPress={() => void clear.run()}
              busy={clear.busy}
              busyLabel="Clearing"
              disabled={!admin}
              reason={why}
            >
              Clear now
            </Button>
            {r.state === 'busy' ? (
              <Text tone="muted">A job is running. Clearing waits for it to finish.</Text>
            ) : null}
            {clear.error ? (
              <Message tone="problem">Could not clear. {clear.error}</Message>
            ) : null}
          </Stack>
        </Section>
      ) : null}
      {persistent ? (
        <EditBar
          changes={keeps !== saved ? 1 : 0}
          submit={false}
          onSave={() => void save.run()}
          onCancel={() => setKeeps(saved)}
          busy={save.busy}
          blocked={!admin}
          why={why}
        />
      ) : null}
    </Stack>
  );
}
