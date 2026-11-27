// Settings sub-pages Audit log and Config export (web-console 4.9, R5.29, UX-39). The audit log lists who changed
// what, newest first, and exports every row that matches the filters. Config export downloads the configuration
// without any secret; a secret is named only as set or not set.
import {
  Button,
  DateTime,
  Empty,
  ExportButton,
  Section,
  ListToolbar,
  SearchBar,
  Stack,
  Status,
  Table,
  Text,
  type Column,
  formatDateTime,
} from '@crsp/ui';
import { useEffect, useState } from 'react';
import { unwrap, useClient, type Schemas } from '../../api/api';
import { saveExport } from '../../api/download';
import { useCommand } from '../../api/hooks';
import { useSession } from '../../auth/session';
import { Load } from '../common';
import type { Loaded } from '../../api/hooks';

type Event = Schemas['AuditEvent'];

const PAGE = 100;

/** The thing a change acted on in plain words: "repository luis-mc/app", or the type alone when it has no id. */
function targetOf(e: Event): string {
  return [e.target_type, e.target_id].filter(Boolean).join(' ');
}

const cols: Column<Event>[] = [
  {
    id: 'at',
    header: 'When',
    cell: (e) => <DateTime value={e.at} />,
    title: (e) => formatDateTime(e.at),
    sort: (e) => e.at,
  },
  { id: 'actor', header: 'Who', cell: (e) => e.actor, sort: (e) => e.actor },
  { id: 'action', header: 'What', cell: (e) => e.action, sort: (e) => e.action },
  // What the change acted on, so two rows of one action in a minute can be told apart (LOOK49).
  {
    id: 'target',
    header: 'On',
    cell: (e) => targetOf(e),
    sort: (e) => targetOf(e),
    grow: 1,
  },
  { id: 'summary', header: 'Details', cell: (e) => e.summary, grow: 2 },
  {
    id: 'result',
    header: 'Result',
    cell: (e) => <Status kind={e.result === 'done' ? 'ok' : 'waiting'} word={e.result} />,
  },
];

export function AuditLogSettings() {
  const client = useClient();
  const [actor, setActor] = useState('');
  const [action, setAction] = useState('');
  const [rows, setRows] = useState<Event[]>([]);
  const [next, setNext] = useState<string | undefined>(undefined);
  const [result, setResult] = useState<Loaded<Event[]>>({ state: 'loading' });
  const [tick, setTick] = useState(0);

  const query = {
    ...(actor.trim() ? { actor: actor.trim() } : {}),
    ...(action.trim() ? { action: action.trim() } : {}),
  };

  useEffect(() => {
    let live = true;
    unwrap(client.GET('/api/v1/audit', { params: { query: { ...query, limit: PAGE } } }))
      .then((page) => {
        if (!live) return;
        setRows(page.items);
        setNext(page.next_cursor);
        setResult({ state: 'ready', data: page.items, loadedAt: Date.now() });
      })
      .catch((e: unknown) => {
        if (!live) return;
        setResult({
          state: 'failed',
          error: e instanceof Error ? e : new Error('Failed'),
        });
      });
    return () => {
      live = false;
    };
    // The filters are the only inputs.
  }, [client, actor, action, tick]);

  const more = useCommand(async () => {
    const page = await unwrap(
      client.GET('/api/v1/audit', {
        params: { query: { ...query, limit: PAGE, ...(next ? { cursor: next } : {}) } },
      }),
    );
    setRows((r) => [...r, ...page.items]);
    setNext(page.next_cursor);
  });

  const exportAll = () =>
    saveExport(
      client.GET('/api/v1/exports/audit', {
        params: { query: { ...query, format: 'csv' as const } },
        parseAs: 'blob',
      }),
      'crsp-audit.csv',
    );

  return (
    <Section title="Audit log">
      <Stack>
        <Text>Who changed what, and when. The newest change is first.</Text>
        <ListToolbar
          label="Audit log"
          search={
            <>
              <SearchBar label="Search who" value={actor} onChange={setActor} />
              <SearchBar label="Search what" value={action} onChange={setAction} />
            </>
          }
          actions={<ExportButton primary what="the audit log" onExport={exportAll} />}
        />
        <Load
          result={result.state === 'ready' ? { ...result, data: rows } : result}
          what="the audit log"
          reload={() => setTick((t) => t + 1)}
        >
          {(items) =>
            items.length === 0 ? (
              <Empty
                title={
                  actor || action
                    ? 'No change matches the search. Clear it to see every change.'
                    : 'Nothing has been changed yet.'
                }
              />
            ) : (
              <Stack>
                <Table
                  caption="Audit log"
                  columns={cols}
                  rows={items}
                  rowKey={(e) => String(e.id)}
                  oneLine
                />
                {next ? (
                  <Button
                    onPress={() => void more.run()}
                    busy={more.busy}
                    busyLabel="Loading more"
                  >
                    Load more
                  </Button>
                ) : null}
              </Stack>
            )
          }
        </Load>
      </Stack>
    </Section>
  );
}

export function ConfigExportSettings() {
  const client = useClient();
  const session = useSession();
  const exportConfig = () =>
    saveExport(
      client.GET('/api/v1/exports/config', { parseAs: 'blob' }),
      'crsp-config.yaml',
    );
  return (
    <Section title="Config export">
      <Stack>
        <Text>
          The configuration as a file. It holds no secret: each secret is named and marked
          set or not set.
        </Text>
        <ExportButton
          label="Export config"
          what="the configuration"
          onExport={exportConfig}
          disabled={!session.can('admin')}
          reason={session.whyNot('admin')}
        />
      </Stack>
    </Section>
  );
}
