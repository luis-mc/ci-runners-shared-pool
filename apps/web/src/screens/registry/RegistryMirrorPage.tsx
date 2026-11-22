// The pull-through mirror of upstream base images (REG5 spec section 4, REG9): on or off, its upstreams and budget.
import {
  Breadcrumbs,
  CheckRow,
  EditBar,
  Facts,
  Message,
  NumberField,
  Page,
  PageHeader,
  Section,
  Stack,
  TextField,
  formatSize,
} from '@crsp/ui';
import { useState } from 'react';
import { unwrap, useClient, type Schemas } from '../../api/api';
import { useCommand, useLoad } from '../../api/hooks';
import { Load } from '../common';
import { GB } from './words';

export function RegistryMirrorPage() {
  const client = useClient();
  const { result, reload } = useLoad(
    () => unwrap(client.GET('/api/v1/registry/mirror')),
    [],
  );
  return (
    <Page>
      <Breadcrumbs
        trail={[
          { to: '/setup/images?tab=registry', label: '‹ Registry' },
          { label: 'Mirror' },
        ]}
      />
      <PageHeader
        title="Mirror of upstream images"
        lead="Machines pull base images such as docker.io ones through the controller, so each is downloaded once."
      />
      <Load result={result} what="the mirror" reload={reload}>
        {(m) => <MirrorBody mirror={m} reload={reload} />}
      </Load>
    </Page>
  );
}

function MirrorBody({
  mirror,
  reload,
}: {
  mirror: Schemas['RegistryMirrorSettings'];
  reload: () => void;
}) {
  const client = useClient();
  const base = {
    enabled: mirror.enabled,
    upstreams: mirror.upstreams.join(', '),
    budget: Math.round(mirror.budget_bytes / GB),
    recheck: mirror.tag_recheck_minutes,
  };
  const [draft, setDraft] = useState<Partial<typeof base>>({});
  const v = { ...base, ...draft };
  const changes = (Object.keys(base) as (keyof typeof base)[]).filter(
    (k) => v[k] !== base[k],
  ).length;
  const save = useCommand(async () => {
    await unwrap(
      client.PUT('/api/v1/registry/mirror', {
        body: {
          enabled: v.enabled,
          upstreams: v.upstreams
            .split(',')
            .map((x) => x.trim())
            .filter(Boolean),
          budget_bytes: v.budget * GB,
          tag_recheck_minutes: v.recheck,
        },
      }),
    );
    setDraft({});
    reload();
  });
  const set =
    <K extends keyof typeof base>(k: K) =>
    (x: (typeof base)[K]) =>
      setDraft((d) => ({ ...d, [k]: x }));
  return (
    <Stack>
      <Section title="Mirror">
        <Stack gap="tight">
          <CheckRow
            label="Mirror upstream images"
            checked={v.enabled}
            onChange={set('enabled')}
            help={
              v.enabled
                ? 'On. Machines pull bases through the controller.'
                : 'Off. Machines pull bases from the upstream themselves.'
            }
          />
          <Facts
            items={[
              { name: 'Folder', value: mirror.folder },
              { name: 'Disk used', value: formatSize(mirror.used_bytes) },
            ]}
          />
        </Stack>
      </Section>
      <Section title="Settings">
        <Stack gap="tight">
          <TextField
            label="Upstreams"
            help="Separate registries with commas."
            value={v.upstreams}
            onChange={set('upstreams')}
            width="full"
          />
          <NumberField
            label="Budget"
            unit="GB"
            min={1}
            max={2000}
            value={v.budget}
            onChange={(n) => set('budget')(n ?? base.budget)}
            changed={v.budget !== base.budget}
          />
          <NumberField
            label="Check a tag again after"
            unit="minutes"
            min={1}
            max={10080}
            value={v.recheck}
            onChange={(n) => set('recheck')(n ?? base.recheck)}
            changed={v.recheck !== base.recheck}
          />
        </Stack>
      </Section>
      {save.error ? (
        <Message tone="problem">{`Could not save. ${save.error}`}</Message>
      ) : null}
      <EditBar
        changes={changes}
        onSave={() => void save.run()}
        onCancel={() => setDraft({})}
        busy={save.busy}
        submit={false}
      />
    </Stack>
  );
}
