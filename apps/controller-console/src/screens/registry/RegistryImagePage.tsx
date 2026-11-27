// One image of the registry (RES20, REG5 R5.37, R5.40): where it came from, who uses it, what is in it. Delete is
// here only, behind a question card that names what goes (UX-62); nothing on a list deletes (R5.40).
import {
  ActionBar,
  Breadcrumbs,
  Button,
  Code,
  Cut,
  Facts,
  LinkButton,
  Message,
  Page,
  PageHeader,
  Section,
  Stack,
  Status,
  Table,
  Text,
  TrendChart,
  formatCount,
  formatDate,
  formatDuration,
  formatNumber,
  formatSize,
  useNow,
  type Column,
} from '@crsp/ui';
import { useNavigate, useParams } from 'react-router-dom';
import { ApiError, unwrap, useClient, type Schemas } from '../../api/api';
import { useCommand } from '../../api/hooks';
import { useLoad } from '../../api/hooks';
import { Ask, Load } from '../common';
import { useAskFirst } from '../images/ImagesAndCaches';
import { imageName, lastPulled, shortKey, stateOf } from './words';

type Image = Schemas['RegistryImage'];
type Machine = Image['machines'][number];
type Layer = Image['layers'][number];
type Pointer = Image['pointers'][number];
type Lineage = Image['lineage'][number];

const REFUSED =
  'A runner uses this image. The registry refuses to delete it until that job ends.';

export function RegistryImagePage() {
  const { id = '' } = useParams();
  const client = useClient();
  const { result, reload } = useLoad(
    () =>
      unwrap(
        client.GET('/api/v1/registry/images/{registryImageId}', {
          params: { path: { registryImageId: id } },
        }),
      ),
    [id],
  );
  return (
    <Page>
      <Breadcrumbs
        trail={[
          { to: '/setup/images?tab=registry', label: '‹ Registry' },
          { label: 'Image' },
        ]}
      />
      {result.state === 'ready' ? null : <PageHeader title="Image" />}
      <Load result={result} what="the image" reload={reload}>
        {(img) => <ImageBody image={img} />}
      </Load>
    </Page>
  );
}

