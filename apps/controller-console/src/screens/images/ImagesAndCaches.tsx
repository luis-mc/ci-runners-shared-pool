// Images and caches (web-console 4.7, R5.25): what each machine keeps, and the questions before a rebuild or a
// wipe (R5.30). Images arrive with milestone 2; the caches of milestone 1 are the per-runner tool and Rust
// build caches.
import {
  Button,
  Card,
  CardGrid,
  Empty,
  Facts,
  LevelBar,
  Message,
  OpenRow,
  Page,
  Pane,
  Progress,
  Section,
  Stack,
  Status,
  ListToolbar,
  SearchBar,
  Tabs,
  Text,
  formatNumber,
  formatPast,
  formatPercent,
  formatSize,
  useNow,
  ActionBar,
} from '@crsp/ui';
import { useEffect, useMemo, useState } from 'react';
import { useNavigate, useSearchParams } from 'react-router-dom';
import { unwrap, useClient, type Schemas } from '../../api/api';
import { useLoad } from '../../api/hooks';
import { useSession } from '../../auth/session';
import { useLiveStore, useVersion } from '../../live/LiveProvider';
import { TIMING } from '../../timing';
import { Ask, Load } from '../common';
import { RegistryTab } from '../registry/RegistryTab';
import { useAskFirst } from './askFirst';
import { CacheHub } from './CacheHub';
import { cacheName, imageTitle, missingOf, wipingLabel } from './labels';

type Cache = Schemas['Cache'];

const TABS = [
  { id: 'images', label: 'Images' },
  { id: 'caches', label: 'Caches' },
  { id: 'registry', label: 'Registry' },
] as const;

export function ImagesAndCaches() {
  const [params, setParams] = useSearchParams();
  const navigate = useNavigate();
  const session = useSession();
  const raw = params.get('tab');
  const tab = raw === 'caches' || raw === 'registry' ? raw : 'images';
  const client = useClient();
  const mv = useVersion('machines');
  const { result: machines } = useLoad(
    () => unwrap(client.GET('/api/v1/machines')),
    [mv],
  );
  const names = useMemo(() => {
    const out = new Map<string, string>();
    if (machines.state === 'ready')
      for (const m of machines.data.items) out.set(m.id, m.name);
    return out;
  }, [machines]);
  const nameOf = (id: string) => names.get(id) ?? id;

  return (
    <Page>
      <Text tone="muted">What each machine keeps, and what you can rebuild or wipe.</Text>
      <Tabs
        label="Images and caches"
        tabs={TABS}
        current={tab}
        onChange={(id) => setParams(id === 'images' ? {} : { tab: id })}
      />
      {tab === 'images' ? <ImagesTab nameOf={nameOf} /> : null}
      {tab === 'caches' ? <CachesTab nameOf={nameOf} /> : null}
      {tab === 'registry' ? <RegistryTab /> : null}
      {session.can('admin') && tab !== 'registry' ? (
        <ActionBar
          primary={
            <Button kind="primary" onPress={() => void navigate('/setup/runners/add')}>
              Add runners
            </Button>
          }
        />
      ) : null}
    </Page>
  );
}

export { useAskFirst };

// ---------------------------------------------------------------------------------------------- images

function ImagesTab({ nameOf }: { nameOf: (id: string) => string }) {
  const client = useClient();
  const navigate = useNavigate();
  const v = useVersion('images');
  const { result, reload } = useLoad(() => unwrap(client.GET('/api/v1/images')), [v]);
  return (
    <Load result={result} what="images" reload={reload}>
      {(data) =>
        data.items.length === 0 ? (
          <Section title="Images" count={0}>
            <Empty title="No images yet. A machine shows one here once it can run container jobs." />
          </Section>
        ) : (
          <Section
            title="Images"
            hint="Each machine has one Linux image. Its repositories' jobs run from it."
          >
            {data.items.map((img) => {
              const miss = missingOf(img);
              const extra = img.layers.filter((l) => l.enabled).length;
              return (
                <OpenRow
                  key={img.id}
                  title={imageTitle(nameOf(img.machine_id), img)}
                  line={
                    miss.layers.length > 0 ? (
                      <Status
                        kind="waiting"
                        word={`Missing ${miss.layers.map((l) => l.name).join(', ')} · needed by ${miss.repos.join(', ')}`}
                      />
                    ) : img.state === 'not_built' ? (
                      <Text tone="muted">Not built yet</Text>
                    ) : (
                      <Status kind="ok" word="Has what its repositories need" />
                    )
                  }
                  aside={
                    extra > 0
                      ? `${extra} extra ${extra === 1 ? 'layer' : 'layers'}`
                      : 'base only'
                  }
                  onOpen={() =>
                    void navigate(`/setup/images/${encodeURIComponent(img.id)}`)
                  }
                />
              );
            })}
          </Section>
        )
      }
    </Load>
  );
}

// ---------------------------------------------------------------------------------------------- caches

