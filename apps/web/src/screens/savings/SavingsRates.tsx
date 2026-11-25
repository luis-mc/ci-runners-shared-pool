// Settings > Repositories > Savings rates (observability R5.16, R5.17): the per-minute price of each GitHub-hosted
// runner kind, the source address and date they were copied, and the size each machine is priced at. It is a section
// of the tab's one form (UX-24, UX-71): the tab's Save writes the rates; "Reset to shipped rates" restores the table.
import {
  Button,
  ErrorSummary,
  FieldGrid,
  Message,
  NumberField,
  Section,
  SelectField,
  Stack,
  Text,
} from '@crsp/ui';
import { useState } from 'react';
import { unwrap, useClient, type Schemas } from '../../api/api';
import { useCommand, useLoad } from '../../api/hooks';
import { useSession } from '../../auth/session';
import { useMachines } from '../../api/machines';
import { Load } from '../common';
import { useTabPart } from '../settings/TabForm';
import { SAVINGS_WORDS as W } from './words';

type Table = Schemas['SavingsRates'];

export function SavingsRatesSettings() {
  const client = useClient();
  const { result, reload } = useLoad(
    () => unwrap(client.GET('/api/v1/savings/rates')),
    [client],
  );
  return (
    <Section title={W.ratesTitle} hint={W.ratesLead}>
      <Load result={result} what="the savings rates" reload={reload}>
        {(t) => <RatesForm t={t} onSaved={reload} />}
      </Load>
    </Section>
  );
}

function RatesForm({ t, onSaved }: { t: Table; onSaved: () => void }) {
  const client = useClient();
  const session = useSession();
  const machines = useMachines();
  const canEdit = session.can('owner');
  const [rates, setRates] = useState(t.rates);
  const [sizes, setSizes] = useState<Record<string, string>>(t.machine_sizes);
  const [errors, setErrors] = useState<string[]>([]);
  const [note, setNote] = useState<string | undefined>();
  const sizeNames = [...new Set(t.rates.map((r) => r.size))];
  const changes =
    rates.filter((r, i) => r.per_minute !== t.rates[i]?.per_minute).length +
    Object.keys({ ...sizes, ...t.machine_sizes }).filter(
      (m) => (sizes[m] ?? 'standard') !== (t.machine_sizes[m] ?? 'standard'),
    ).length;
  const save = useCommand(async () => {
    const bad = rates.filter((r) => !(r.per_minute >= 0 && r.per_minute <= 10));
    setErrors(bad.map((r) => `${W.rate(r.os, r.size)} must be between 0 and 10.`));
    setNote(undefined);
    if (bad.length) return false;
    const kept: Record<string, string> = {};
    for (const [m, s] of Object.entries(sizes)) if (s !== 'standard') kept[m] = s;
    await unwrap(
      client.PUT('/api/v1/savings/rates', { body: { ...t, rates, machine_sizes: kept } }),
    );
    setNote(W.saved);
    onSaved();
    return true;
  });
  const reset = useCommand(async () => {
    await unwrap(client.DELETE('/api/v1/savings/rates'));
    setNote(W.resetDone);
    setRates(t.rates);
    onSaved();
    return true;
  });
  useTabPart('savings-rates', {
    changes,
    save: async () => (await save.run()) === true,
    reset: () => {
      setRates(t.rates);
      setSizes(t.machine_sizes);
      setErrors([]);
      save.clear();
    },
    blocked: canEdit ? undefined : (session.whyNot('owner') ?? 'Only the owner can change this.'),
  });
  const list = machines.result.state === 'ready' ? machines.result.data.items : [];
  return (
    <Stack>
      <Text tone="muted">{W.source(t.source_url, t.copied_on, t.changed)}</Text>
      <ErrorSummary errors={errors} />
      <FieldGrid>
        {rates.map((r, i) => (
          <NumberField
            key={`${r.os}/${r.size}`}
            label={W.rate(r.os, r.size)}
            value={r.per_minute}
            decimals={4}
            min={0}
            max={10}
            changed={r.per_minute !== t.rates[i]?.per_minute}
            onChange={(v) => setRates(rates.map((x, j) => (j === i ? { ...x, per_minute: v } : x)))}
          />
        ))}
      </FieldGrid>
      {list.length > 0 ? (
        <FieldGrid>
          {list.map((m) => (
            <SelectField
              key={m.id}
              label={W.machineSize(m.name)}
              value={sizes[m.name] ?? 'standard'}
              choices={sizeNames.map((s) => ({ value: s, label: s }))}
              onChange={(v) => setSizes({ ...sizes, [m.name]: v })}
            />
          ))}
        </FieldGrid>
      ) : null}
      {canEdit ? (
        <Button
          busy={reset.busy}
          busyLabel={W.resetting}
          disabled={!t.changed}
          reason={t.changed ? undefined : 'The rates are the shipped rates already.'}
          onPress={() => void reset.run()}
        >
          {W.reset}
        </Button>
      ) : null}
      {note ? <Message tone="done">{note}</Message> : null}
      {save.error ? <Message tone="problem">Could not save. {save.error}</Message> : null}
      {reset.error ? <Message tone="problem">Could not reset. {reset.error}</Message> : null}
    </Stack>
  );
}
