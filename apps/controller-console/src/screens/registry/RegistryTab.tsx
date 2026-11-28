// The Registry tab of Images and caches (RES20, REG5 R5.37): the disk against two budgets, what the registry would
// do, and one row per image. Reads the real API; an answer of 501 shows the normal failed state (R5.31).
import {
  ActionBar,
  Button,
  Card,
  CardGrid,
  Code,
  Cut,
  Empty,
  Facts,
  LevelBar,
  MeterList,
  LinkButton,
  Message,
  Section,
  Segmented,
  Stack,
  Status,
  Table,
  Tag,
  formatDate,
  formatDuration,
  formatNumber,
  formatSize,
  useNow,
  type Column,
} from '@crsp/ui';
import { useState } from 'react';
import { useNavigate } from 'react-router-dom';
import { unwrap, useClient, type Schemas } from '../../api/api';
import { useLoad } from '../../api/hooks';
import { Load } from '../common';
import {
  decidedBy,
  imageName,
  lastPulled,
  recommendationTitle,
  recommendationTo,
  registryNext,
  shortKey,
  stateOf,
  type Recommendation,
  type Summary,
} from './words';

type Budget = Schemas['RegistryBudget'];

/** What the tab and the pages around it read: the overview and the list of images. */
export function useRegistry() {
  const client = useClient();
  return useLoad(async () => {
    const [overview, list] = await Promise.all([
      unwrap(client.GET('/api/v1/registry/overview')),
      unwrap(client.GET('/api/v1/registry/images')),
    ]);
    return { overview, images: list.items };
  }, []);
}

export function RegistryTab() {
  const { result, reload } = useRegistry();
  const navigate = useNavigate();
  const data = result.state === 'loading' ? undefined : result.data;
  const next = data ? registryNext(data.overview.recommendations) : undefined;
  return (
    <>
      <Load result={result} what="the registry" reload={reload}>
        {(d) => <RegistryBody overview={d.overview} images={d.images} />}
      </Load>
      {next ? (
        <ActionBar
          status={next.reason}
          primary={
            <Button kind="primary" onPress={() => void navigate(next.to)}>
              {next.label}
            </Button>
          }
        />
      ) : null}
    </>
  );
}

