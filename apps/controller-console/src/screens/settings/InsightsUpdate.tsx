// Settings, Data: how often CI run insights look at the CI again (ci-run-insights R5.44, owner decisions D18, D19).
// The cost warning on Do next recommends a slower period; the owner chooses it here. Shared parts only (design
// system v3 UX-22, UX-71): the tab's one Save saves it.
import {
  ChoiceField,
  ErrorSummary,
  FieldGrid,
  Message,
  NumberField,
  Section,
  Stack,
} from '@crsp/ui';
import { useState } from 'react';
import { unwrap, useClient, type Schemas } from '../../api/api';
import { useCommand, useLoad } from '../../api/hooks';
import { useSession } from '../../auth/session';
import { Load } from '../common';
import { useTabPart } from './TabForm';

type Period = NonNullable<Schemas['Settings']['insights_update_period']>;

const CHOICES = [
  { value: 'every_run', label: 'After every CI run' },
  { value: 'every_nth_run', label: 'After every few CI runs' },
  { value: 'hourly', label: 'Once an hour' },
] as const;

export function InsightsUpdateSettings() {
  const client = useClient();
  const session = useSession();
  const { result, reload } = useLoad(() => unwrap(client.GET('/api/v1/settings')), []);
  return (
    <Section title="CI run insights">
      <Load result={result} what="CI run insights" reload={reload}>
        {(s) => (
          <InsightsUpdateForm
            s={s}
            onSaved={reload}
            canEdit={session.can('owner')}
            why={session.whyNot('owner')}
          />
        )}
      </Load>
    </Section>
  );
}

function InsightsUpdateForm({
  s,
  onSaved,
  canEdit,
  why,
}: {
  s: Schemas['Settings'];
  onSaved: () => void;
  canEdit: boolean;
  why: string | undefined;
}) {
  const client = useClient();
  const was = {
    period: s.insights_update_period ?? 'every_run',
    n: s.insights_update_every_n_runs ?? 5,
  };
  const [period, setPeriod] = useState<Period>(was.period);
  const [n, setN] = useState(was.n);
  const [errors, setErrors] = useState<string[]>([]);
  const [saved, setSaved] = useState(false);
  const changes = [period !== was.period, n !== was.n].filter(Boolean).length;
  const save = useCommand(async () => {
    setSaved(false);
    if (period === 'every_nth_run' && (n < 2 || n > 50)) {
      setErrors(['Runs between updates must be from 2 to 50.']);
      return false;
    }
    setErrors([]);
    await unwrap(
      client.PATCH('/api/v1/settings', {
        body: { insights_update_period: period, insights_update_every_n_runs: n },
      }),
    );
    setSaved(true);
    onSaved();
    return true;
  });
  useTabPart('insights-update', {
    changes,
    save: async () => (await save.run()) === true,
    reset: () => {
      setPeriod(was.period);
      setN(was.n);
      setErrors([]);
      save.clear();
    },
    blocked: canEdit ? undefined : (why ?? 'Only the owner can change this.'),
  });
  return (
    <Stack>
      <ErrorSummary errors={errors} />
      <FieldGrid>
        <ChoiceField
          label="Update period"
          help="How often crsp looks at your CI again. A slower period uses less CPU, memory and disk."
          value={period}
          changed={period !== was.period}
          onChange={(v) => setPeriod(v as Period)}
          choices={CHOICES}
        />
        {period === 'every_nth_run' ? (
          <NumberField
            label="Runs between updates"
            value={n}
            changed={n !== was.n}
            unit=" runs"
            min={2}
            max={50}
            onChange={setN}
          />
        ) : null}
      </FieldGrid>
      {saved ? <Message tone="done">CI run insights saved.</Message> : null}
      {save.error ? <Message tone="problem">Could not save. {save.error}</Message> : null}
    </Stack>
  );
}