function ImageBody({ image }: { image: Image }) {
  const now = useNow();
  const navigate = useNavigate();
  const client = useClient();
  const inUse = image.in_use === true;
  const word = stateOf(image, { inUse });
  const path = { params: { path: { registryImageId: image.id } } };
  const backToList = () => void navigate('/setup/images?tab=registry');
  const { ask, probe } = useAskFirst({
    // An image a runner uses is refused with `conflict` and the job named: the question card opens all the same and
    // names the job it waits for, and stays open (UX-73).
    probe: async () => {
      try {
        return await unwrap(
          client.DELETE('/api/v1/registry/images/{registryImageId}', path),
        );
      } catch (e) {
        if (e instanceof ApiError && e.code === 'conflict')
          throw new ApiError('needs_confirmation', e.message, e.status, e.details);
        throw e;
      }
    },
    commit: () =>
      unwrap(
        client.DELETE('/api/v1/registry/images/{registryImageId}', {
          params: { path: path.params.path, query: { confirm: true } },
        }),
      ),
    fallback: () => [
      `${imageName(image)}, ${image.arch}, key ${shortKey(image.key)}`,
      `${formatSize(image.unique_bytes)} freed; the layers shared with other images stay`,
    ],
    onDone: backToList,
  });
  // Go back to a superseded image: a plan that makes this image the new one, shown in the replace wizard.
  const goBack = useCommand(async () => {
    const plan = await unwrap(
      client.POST('/api/v1/registry/replace-plans', {
        body: { new_image_id: image.id },
      }),
    );
    void navigate(`/setup/images/registry/${image.id}/replace?plan=${plan.id}`);
  });
  const lineage = image.lineage.filter((l) => l.image_id !== image.id);
  const relation = (l: Lineage) =>
    l.relation === 'replaces'
      ? 'It replaces'
      : l.relation === 'replaced_by'
        ? 'Replaced by'
        : 'Same profile';
  const lineageCols: Column<Lineage>[] = [
    { id: 'rel', header: 'Relation', cell: relation, kind: 'fit' },
    {
      id: 'img',
      header: 'Image',
      cell: (l) => (
        <Cut>{l.branch ? `${image.profile}, variant of ${l.branch}` : image.profile}</Cut>
      ),
      kind: 'grow',
    },
    {
      id: 'key',
      header: 'Key',
      cell: (l) => <Code>{shortKey(l.key)}</Code>,
      kind: 'fit',
    },
    {
      id: 'state',
      header: 'State',
      cell: (l) => (
        <Status {...stateOf({ state: l.state, update_found: false, branch: l.branch })} />
      ),
      kind: 'fit',
    },
  ];
  const layerCols: Column<Layer>[] = [
    {
      id: 'd',
      header: 'Layer',
      cell: (l) => <Code>{l.digest.replace('sha256:', '').slice(0, 12)}</Code>,
      kind: 'grow',
    },
    { id: 's', header: 'Size', cell: (l) => formatSize(l.size_bytes), kind: 'fit' },
    {
      id: 'w',
      header: 'Shared',
      cell: (l) =>
        l.shared_with > 0 ? formatCount(l.shared_with, 'other image') : 'only here',
      kind: 'fit',
    },
  ];
  const machineCols: Column<Machine>[] = [
    { id: 'm', header: 'Machine', cell: (m) => m.machine_name, kind: 'grow' },
    {
      id: 's',
      header: 'State',
      cell: (m) => (
        <Status
          kind={m.in_use_by_runner ? 'running' : m.holds ? 'ok' : 'off'}
          word={m.in_use_by_runner ? 'used now' : m.holds ? 'holds a copy' : 'no copy'}
        />
      ),
      kind: 'fit',
    },
    {
      id: 'p',
      header: 'Pulled',
      cell: (m) => (m.last_used_at ? formatDate(m.last_used_at) : '–'),
      kind: 'fit',
    },
  ];
  const pointerCols: Column<Pointer>[] = [
    {
      id: 'r',
      header: 'Repository',
      cell: (p) => <Cut>{p.repository}</Cut>,
      kind: 'grow',
    },
    { id: 'p', header: 'Profile', cell: (p) => p.profile, kind: 'fit' },
    { id: 'm', header: 'Moved', cell: (p) => formatDate(p.moved_at), kind: 'fit' },
  ];
  const week = image.pulls_by_day;
  const scan = image.scan;
  const scanned = scan !== undefined && scan.state === 'done';
  const keepUntil = image.keep_until
    ? formatDate(image.keep_until)
    : 'the rollback days end';
  return (
    <Stack gap="section">
      <PageHeader
        title={image.profile}
        lead={`${image.kind} · ${image.arch} · key ${shortKey(image.key)} · pushed on ${formatDate(image.pushed_at)}`}
      />
      <Section title="State">
        <Stack gap="tight">
          <Status {...word} />
          {image.update_found ? (
            <Message tone="needs-you">
              {`A run of ${image.source_repository ?? 'a repository'} needs this newer version. The image it uses now stays until you say yes.`}
            </Message>
          ) : null}
          {image.state === 'superseded' ? (
            <Message tone="note">
              {`A newer image took its place. It is kept until ${keepUntil} so you can go back, then it is deleted.`}
            </Message>
          ) : null}
          {goBack.error ? (
            <Message tone="problem">{`Could not go back. ${goBack.error}`}</Message>
          ) : null}
          {image.state === 'variant' ? (
            <Message tone="note">
              {`Made for the branch ${image.branch ?? ''}, whose stack files differ from main.${image.variant_gone_days !== undefined ? ` It is deleted ${image.variant_gone_days} days after the branch is merged or deleted.` : ''}`}
            </Message>
          ) : null}
          {image.state === 'staged' ? (
            <Message tone="note">
              The registry has not checked this image yet. It is offered to machines when
              it passes.
            </Message>
          ) : null}
          {image.state === 'refused' ? (
            <Message tone="problem">
              The registry refused this image. It stays in staging until the next cleanup.
            </Message>
          ) : null}
          <Facts
            items={[
              { name: 'Size', value: formatSize(image.size_bytes) },
              { name: 'Only this image holds', value: formatSize(image.unique_bytes) },
              { name: 'Shared layers', value: formatSize(image.shared_bytes ?? 0) },
              ...(image.source_repository
                ? [{ name: 'Repositories', value: image.source_repository }]
                : []),
              { name: 'Last pulled', value: lastPulled(image, now) },
              { name: 'Pulls', value: `${formatNumber(image.pulls_7_days)} this week` },
              ...(image.saved_seconds !== undefined
                ? [
                    {
                      name: 'Time saved this week',
                      value: formatDuration(image.saved_seconds),
                    },
                  ]
                : []),
            ]}
          />
        </Stack>
      </Section>
      <Section
        title="Where it came from"
        hint="The key is made from the profile, the content of the stack files at the commit, the arch and the base image."
      >
        <Stack gap="tight">
          <Facts
            items={[
              { name: 'Profile', value: image.profile },
              ...(image.base_name
                ? [{ name: 'Base', value: <Code>{image.base_name}</Code> }]
                : []),
              ...(image.stack_hash
                ? [
                    {
                      name: 'Stack files',
                      value: <Code>{shortKey(image.stack_hash)}</Code>,
                    },
                  ]
                : []),
            ]}
          />
          {lineage.length > 0 ? (
            <Table
              caption="Lineage"
              columns={lineageCols}
              rows={lineage}
              rowKey={(l) => l.image_id}
              oneLine
              action={{
                label: 'Open',
                to: (l) => `/setup/images/registry/${l.image_id}`,
                name: (l) => `${relation(l)} ${shortKey(l.key)}`,
              }}
            />
          ) : (
            <Text tone="muted">
              It does not replace an image and nothing replaced it.
            </Text>
          )}
        </Stack>
      </Section>
      {image.layers.length > 0 ? (
        <Section
          title="Layers shared with other images"
          count={image.layers.length}
          hint="A shared layer is stored once. Deleting this image frees only what no other image holds."
        >
          <Table
            caption="Layers shared with other images"
            columns={layerCols}
            rows={image.layers}
            rowKey={(l) => l.digest}
            oneLine
          />
        </Section>
      ) : null}
      <Section title="Pulls by day" hint="The last 14 days.">
        <Stack gap="tight">
          <TrendChart
            label={`Pulls of ${image.profile} ${shortKey(image.key)} by day`}
            unit="count"
            emptyText="No pulls in the last 14 days."
            series={[
              {
                id: 'pulls',
                label: 'Pulls',
                kind: 'column',
                points: week.map((d) => ({
                  x: formatDate(d.day),
                  y: d.pulls,
                  note: `${formatCount(d.pulls, 'pull')} on ${formatDate(d.day)}`,
                })),
              },
            ]}
          />
          {image.pulls_text ? <Text tone="muted">{image.pulls_text}</Text> : null}
        </Stack>
      </Section>
      <Section title="Machines that hold it" count={image.machines.length}>
        {image.machines.length > 0 ? (
          <Table
            caption="Machines that hold it"
            columns={machineCols}
            rows={image.machines}
            rowKey={(m) => m.machine_id}
            oneLine
          />
        ) : (
          <Text tone="muted">No machine holds a copy. Nothing runs from it.</Text>
        )}
      </Section>
      {image.pointers.length > 0 ? (
        <Section title="Repositories that use it" count={image.pointers.length}>
          <Table
            caption="Repositories that use it"
            columns={pointerCols}
            rows={image.pointers}
            rowKey={(p) => `${p.repository}/${p.profile}/${p.arch}`}
            oneLine
          />
        </Section>
      ) : null}
      <Section
        title="What is in it"
        hint="Made by crsp when the registry stored the image. It never blocks a pull."
      >
        {scanned ? (
          <Facts
            items={[
              { name: 'Software', value: `${formatNumber(scan.packages ?? 0)} packages` },
              {
                name: 'Known vulnerabilities',
                value: `${scan.critical ?? 0} critical, ${scan.high ?? 0} high, ${scan.medium ?? 0} medium, ${scan.low ?? 0} low`,
              },
            ]}
          />
        ) : (
          <Text tone="muted">
            {scan?.state === 'failed'
              ? `The scan failed. ${scan.error ?? ''}`.trim()
              : 'Not scanned yet. The result shows here when the scan ends.'}
          </Text>
        )}
      </Section>
      <Section
        title="Delete"
        hint="Deleting removes the image from the registry. Machines that hold a copy keep it until their next sweep."
        actions={
          <Button onPress={() => void probe.run()} busy={probe.busy} busyLabel="Checking">
            Delete
          </Button>
        }
      >
        <Stack gap="tight">
          <Text tone="muted">
            {inUse ? REFUSED : `${formatSize(image.unique_bytes)} would be freed.`}
          </Text>
          {probe.error ? (
            <Message tone="problem">{`Could not delete. ${probe.error}`}</Message>
          ) : null}
        </Stack>
      </Section>
      <Ask
        title={`Delete ${image.profile} ${shortKey(image.key)}?`}
        action="Delete"
        busyLabel="Deleting"
        q={ask.question}
        busy={ask.busy}
        error={ask.error}
        onCancel={ask.cancel}
        onConfirm={() => void ask.confirm()}
      />
      {image.update_found ? (
        <ActionBar
          status="Nothing changes until you confirm."
          secondary={
            <LinkButton to="/setup/images?tab=registry">Open the registry</LinkButton>
          }
          primary={
            <Button
              kind="primary"
              onPress={() => void navigate(`/setup/images/registry/${image.id}/replace`)}
            >
              Review the replacement
            </Button>
          }
        />
      ) : image.state === 'superseded' ? (
        <ActionBar
          status="Nothing changes until you confirm."
          secondary={
            <LinkButton to="/setup/images?tab=registry">Open the registry</LinkButton>
          }
          primary={
            <Button
              kind="primary"
              onPress={() => void goBack.run()}
              busy={goBack.busy}
              busyLabel="Going back"
            >
              Go back to this one
            </Button>
          }
        />
      ) : (
        <ActionBar
          primary={
            <Button kind="primary" onPress={backToList}>
              Open the registry
            </Button>
          }
        />
      )}
    </Stack>
  );
}
