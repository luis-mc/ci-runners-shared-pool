// Downloads (dependency-setup DEP15, D-DEP-8): what this controller keeps for machines that cannot reach the
// internet. The controller downloads each file, checks it against the checksum of this release and hands it to a
// machine; a machine asks the controller first. When the controller has no internet either, the person imports
// the file here. Every state is a word with a glyph (UX-58); a button that starts work locks and says so (UX-13).
import {
  Button,
  CardGrid,
  Card,
  Empty,
  FileField,
  Facts,
  Message,
  Section,
  Stack,
  Status,
  Text,
  formatPast,
  formatSize,
  useNow,
} from '@crsp/ui';
import { useState } from 'react';
import { unwrap, useClient, type Schemas } from '../../api/api';
import { useCommand, useLoad } from '../../api/hooks';
import { useSession } from '../../auth/session';
import { Load } from '../common';

type Entry = Schemas['DependencyStoreEntry'];

const IMPORT_MAX = 2 * 1024 * 1024 * 1024;

const GROUPS = [
  { kind: 'installer', title: 'Installers' },
  { kind: 'runner', title: 'Runner packages' },
  { kind: 'agent', title: 'Agent packages' },
] as const;

/** The state in words a person reads: the word is always there, the glyph and the colour only help (UX-58). */
export function stateOf(e: Entry): {
  kind: 'ok' | 'waiting' | 'failed' | 'info';
  word: string;
} {
  switch (e.state) {
    case 'ready':
      return {
        kind: 'ok',
        word:
          e.origin === 'imported' ? 'Ready, imported by you' : 'Ready on the controller',
      };
    case 'failed':
      return { kind: 'failed', word: 'Not kept' };
    case 'not_pinned':
      return { kind: 'info', word: 'Cannot be checked in this release' };
    default:
      return { kind: 'waiting', word: 'Not downloaded yet' };
  }
}

/** Why Download now is not pressable, or nothing when it is. */
export function fetchBlock(e: Entry): string | undefined {
  if (!e.pinned) return 'This release has no checksum for it.';
  if (!e.can_fetch) return 'This release has no download address for it.';
  return undefined;
}

export function DownloadsTab() {
  const client = useClient();
  const session = useSession();
  const { result, reload } = useLoad(
    () => unwrap(client.GET('/api/v1/dependencies/store')),
    [],
  );
  const admin = session.can('admin');
  return (
    <Load result={result} what="downloads" reload={reload}>
      {(data) =>
        data.items.length === 0 ? (
          <Section title="Downloads" count={0}>
            <Empty title="Nothing to keep yet. This release lists no downloads." />
          </Section>
        ) : (
          <Stack>
            <Text tone="muted">
              A machine without internet installs from this controller first. The
              controller downloads each file, checks it against the checksum of this
              release, and keeps only a file that matches. If this controller has no
              internet either, import the file here.
            </Text>
            {GROUPS.map((g) => {
              const items = data.items.filter((e) => e.kind === g.kind);
              if (items.length === 0) return null;
              return (
                <Section key={g.kind} title={g.title} count={items.length}>
                  <CardGrid>
                    {items.map((e) => (
                      <DownloadCard
                        key={e.id}
                        entry={e}
                        admin={admin}
                        onChanged={reload}
                      />
                    ))}
                  </CardGrid>
                </Section>
              );
            })}
          </Stack>
        )
      }
    </Load>
  );
}

function DownloadCard({
  entry: e,
  admin,
  onChanged,
}: {
  entry: Entry;
  admin: boolean;
  onChanged: () => void;
}) {
  const client = useClient();
  const now = useNow();
  const [importing, setImporting] = useState(false);
  const [file, setFile] = useState<File | null>(null);
  const fetchNow = useCommand(async () => {
    await unwrap(
      client.POST('/api/v1/dependencies/store/{dependencyId}/fetch', {
        params: { path: { dependencyId: e.id } },
      }),
    );
    onChanged();
  });
  const importIt = useCommand(async () => {
    if (!file) return;
    await unwrap(
      client.PUT('/api/v1/dependencies/store/{dependencyId}/file', {
        params: { path: { dependencyId: e.id } },
        body: file as unknown as string,
        bodySerializer: (b: unknown) => b as BodyInit,
        headers: { 'Content-Type': 'application/octet-stream' },
      }),
    );
    setImporting(false);
    setFile(null);
    onChanged();
  });
  const st = stateOf(e);
  const block = fetchBlock(e);
  const facts = [
    { name: 'State', value: <Status kind={st.kind} word={st.word} /> },
    ...(e.version ? [{ name: 'Version', value: e.version }] : []),
    ...(e.source ? [{ name: 'From', value: e.source }] : []),
    ...(e.stored_bytes ? [{ name: 'Kept', value: formatSize(e.stored_bytes) }] : []),
    ...(e.fetched_at
      ? [{ name: 'Kept since', value: formatPast(e.fetched_at, now) }]
      : []),
  ];
  return (
    <Card
      title={e.name}
      footer={
        admin && e.pinned ? (
          <Stack>
            <Button
              busy={fetchNow.busy}
              busyLabel="Downloading"
              disabled={block !== undefined || importing}
              reason={block}
              onPress={() => void fetchNow.run()}
            >
              {e.state === 'ready' ? 'Download again' : 'Download now'}
            </Button>
            <Button
              expanded={importing}
              controls={`import-${e.id}`}
              disabled={fetchNow.busy}
              onPress={() => setImporting((v) => !v)}
            >
              Import a file
            </Button>
          </Stack>
        ) : undefined
      }
    >
      <Facts items={facts} />
      {e.state === 'failed' && e.error ? (
        <Message tone="problem">{e.error}</Message>
      ) : null}
      {e.state === 'not_pinned' ? (
        <Text tone="muted">
          crsp cannot check a file for it, so it keeps none. Use Do it myself in Get this
          machine ready.
        </Text>
      ) : null}
      {fetchNow.error ? <Message tone="problem">{fetchNow.error}</Message> : null}
      {importing ? (
        <div id={`import-${e.id}`}>
          <Stack>
            <FileField
              label="File you downloaded"
              help="crsp keeps it only when it matches the checksum of this release."
              onChange={setFile}
              validate={(f) =>
                f.size > IMPORT_MAX ? 'The file is over 2 GB.' : undefined
              }
            />
            <Button
              busy={importIt.busy}
              busyLabel="Importing"
              disabled={file === null}
              reason={file === null ? 'Choose the file first.' : undefined}
              onPress={() => void importIt.run()}
            >
              Import
            </Button>
            {importIt.error ? <Message tone="problem">{importIt.error}</Message> : null}
          </Stack>
        </div>
      ) : null}
    </Card>
  );
}
