// One image (LOOK12, owner decision LOOK D23): its layers as a plain checklist, one primary action, and a folded
// part for what is inside. Saving the layers also asks for the rebuild.
import {
  Breadcrumbs,
  Button,
  CheckRow,
  Disclosure,
  Message,
  Page,
  PageHeader,
  Pane,
  Progress,
  Stack,
  Text,
  formatDateTime,
  formatSize,
  EditBar,
} from '@crsp/ui';
import { useMemo, useState } from 'react';
import { useParams } from 'react-router-dom';
import { unwrap, useClient, type Schemas } from '../../api/api';
import { useCommand, useLoad } from '../../api/hooks';
import { useSession } from '../../auth/session';
import { useVersion } from '../../live/LiveProvider';
import { Ask, Load } from '../common';
import { useAskFirst } from './ImagesAndCaches';
import { imageTitle, missingOf, usedBy } from './labels';

type Image = Schemas['Image'];

const toolText = (t: Schemas['ImageTool']): string =>
  t.version ? `${t.name} ${t.version}` : t.name;

export function ImageDetail() {
  const { imageId = '' } = useParams();
  const client = useClient();
  const v = useVersion('images');
  const mv = useVersion('machines');
  const { result, reload } = useLoad(
    () =>
      Promise.all([
        unwrap(client.GET('/api/v1/images/{imageId}', { params: { path: { imageId } } })),
        unwrap(client.GET('/api/v1/machines')),
      ]),
    [v, mv, imageId],
  );
  const ready = result.state === 'ready' ? result.data : undefined;
  const machine = ready
    ? (ready[1].items.find((m) => m.id === ready[0].machine_id)?.name ??
      ready[0].machine_id)
    : undefined;
  return (
    <Page>
      <Breadcrumbs
        trail={[
          { to: '/setup/images', label: '‹ Images' },
          { label: machine ?? 'Image' },
        ]}
      />
      {ready ? null : (
        <PageHeader title="Image" lead="One image: its layers and what is inside." />
      )}
      <Load result={result} what="the image" reload={reload}>
        {([image]) => (
          <ImageBody
            key={image.id}
            image={image}
            machine={machine ?? image.machine_id}
            onChanged={reload}
          />
        )}
      </Load>
    </Page>
  );
}

