// The one cache hub in the controller (caches R5.73, R5.77, R5.78; owner decision D5, CRSP342 option 1): the hit
// rate and the space, the one budget every kind shares, one card per kind with its switch and Wipe, and the warmth
// of each repository. Shared parts only.
import {
  Button,
  Card,
  CardGrid,
  Empty,
  Facts,
  Message,
  Meter,
  NumberField,
  Section,
  Stack,
  StatGrid,
  StatTile,
  Status,
  SummaryLine,
  Switch,
  Table,
  Text,
  formatNumber,
  formatPast,
  formatSize,
  useNow,
} from '@crsp/ui';
import { unwrap, useClient, type Schemas } from '../../api/api';
import { useCommand, useLoad } from '../../api/hooks';
import { useSession } from '../../auth/session';
import { useVersion } from '../../live/LiveProvider';
import { Ask, Load } from '../common';
import { useAskFirst } from './askFirst';

type Hub = Schemas['CacheHub'];
type HubCard = Schemas['CacheHubCard'];
type HubSettings = Schemas['CacheHubSettings'];
type Kind = HubCard['kind'];

/** The card title and the setting each kind's switch turns on. */
export const KINDS: Record<
  Kind,
  {
    title: string;
    setting: 'actions_cache' | 'git_mirror' | 'registry_mirror' | 'package_proxy';
  }
> = {
  actions_cache: { title: 'Actions cache', setting: 'actions_cache' },
  git_mirror: { title: 'Repository mirrors', setting: 'git_mirror' },
  registry_mirror: { title: 'Registry mirror', setting: 'registry_mirror' },
  package_proxy: { title: 'Package proxy', setting: 'package_proxy' },
};

const GIB = 1024 ** 3;

/** The hit rate as a whole percent, or undefined before the first lookup. */
export function hitRate(hits: number, misses: number): string | undefined {
  const all = hits + misses;
  return all > 0 ? String(Math.round((hits / all) * 100)) : undefined;
}

export function CacheHub() {
  const client = useClient();
  const v = useVersion('caches');
  const { result, reload } = useLoad(() => unwrap(client.GET('/api/v1/caches/hub')), [v]);
  return (
    <Load result={result} what="the cache hub" reload={reload}>
      {(hub) => <HubView hub={hub} reload={reload} />}
    </Load>
  );
}

function HubView({ hub, reload }: { hub: Hub; reload: () => void }) {
  const client = useClient();
  const session = useSession();
  const admin = session.can('admin');
  const why = session.whyNot('admin');
  const save = useCommand(async (next: Partial<HubSettings>) => {
    await unwrap(
      client.PUT('/api/v1/caches/settings', { body: { ...hub.settings, ...next } }),
    );
    reload();
  });
  const on = hub.cards.filter((c) => c.on).length;
  const used = hub.used_bytes;
  const budget = hub.budget_bytes;
  const rate = hitRate(hub.hits, hub.misses);
  const budgetText = `${formatNumber(hub.settings.budget_gib)} GiB`;
  return (
    <Stack>
      <Section
        title="Shared cache"
        hint="One shared cache on the controller, used by every runner of the pool."
      >
        <Stack>
          <SummaryLine
            total={[{ count: hub.cards.length, word: 'cache', plural: 'caches' }]}
            states={[
              { count: on, word: 'on' },
              { count: hub.cards.length - on, word: 'off' },
            ]}
          />
          <StatGrid>
            <StatTile
              label="Hit rate"
              value={rate}
              unit={rate === undefined ? undefined : '%'}
            />
            <StatTile label="Served" value={formatSize(hub.served_bytes)} />
            <StatTile label="Used" value={formatSize(used)} unit={`of ${budgetText}`} />
            <StatTile label="Warm machines" value={formatNumber(hub.warm_machines)} />
          </StatGrid>
        </Stack>
      </Section>
      <Section
        title="One budget"
        hint="All caches share this space. When it is full, the least recently used entries go first."
      >
        <Stack>
          <Meter
            label="Cache budget"
            value={used}
            max={budget}
            text={`${formatSize(used)} of ${budgetText} used`}
          />
          {admin ? (
            <NumberField
              label="Budget"
              help="1 to 4096 GiB. A smaller budget frees space at once."
              value={hub.settings.budget_gib}
              unit=" GiB"
              min={1}
              max={4096}
              onChange={(n) => void save.run({ budget_gib: n })}
            />
          ) : (
            <Text tone="muted">
              {`Budget ${formatNumber(Math.round(budget / GIB))} GiB. ${why ?? ''}`.trim()}
            </Text>
          )}
          {save.error ? (
            <Message tone="problem">Could not save. {save.error}</Message>
          ) : null}
        </Stack>
      </Section>
      <CardGrid>
        {hub.cards.map((c) => (
          <HubCardView
            key={c.kind}
            card={c}
            busy={save.busy}
            onSwitch={(value) => void save.run({ [KINDS[c.kind].setting]: value })}
            onChanged={reload}
          />
        ))}
      </CardGrid>
      {hub.repos.length === 0 ? (
        <Section title="Warmth by repository" count={0}>
          <Empty title="No repository has used the cache yet." />
        </Section>
      ) : (
        <Table
          caption="Warmth by repository"
          title="Warmth by repository"
          rows={hub.repos}
          rowKey={(r) => r.repo}
          columns={[
            { id: 'repo', header: 'Repository', cell: (r) => r.repo, grow: 2 },
            { id: 'hits', header: 'Hits', cell: (r) => formatNumber(r.hits) },
            {
              id: 'miss',
              header: 'Misses',
              cell: (r) => formatNumber(r.misses),
              secondary: true,
            },
            {
              id: 'warm',
              header: 'Warmth',
              cell: (r) => (
                <Meter
                  label={`Warmth ${r.repo}`}
                  value={r.warmth}
                  cells={5}
                  warnAt={101}
                  badAt={101}
                />
              ),
            },
            {
              id: 'size',
              header: 'Size',
              cell: (r) => formatSize(r.size_bytes),
              secondary: true,
            },
          ]}
        />
      )}
    </Stack>
  );
}

