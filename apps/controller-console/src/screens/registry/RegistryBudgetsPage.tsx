// Budgets and cleanup rules of the registry (RES20, REG5 R5.37; UX-71): the two limits and the days. A limit too
// high for the disk is refused by the server and the page says by how much.
import {
  Breadcrumbs,
  CardGrid,
  EditBar,
  Facts,
  LinkButton,
  Message,
  NumberField,
  Page,
  PageHeader,
  Section,
  Stack,
  formatDate,
  formatSize,
} from '@crsp/ui';
import { useState } from 'react';
import { unwrap, useClient, type Schemas } from '../../api/api';
import { useCommand, useLoad } from '../../api/hooks';
import { Load } from '../common';
import { BudgetCard } from './RegistryTab';
import { GB } from './words';

export function RegistryBudgetsPage() {
  const client = useClient();
  const { result, reload } = useLoad(async () => {
    const [budgets, settings] = await Promise.all([
      unwrap(client.GET('/api/v1/registry/budgets')),
      unwrap(client.GET('/api/v1/registry/settings')),
    ]);
    return { budgets, settings };
  }, []);
  // The counts and the cleanup dates are in the overview; the list of images is not needed here.
  const counts = useLoad(
    () => unwrap(client.GET('/api/v1/registry/overview')),
    [],
  ).result;
  const overview = counts.state === 'ready' ? counts.data : undefined;
  return (
    <Page>
      <Breadcrumbs
        trail={[
          { to: '/setup/images?tab=registry', label: '‹ Registry' },
          { label: 'Budgets' },
        ]}
      />
      <PageHeader
        title="Registry budgets"
        lead="How much disk the registry may use, and when crsp suggests a deletion."
      />
      <Load result={result} what="the budgets" reload={reload}>
        {(d) => (
          <BudgetsBody
            budgets={d.budgets}
            settings={d.settings}
            overview={overview}
            reload={reload}
          />
        )}
      </Load>
    </Page>
  );
}

function BudgetsBody({
  budgets,
  settings,
  overview,
  reload,
}: {
  budgets: Schemas['RegistryBudgets'];
  settings: Schemas['RegistryStoreSettings'];
  overview: Schemas['RegistryOverview'] | undefined;
  reload: () => void;
}) {
  const client = useClient();
  const base = {
    standard: Math.round(budgets.standard.limit_bytes / GB),
    custom: Math.round(budgets.custom.limit_bytes / GB),
    unused: settings.unused_days,
    keep: settings.keep_old_days,
  };
  const [draft, setDraft] = useState<Partial<typeof base>>({});
  const v = { ...base, ...draft };
  const changes = (Object.keys(base) as (keyof typeof base)[]).filter(
    (k) => v[k] !== base[k],
  ).length;
  const save = useCommand(async () => {
    if (v.standard !== base.standard || v.custom !== base.custom)
      await unwrap(
        client.PUT('/api/v1/registry/budgets', {
          body: { standard_bytes: v.standard * GB, custom_bytes: v.custom * GB },
        }),
      );
    if (v.unused !== base.unused || v.keep !== base.keep) {
      const { alone_rules: _alone, folder_pending: _pending, ...input } = settings;
      await unwrap(
        client.PUT('/api/v1/registry/settings', {
          body: { ...input, unused_days: v.unused, keep_old_days: v.keep },
        }),
      );
    }
    setDraft({});
    reload();
  });
  const set = (k: keyof typeof base) => (n: number | undefined) =>
    setDraft((d) => ({ ...d, [k]: n ?? base[k] }));
  return (
    <Stack>
      <Section title="Disk used now">
        <CardGrid>
          <BudgetCard
            title="Standard images"
            budget={budgets.standard}
            count={overview?.images.standard ?? 0}
          />
          <BudgetCard
            title="Custom images"
            budget={budgets.custom}
            count={overview?.images.custom ?? 0}
          />
        </CardGrid>
      </Section>
      <Section
        title="Budgets"
        hint={`Over a budget, crsp recommends what to delete. It never deletes for this reason without your yes. Together the two may be at most ${formatSize(budgets.max_total_bytes)}, half the free disk.`}
      >
        <Stack gap="tight">
          <NumberField
            label="Standard images"
            unit="GB"
            min={1}
            max={2000}
            value={v.standard}
            onChange={set('standard')}
            help="crsp's own profile images."
            changed={v.standard !== base.standard}
          />
          <NumberField
            label="Custom images"
            unit="GB"
            min={1}
            max={2000}
            value={v.custom}
            onChange={set('custom')}
            help="Images made for your repositories, branch variants included."
            changed={v.custom !== base.custom}
          />
        </Stack>
      </Section>
      <Section
        title="Cleanup rules"
        hint="crsp does the first two alone. An unused image and an image over a budget wait for your yes."
      >
        <Stack gap="tight">
          <NumberField
            label="Suggest deleting an image unused for"
            unit="days"
            min={1}
            max={3650}
            value={v.unused}
            onChange={set('unused')}
            help="An image no run has pulled for this long shows up in Recommendations."
            changed={v.unused !== base.unused}
          />
          <NumberField
            label="Keep a replaced image for"
            unit="days"
            min={0}
            max={365}
            value={v.keep}
            onChange={set('keep')}
            help="So you can go back. After that crsp deletes it."
            changed={v.keep !== base.keep}
          />
          <Facts
            items={[
              { name: 'Folder', value: settings.folder },
              ...(overview?.last_cleanup
                ? [
                    {
                      name: 'Last cleanup',
                      value: formatDate(overview.last_cleanup.started_at),
                    },
                  ]
                : []),
              ...(overview?.next_cleanup_at
                ? [{ name: 'Next cleanup', value: formatDate(overview.next_cleanup_at) }]
                : []),
            ]}
          />
        </Stack>
      </Section>
      <Section title="Mirror of upstream images">
        <LinkButton to="/setup/images/registry/mirror">Open the mirror</LinkButton>
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
