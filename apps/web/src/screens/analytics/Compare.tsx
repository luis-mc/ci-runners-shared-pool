// Analytics > Compare (observability R5.34): the same jobs on crsp and on GitHub-hosted runners, from the finished
// workflow-run jobs GitHub holds. It reads only and starts nothing. A number appears only when the data has it.
import {
  ActionBar,
  Button,
  Empty,
  Message,
  NumberField,
  Section,
  Stack,
  Table,
  Text,
  TextField,
  formatDuration,
  formatNumber,
  type Column,
} from '@crsp/ui';
import { useState } from 'react';
import { unwrap, useClient, type Schemas } from '../../api/api';
import { useCommand } from '../../api/hooks';
import { SAVINGS_WORDS as W, money } from '../savings/words';

type Row = Schemas['SpeedRow'];

const secs = (n: number | undefined, count: number): string =>
  count === 0 || n === undefined ? 'not known' : formatDuration(n);

const columns: Column<Row>[] = [
  {
    id: 'job',
    header: W.compareJob,
    cell: (r) => r.job,
    sort: (r) => r.job,
    kind: 'grow',
  },
  {
    id: 'crsp',
    header: W.compareCrsp,
    cell: (r) => secs(r.crsp.run.median_seconds, r.crsp.run.count),
  },
  {
    id: 'hosted',
    header: W.compareHosted,
    cell: (r) => secs(r.hosted.run.median_seconds, r.hosted.run.count),
  },
  {
    id: 'saved',
    header: W.compareSaved,
    cell: (r) => formatNumber(r.minutes_saved, 1),
    sort: (r) => r.minutes_saved,
  },
  {
    id: 'cost',
    header: W.compareCost,
    cell: (r) => money(r.cost_avoided_usd),
    sort: (r) => r.cost_avoided_usd,
  },
];

export function Compare() {
  const client = useClient();
  const [repo, setRepo] = useState('');
  const [days, setDays] = useState(14);
  const [out, setOut] = useState<Schemas['SpeedComparison'] | undefined>();
  const run = useCommand(async () => {
    setOut(
      await unwrap(
        client.GET('/api/v1/compare', {
          params: { query: { repository: repo.trim(), days } },
        }),
      ),
    );
    return true;
  });
  const valid = /^[^/\s]+\/[^/\s]+$/.test(repo.trim());
  return (
    <Stack>
      <Section title={W.compareTitle}>
        <TextField
          label={W.compareRepository}
          value={repo}
          onChange={setRepo}
          help={W.compareHelp}
        />
        <NumberField
          label={W.compareDays}
          value={days}
          min={1}
          max={90}
          onChange={setDays}
        />
        {run.error ? (
          <Message tone="problem">Could not compare. {run.error}</Message>
        ) : null}
      </Section>
      {out ? (
        out.excluded ? (
          <Message tone="note">{out.reason ?? W.compareExcluded}</Message>
        ) : out.rows.length === 0 ? (
          <Empty title={W.compareEmpty} />
        ) : (
          <Section title={out.repository} count={out.rows.length}>
            <Table
              caption={W.compareTitle}
              columns={columns}
              rows={out.rows}
              rowKey={(r) => r.job}
              rowLabel={(r) => r.job}
            />
            <Text tone="muted">
              {W.compareTotal(out.total_minutes_saved, out.total_cost_avoided_usd)}
            </Text>
          </Section>
        )
      ) : null}
      <ActionBar
        primary={
          <Button
            kind="primary"
            busy={run.busy}
            busyLabel={W.compareRunning}
            disabled={!valid}
            reason={valid ? undefined : 'Write the repository as owner/repo.'}
            onPress={() => void run.run()}
          >
            {W.compareRun}
          </Button>
        }
      />
    </Stack>
  );
}