function facts(c: HubCard, now: number): { name: string; value: string }[] {
  const last = c.last_used_at ? formatPast(c.last_used_at, now) : 'never';
  switch (c.kind) {
    case 'git_mirror':
      return [
        { name: 'Repositories', value: formatNumber(c.entry_count) },
        { name: 'Size', value: formatSize(c.size_bytes) },
        { name: 'Refreshed', value: last },
      ];
    case 'registry_mirror':
      return [{ name: 'Size', value: formatSize(c.size_bytes) }];
    case 'package_proxy':
      return [
        { name: 'Covers', value: 'npm, PyPI' },
        { name: 'Apt and crates', value: 'not yet' },
        { name: 'Entries', value: formatNumber(c.entry_count) },
        { name: 'Size', value: formatSize(c.size_bytes) },
        { name: 'Last hit', value: last },
      ];
    default:
      return [
        { name: 'Entries', value: formatNumber(c.entry_count) },
        { name: 'Size', value: formatSize(c.size_bytes) },
        { name: 'Last hit', value: last },
      ];
  }
}

function HubCardView({
  card,
  busy,
  onSwitch,
  onChanged,
}: {
  card: HubCard;
  busy: boolean;
  onSwitch: (on: boolean) => void;
  onChanged: () => void;
}) {
  const client = useClient();
  const session = useSession();
  const now = useNow();
  const admin = session.can('admin');
  const why = session.whyNot('admin');
  const name = KINDS[card.kind].title;
  const wipe = (confirm: boolean) =>
    unwrap(
      client.POST('/api/v1/caches/{cacheId}/wipe', {
        params: {
          path: { cacheId: card.wipe_id },
          query: confirm ? { confirm: true } : {},
        },
      }),
    );
  const { ask, probe } = useAskFirst({
    probe: () => wipe(false),
    commit: () => wipe(true),
    fallback: () => [`${name}, ${formatSize(card.size_bytes)}`],
    onDone: onChanged,
  });
  return (
    <Card
      title={name}
      footer={
        <Button
          onPress={() => void probe.run()}
          busy={probe.busy}
          busyLabel="Working"
          disabled={!admin}
          reason={why}
        >
          Wipe
        </Button>
      }
    >
      <Stack gap="tight">
        <Switch
          label={card.kind === 'package_proxy' ? 'Package proxy (npm, PyPI)' : name}
          on={card.on}
          onChange={onSwitch}
          busy={busy}
          disabledReason={admin ? undefined : why}
        />
        {card.on && card.status !== 'ok' ? (
          <>
            <Status
              kind={card.status === 'failed' ? 'failed' : 'waiting'}
              word={card.status === 'failed' ? 'failed' : 'waiting'}
            />
            {card.note ? <Text tone="muted">{card.note}</Text> : null}
          </>
        ) : null}
        <Facts items={facts(card, now)} />
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
