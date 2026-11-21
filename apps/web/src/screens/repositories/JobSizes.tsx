// Job sizes of one repository (scheduling R5.80 to R5.88): how big crsp thinks each job is, where the size came
// from, the two override values side by side (R5.87), and the cost table the guesses use (read-only).
// Design system CORE-1 to CORE-7, UX-25, UX-26, UX-27, UX-37, UX-72; one pane, shared parts only.
import {
  Segmented,
  Empty,
  Message,
  Pane,
  Stack,
  Table,
  Text,
  type Column,
} from '@crsp/ui';
import { useState } from 'react';
import { unwrap, useClient, type Schemas } from '../../api/api';
import { useCommand, useLoad } from '../../api/hooks';
import { useSession } from '../../auth/session';
import { Load } from '../common';

type Row = Schemas['JobSizeRow'];
type Cost = Schemas['JobSizeCostRow'];
type Size = Schemas['JobSizeClass'];

export const SIZE_WORDS: Record<Size, string> = {
  light: 'Light',
  normal: 'Normal',
  heavy: 'Heavy',
};
export const SOURCE_WORDS: Record<Row['source'], string> = {
  override: 'Override',
  measured: 'Measured',
  scan: 'Scan',
  default: 'Default',
};

const sizeWord = (c: string): string => (c ? (SIZE_WORDS[c as Size] ?? c) : 'None');

export function sourceWords(r: Row): string {
  const base = SOURCE_WORDS[r.source];
  return r.confidence === 'low' && r.source === 'scan' ? `${base}, low confidence` : base;
}

const costColumns: Column<Cost>[] = [
  {
    id: 'kind',
    header: 'Kind',
    cell: (c) => (c.kind === 'framework' ? 'Test framework' : 'Service'),
    sort: (c) => c.kind,
  },
  { id: 'key', header: 'Name', cell: (c) => c.key, sort: (c) => c.key, kind: 'grow' },
  { id: 'class', header: 'Size', cell: (c) => sizeWord(c.class), sort: (c) => c.class },
  { id: 'cpu', header: 'CPU units', cell: (c) => String(c.cpu), sort: (c) => c.cpu },
  { id: 'gib', header: 'Memory (GiB)', cell: (c) => String(c.gib), sort: (c) => c.gib },
  {
    id: 'assumed',
    header: 'Basis',
    cell: (c) => (c.assumption ? 'Assumption' : 'Measured'),
    sort: (c) => String(c.assumption),
  },
];

export function JobSizesPane({ fullName }: { fullName: string }) {
  const client = useClient();
  const session = useSession();
  const admin = session.can('admin');
  const { result, reload } = useLoad(
    () =>
      unwrap(
        client.GET('/api/v1/job-sizes', { params: { query: { repository: fullName } } }),
      ),
    [fullName],
  );
  const [copied, setCopied] = useState<string | undefined>(undefined);
  const [copyError, setCopyError] = useState(false);
  const save = useCommand(async (job: string, value: string) => {
    if (value === '')
      await unwrap(
        client.DELETE('/api/v1/job-sizes', {
          params: { query: { repository: fullName, pattern: job } },
        }),
      );
    else
      await unwrap(
        client.PUT('/api/v1/job-sizes', {
          body: { repository: fullName, pattern: job, class: value as Size },
        }),
      );
    reload();
  });
  const copy = (r: Row) => {
    setCopied(undefined);
    setCopyError(false);
    void navigator.clipboard
      ?.writeText(r.copy_line)
      .then(() => setCopied(r.job))
      .catch(() => setCopyError(true));
  };

  const columns: Column<Row>[] = [
    { id: 'job', header: 'Job', cell: (r) => r.job, sort: (r) => r.job, kind: 'grow' },
    {
      id: 'size',
      header: 'Size',
      cell: (r) => (
        <Stack gap="tight">
          <Text>{sizeWord(r.class)}</Text>
          <Text tone="muted">{`CPU ${r.weight.cpu}, memory ${r.weight.memory}`}</Text>
        </Stack>
      ),
      sort: (r) => r.class,
    },
    {
      id: 'source',
      header: 'Where the size came from',
      kind: 'grow',
      grow: 2,
      cell: (r) => (
        <Stack gap="tight">
          <Text>{sourceWords(r)}</Text>
          {r.reset_note ? <Text tone="muted">{r.reset_note}</Text> : null}
        </Stack>
      ),
      sort: (r) => r.source,
    },
    {
      id: 'file',
      header: 'From .crsp.yml',
      cell: (r) => sizeWord(r.override_file),
      secondary: true,
    },
    {
      id: 'here',
      header: 'Set here',
      cell: (r) => sizeWord(r.override_console),
      secondary: true,
    },
    // A viewer sees the two values and no field (admin only, R5.87).
    ...(admin
      ? [
          {
            id: 'change',
            header: 'Change the size here',
            kind: 'grow' as const,
            cell: (r: Row) => (
              <Segmented
                label={`Size of ${r.job}`}
                allLabel="No override"
                options={[
                  { value: 'light', label: 'Light' },
                  { value: 'normal', label: 'Normal' },
                  { value: 'heavy', label: 'Heavy' },
                ]}
                value={r.override_console as Size | ''}
                onChange={(v) => void save.run(r.job, v)}
              />
            ),
          },
        ]
      : []),
  ];

  return (
    <Load result={result} what="the job sizes" reload={reload}>
      {(l) => (
        <>
          <Pane title="Job sizes" count={l.items.length}>
            <Stack>
              {l.file_error ? (
                <Message tone="needs-you">
                  {`.crsp.yml could not be read: ${l.file_error}. Sizes from the file are left out until it can be read.`}
                </Message>
              ) : null}
              {!admin ? <Message tone="note">{session.whyNot('admin')}</Message> : null}
              {save.error ? (
                <Message tone="problem">{`Could not save the size. ${save.error}`}</Message>
              ) : null}
              {l.items.length === 0 ? (
                <Empty title="No jobs are known for this repository yet. They show here after crsp has read its workflows." />
              ) : (
                <Table
                  caption="Job sizes"
                  columns={columns}
                  rows={l.items}
                  rowKey={(r) => r.job}
                  action={{
                    label: 'Copy line',
                    name: (r) => `${r.job} to .crsp.yml`,
                    press: (r) => ({ onPress: () => copy(r) }),
                  }}
                />
              )}
              {copied ? (
                <Message tone="done">{`Copied the line for ${copied}. Paste it into .crsp.yml.`}</Message>
              ) : null}
              {copyError ? (
                <Message tone="problem">
                  Could not copy. Select the line and copy it yourself.
                </Message>
              ) : null}
            </Stack>
          </Pane>
          <Pane title="Cost table" count={l.cost_table.length}>
            <Stack>
              <Message tone="note">
                Every row is an assumption until measured sizes replace it.
              </Message>
              {l.cost_table.length === 0 ? (
                <Empty title="The cost table is empty." />
              ) : (
                <Table
                  caption="Cost table"
                  columns={costColumns}
                  rows={l.cost_table}
                  rowKey={(c) => `${c.kind}-${c.key}`}
                />
              )}
            </Stack>
          </Pane>
        </>
      )}
    </Load>
  );
}