function ImageBody({
  image,
  machine,
  onChanged,
}: {
  image: Image;
  machine: string;
  onChanged: () => void;
}) {
  const client = useClient();
  const session = useSession();
  const admin = session.can('admin');
  const why = session.whyNot('admin');
  const saved = useMemo(
    () => image.layers.filter((l) => l.enabled).map((l) => l.id),
    [image.layers],
  );
  const [draft, setDraft] = useState<string[] | undefined>(undefined);
  const enabled = draft ?? saved;
  const dirty =
    draft !== undefined && [...draft].sort().join() !== [...saved].sort().join();
  const layerChanges = draft
    ? draft.filter((x) => !saved.includes(x)).length +
      saved.filter((x) => !draft.includes(x)).length
    : 0;
  const rebuilding = image.state === 'rebuilding';
  const notBuilt = image.state === 'not_built';
  const editable = admin && !rebuilding;
  const miss = missingOf(image, enabled);
  const users = usedBy(image);
  const title = imageTitle(machine, image);

  const save = useCommand(async () => {
    await unwrap(
      client.PUT('/api/v1/images/{imageId}/layers', {
        params: { path: { imageId: image.id } },
        body: { enabled_layers: enabled },
      }),
    );
    setDraft(undefined);
    onChanged();
  });
  const { ask, probe } = useAskFirst({
    probe: () =>
      unwrap(
        client.POST('/api/v1/images/{imageId}/rebuild', {
          params: { path: { imageId: image.id } },
        }),
      ),
    commit: () =>
      unwrap(
        client.POST('/api/v1/images/{imageId}/rebuild', {
          params: { path: { imageId: image.id }, query: { confirm: true } },
        }),
      ),
    fallback: () => [`Image of ${title}`],
    onDone: onChanged,
  });

  const toggle = (id: string) =>
    setDraft(enabled.includes(id) ? enabled.filter((x) => x !== id) : [...enabled, id]);
  const addMissing = () =>
    setDraft([...new Set([...enabled, ...miss.layers.map((l) => l.id)])]);

  const facts = notBuilt
    ? 'Not built yet. Choose layers now; it is built when its first runner starts.'
    : [
        image.built_at ? `Built ${formatDateTime(image.built_at)}` : undefined,
        formatSize(image.size_bytes),
        users.length > 0 ? `used by ${users.join(', ')}` : undefined,
      ]
        .filter(Boolean)
        .join(' · ');

  return (
    <Stack>
      <PageHeader title={machine} lead={facts} />
      {editable && miss.layers.length > 0 ? (
        <Message
          tone="needs-you"
          action={
            <Button onPress={addMissing}>
              {miss.layers.length === 1 ? 'Add it' : 'Add them'}
            </Button>
          }
        >
          {`${miss.repos.join(', ')} ${miss.repos.length === 1 ? 'needs' : 'need'} ${miss.layers.map((l) => l.name).join(', ')}`}
        </Message>
      ) : null}
      <Pane title="Layers">
        <Text tone="muted">
          Add what your repositories' jobs need. The basics are always included.
        </Text>
        {image.layers.map((l) => {
          const needed = l.needed_by.count > 0;
          const on = enabled.includes(l.id);
          return (
            <CheckRow
              key={l.id}
              stacked
              label={l.name}
              help={l.purpose}
              checked={on}
              changed={on !== saved.includes(l.id)}
              onChange={() => toggle(l.id)}
              disabledReason={
                editable ? undefined : !admin ? why : 'The image is being rebuilt.'
              }
              aside={needed ? `needed by ${l.needed_by.count}` : undefined}
              asideTone={needed && !on ? 'needs-you' : undefined}
            />
          );
        })}
      </Pane>
      {rebuilding ? <Progress label={`Rebuilding ${title}`} /> : null}
      {image.state === 'failed' && image.error ? (
        <Message tone="problem">
          Could not rebuild. {image.error} The old image keeps serving.
        </Message>
      ) : null}
      {save.error ? (
        <Message tone="problem">Could not save the layers. {save.error}</Message>
      ) : null}
      {probe.error ? (
        <Message tone="problem">Could not rebuild. {probe.error}</Message>
      ) : null}
      <Disclosure summary="What is inside this image">
        <Text>Always included: {image.base_tools.map(toolText).join(' · ')}</Text>
        <Text>Extra packages set by repositories:</Text>
        {image.extra_packages.length === 0 ? (
          <Text tone="muted">None</Text>
        ) : (
          image.extra_packages.map((e) => (
            <Text key={e.repository}>
              {e.repository}: {e.packages.join(', ')}
            </Text>
          ))
        )}
        {!notBuilt ? (
          <Button
            onPress={() => void probe.run()}
            busy={probe.busy || rebuilding}
            busyLabel={rebuilding ? 'Rebuilding' : 'Working'}
            disabled={!admin}
            reason={why}
          >
            Rebuild
          </Button>
        ) : null}
      </Disclosure>
      <Ask
        title={`Rebuild the image of ${title}?`}
        action="Rebuild"
        busyLabel="Rebuilding"
        q={ask.question}
        busy={ask.busy}
        error={ask.error}
        onCancel={ask.cancel}
        onConfirm={() => void ask.confirm()}
      />
      {dirty && !notBuilt ? (
        <Text tone="muted">
          Rebuilds after running jobs finish. The old image keeps working until then.
        </Text>
      ) : null}
      <EditBar
        changes={layerChanges}
        submit={false}
        onSave={() => void save.run()}
        onCancel={() => setDraft(undefined)}
        busy={save.busy}
        blocked={!admin || rebuilding}
        why={!admin ? why : 'The image is being rebuilt.'}
        saveLabel={notBuilt ? 'Save' : 'Save and rebuild'}
      />
    </Stack>
  );
}
