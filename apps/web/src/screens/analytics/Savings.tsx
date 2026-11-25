// Analytics > Savings (observability R5.15, R5.16, R5.18, R5.19): minutes and money the pool saved against
// GitHub-hosted runners, for today, 7 days, 30 days or a range, by repository, system and machine. An estimate:
// the page says so and shows where the rates came from and when. Built from shared parts only (UX-52).
import {
  DatePicker,
  Empty,
  Message,
  Section,
  Segmented,
  StatGrid,
  StatTile,
  Stack,
  Table,
  Text,
  formatDate,
  formatNumber,
  type Column,
} from '@crsp/ui';
import { useState } from 'react';
import { unwrap, useClient, type Schemas } from '../../api/api';
import { useLoad } from '../../api/hooks';
import { Load } from '../common';
import { SAVINGS_WORDS as W, money } from '../savings/words';

type Row = Schemas['SavingsRow'];
type RangeName = Schemas['SavingsReport']['range'];

const today = () => formatDate(new Date());

function columns(first: string): Column<Row>[] {
  return [
    { id: 'key', header: first, cell: (r) => r.key, sort: (r) => r.key, kind: 'grow' },
    {
      id: 'jobs',
      header: W.jobs,
      cell: (r) => formatNumber(r.jobs),
      sort: (r) => r.jobs,
    },
    {
      id: 'minutes',
      header: W.minutes,
      cell: (r) => formatNumber(r.minutes),
      sort: (r) => r.minutes,
    },
    { id: 'money', header: W.money, cell: (r) => money(r.money), sort: (r) => r.money },
  ];
}

function Split({ title, first, rows }: { title: string; first: string; rows: Row[] }) {
  return (
    <Section title={title} count={rows.length}>
      <Table
        caption={title}
        columns={columns(first)}
        rows={rows}
        rowKey={(r) => r.key}
        rowLabel={(r) => r.key}
      />
    </Section>
  );
}

export function Savings() {
  const client = useClient();
  const [range, setRange] = useState<RangeName>('today');
  const [from, setFrom] = useState<string>(today());
  const [to, setTo] = useState<string>(today());
  const custom = range === 'custom';
  const ready = !custom || from <= to;
  const { result, reload } = useLoad(
    () =>
      unwrap(
        client.GET('/api/v1/savings', {
          params: { query: { range, ...(custom ? { from, to } : {}) } },
        }),
      ),
    [client, range, custom ? from : '', custom ? to : ''],
  );
  return (
    <Stack>
      <Segmented
        label={W.rangeLabel}
        options={W.ranges}
        value={range}
        allLabel={null}
        onChange={(v) => setRange(v === '' ? 'today' : v)}
      />
      {custom ? (
        <Section title={W.customHint}>
          <DatePicker label={W.from} value={from} max={today()} onChange={setFrom} />
          <DatePicker label={W.to} value={to} max={today()} onChange={setTo} />
          {ready ? null : (
            <Message tone="problem">The last day is before the first day.</Message>
          )}
        </Section>
      ) : null}
      <Load result={result} what="the savings report" reload={reload}>
        {(r) => (
          <Stack>
            <StatGrid>
              <StatTile
                label="Minutes saved"
                value={formatNumber(r.total.minutes)}
                unit={W.minutesUnit}
              />
              <StatTile
                label="Money saved"
                value={money(r.total.money)}
                unit={W.estimate}
              />
              <StatTile label="Jobs" value={formatNumber(r.total.jobs)} />
            </StatGrid>
            <Text tone="muted">{r.note}</Text>
            {r.total.public_minutes > 0 ? (
              <Message tone="note">
                {`${formatNumber(r.total.public_minutes)} of these minutes are from public repositories. ${W.publicNote}`}
              </Message>
            ) : null}
            {r.not_priced > 0 ? (
              <Message tone="note">{W.notPriced(r.not_priced)}</Message>
            ) : null}
            {r.total.jobs === 0 ? (
              <Empty title={W.empty} />
            ) : (
              <>
                <Split
                  title={W.byRepository}
                  first={W.repository}
                  rows={r.by_repository}
                />
                <Split title={W.byOs} first={W.system} rows={r.by_os} />
                <Split title={W.byMachine} first={W.machine} rows={r.by_machine} />
              </>
            )}
            <Text tone="muted">
              {W.source(r.source_url, r.copied_on, r.rates_changed)}
            </Text>
          </Stack>
        )}
      </Load>
    </Stack>
  );
}