function CachesTab({ nameOf }: { nameOf: (id: string) => string }) {
  const client = useClient();
  const live = useLiveStore();
  const v = useVersion('caches');
  const { result, reload } = useLoad(() => unwrap(client.GET('/api/v1/caches')), [v]);
  const items = result.state === 'ready' ? result.data.items : undefined;
  const anyWiping = items?.some((c) => c.state === 'wiping') ?? false;
  const [q, setQ] = useState('');

  // The strip and the list read the server's state (UX-19); a wipe in progress is asked again until it ends.
  useEffect(() => {
    for (const c of items ?? []) live.trackCache(c);
  }, [items, live]);
  useEffect(() => {
    if (!anyWiping) return;
    const t = setInterval(reload, TIMING.cachePollMs);
    return () => clearInterval(t);
  }, [anyWiping, reload]);

  return (
    <Stack>
      <CacheHub />
      <Load result={result} what="caches" reload={reload}>
        {(data) => {
          // The hub's kinds show on its cards above (caches R5.77), not again per machine.
          const own = data.items.filter(
            (c) => !HUB_KINDS.has(c.kind) || !c.id.startsWith('mirror:'),
          );
          if (own.length === 0)
            return (
              <Section title="Caches per machine" count={0}>
                <Empty title="No caches yet. Caches show here when a runner has been added." />
              </Section>
            );
          const byMachine = new Map<string, Cache[]>();
          const found = own.filter((c) =>
            `${cacheName(c)} ${nameOf(c.machine_id)}`
              .toLowerCase()
              .includes(q.trim().toLowerCase()),
          );
          for (const c of found)
            byMachine.set(c.machine_id, [...(byMachine.get(c.machine_id) ?? []), c]);
          return (
            <Section title="Caches per machine" count={found.length}>
              <Stack>
                <ListToolbar
                  label="Caches"
                  search={<SearchBar value={q} onChange={setQ} label="Search caches" />}
                  count={`${found.length} ${found.length === 1 ? 'cache' : 'caches'}`}
                />
                {[...byMachine.entries()].map(([machineId, rows]) => (
                  <Pane key={machineId} title={nameOf(machineId)} level={3}>
                    <CardGrid>
                      {rows.map((c) => (
                        <CacheCard key={c.id} cache={c} onChanged={reload} />
                      ))}
                    </CardGrid>
                  </Pane>
                ))}
              </Stack>
            </Section>
          );
        }}
      </Load>
    </Stack>
  );
}

/** The kinds the one hub holds; their controller rows show as hub cards. */
const HUB_KINDS: ReadonlySet<string> = new Set([
  'actions_cache',
  'git_mirror',
  'registry_mirror',
]);

function CacheCard({ cache, onChanged }: { cache: Cache; onChanged: () => void }) {
  const client = useClient();
  const session = useSession();
  const live = useLiveStore();
  const now = useNow();
  const admin = session.can('admin');
  const why = session.whyNot('admin');
  const wiping = cache.state === 'wiping';
  const name = cacheName(cache);

  const { ask, probe } = useAskFirst({
    probe: async () => {
      live.trackCache(
        await unwrap(
          client.POST('/api/v1/caches/{cacheId}/wipe', {
            params: { path: { cacheId: cache.id } },
          }),
        ),
      );
    },
    commit: async () => {
      live.trackCache(
        await unwrap(
          client.POST('/api/v1/caches/{cacheId}/wipe', {
            params: { path: { cacheId: cache.id }, query: { confirm: true } },
          }),
        ),
      );
    },
    fallback: () => [`${name}, ${formatSize(cache.size_bytes)}`],
    onDone: onChanged,
  });

  const cap = cache.cap_bytes;
  const waits = cache.waiting_for ?? [];
  return (
    <Card
      title={name}
      footer={
        <Button
          onPress={() => void probe.run()}
          busy={probe.busy || wiping}
          busyLabel={wiping ? 'Wiping' : 'Working'}
          disabled={!admin}
          reason={why}
        >
          Wipe
        </Button>
      }
    >
      <Stack gap="tight">
        {wiping ? <Status kind="running" word="wiping" /> : null}
        {cap !== undefined && cap > 0 ? (
          <LevelBar
            label="Size"
            value={cache.size_bytes}
            max={cap}
            text={`${formatSize(cache.size_bytes)} of ${formatSize(cap)} (${formatPercent((cache.size_bytes / cap) * 100)})${cache.size_bytes > cap ? ', over the cap' : ''}`}
          />
        ) : (
          <Text>{formatSize(cache.size_bytes)}, no cap</Text>
        )}
        <Facts
          items={[
            ...(cache.entry_count !== undefined
              ? [{ name: 'Entries', value: formatNumber(cache.entry_count) }]
              : []),
            {
              name: 'Last wiped',
              value: cache.last_wiped_at ? formatPast(cache.last_wiped_at, now) : 'never',
            },
          ]}
        />
        {wiping ? (
          <>
            <Progress label={wipingLabel(cache)} />
            <Text tone="muted">
              {waits.length > 0
                ? `Waiting for ${waits.join(', ')}.`
                : 'Wiping. No running job uses it.'}
            </Text>
          </>
        ) : null}
        {!wiping && cache.error ? (
          <Message tone="problem">
            Could not wipe {name}. {cache.error}
          </Message>
        ) : null}
        {probe.error ? (
          <Message tone="problem">
            Could not wipe {name}. {probe.error}
          </Message>
        ) : null}
      </Stack>
      <Ask
        title={`Wipe ${name}?`}
        action="Wipe"
        busyLabel="Wiping"
        q={ask.question}
        busy={ask.busy}
        error={ask.error}
        onCancel={ask.cancel}
        onConfirm={() => void ask.confirm()}
      />
    </Card>
  );
}