function RegistryBody({
  overview,
  images,
}: {
  overview: Schemas['RegistryOverview'];
  images: readonly Summary[];
}) {
  const now = useNow();
  const [kind, setKind] = useState<Summary['kind'] | ''>('');
  const shown = images.filter((i) => kind === '' || i.kind === kind);
  const budget = (k: Summary['kind']) => overview.budgets.find((b) => b.kind === k);
  const week = overview.last_7_days;
  const recCols: Column<Recommendation>[] = [
    {
      id: 'what',
      header: 'Recommendation',
      cell: (r) => <Cut>{recommendationTitle(r)}</Cut>,
      title: (r) => recommendationTitle(r),
      kind: 'grow',
    },
    { id: 'saves', header: 'Frees', cell: (r) => formatSize(r.frees_bytes), kind: 'fit' },
    {
      id: 'who',
      header: 'Decided by',
      cell: (r) => <Tag>{decidedBy(r)}</Tag>,
      kind: 'fit',
    },
  ];
  const cols: Column<Summary>[] = [
    {
      id: 'profile',
      header: 'Image',
      cell: (i) => <Cut>{i.profile}</Cut>,
      title: (i) => imageName(i),
      sort: (i) => i.profile,
      kind: 'grow',
    },
    {
      id: 'key',
      header: 'Key',
      cell: (i) => <Code>{shortKey(i.key)}</Code>,
      kind: 'fit',
    },
    { id: 'kind', header: 'Kind', cell: (i) => i.kind, sort: (i) => i.kind, kind: 'fit' },
    { id: 'arch', header: 'Arch', cell: (i) => i.arch, kind: 'fit' },
    {
      id: 'size',
      header: 'Size',
      cell: (i) => formatSize(i.size_bytes),
      sort: (i) => i.size_bytes,
      kind: 'fit',
    },
    {
      id: 'pulls',
      header: 'Pulls',
      cell: (i) => formatNumber(i.pulls_7_days),
      sort: (i) => i.pulls_7_days,
      kind: 'fit',
    },
    { id: 'last', header: 'Last pulled', cell: (i) => lastPulled(i, now), kind: 'fit' },
    {
      id: 'state',
      header: 'State',
      cell: (i) => <Status {...stateOf(i, { unusedDays: i.unused_days })} />,
      sort: (i) => i.state,
      kind: 'fit',
    },
  ];
  const std = budget('standard');
  const cus = budget('custom');
  return (
    <Stack>
      {!overview.where.running ? (
        <Message tone="problem">
          {`The registry is not running. ${overview.where.why_not ?? 'Machines build their images themselves until it runs.'}`}
        </Message>
      ) : null}
      {overview.where.read_only ? (
        <Message tone="note">
          The registry is cleaning up. Machines can pull; new images wait until it ends.
        </Message>
      ) : null}
      <Section
        title="Disk"
        hint="Standard and custom images have a budget each. Layers two images share are stored once."
      >
        <CardGrid>
          {std ? (
            <BudgetCard
              title="Standard images"
              budget={std}
              count={overview.images.standard}
            />
          ) : null}
          {cus ? (
            <BudgetCard
              title="Custom images"
              budget={cus}
              count={overview.images.custom}
            />
          ) : null}
          <Card title="Last 7 days">
            <Facts
              items={[
                { name: 'Pulls', value: formatNumber(week.pulls) },
                { name: 'Builds avoided', value: formatNumber(week.builds_avoided) },
                { name: 'Time saved', value: formatDuration(week.saved_seconds) },
              ]}
            />
          </Card>
          <Card
            title="Where it runs"
            footer={
              <LinkButton to="/setup/images/registry/budgets">Edit budgets</LinkButton>
            }
          >
            <Facts
              items={[
                { name: 'Machine', value: `${overview.where.host}, the controller` },
                { name: 'Folder', value: <Code>{overview.where.folder}</Code> },
                ...(overview.next_cleanup_at
                  ? [
                      {
                        name: 'Next cleanup',
                        value: formatDate(overview.next_cleanup_at),
                      },
                    ]
                  : []),
              ]}
            />
          </Card>
        </CardGrid>
      </Section>
      <Section
        title="Recommendations"
        count={overview.recommendations.length}
        hint="crsp never deletes what you may still want without your yes."
      >
        {overview.recommendations.length === 0 ? (
          <Empty title="Nothing to recommend. The registry is within its budgets." />
        ) : (
          <Table
            caption="Recommendations"
            columns={recCols}
            rows={overview.recommendations}
            rowKey={(r) => r.id}
            oneLine
            action={{
              label: 'Open',
              to: (r) => recommendationTo(r),
              name: (r) => recommendationTitle(r),
            }}
          />
        )}
      </Section>
      <Section title="Images in the registry" count={shown.length}>
        {images.length === 0 ? (
          <Empty title="No images yet. The first machine that builds a job image pushes it here." />
        ) : (
          <Stack gap="tight">
            <Segmented
              label="Kind"
              value={kind}
              onChange={setKind}
              allLabel="All"
              options={[
                { value: 'standard', label: 'Standard' },
                { value: 'custom', label: 'Custom' },
              ]}
            />
            <Table
              caption="Images in the registry"
              columns={cols}
              rows={shown}
              rowKey={(i) => i.id}
              oneLine
              action={{
                label: 'Open',
                to: (i) => `/setup/images/registry/${i.id}`,
                name: (i) => `${imageName(i)} ${shortKey(i.key)}`,
              }}
            />
          </Stack>
        )}
      </Section>
    </Stack>
  );
}

export function BudgetCard({
  title,
  budget,
  count,
}: {
  title: string;
  budget: Budget;
  count: number;
}) {
  return (
    <Card title={title}>
      <Stack gap="tight">
        <MeterList>
          <LevelBar
            label={title}
            value={budget.used_bytes}
            max={Math.max(budget.limit_bytes, 1)}
            text={`${formatSize(budget.used_bytes)} of ${formatSize(budget.limit_bytes)}${budget.over ? ', over the budget' : ''}`}
          />
        </MeterList>
        <Facts
          items={[
            { name: 'Images', value: formatNumber(count) },
            { name: 'Shared layers', value: formatSize(budget.shared_bytes) },
          ]}
        />
      </Stack>
    </Card>
  );
}
