// Settings > Console > Metrics (observability R5.40 to R5.47): send the pool's numbers to Prometheus (a page the
// tool reads) or to an OpenTelemetry collector (the controller pushes). Both are off until the owner turns them
// on; the page listens on this PC only unless the owner exposes it, and may ask for a key. One section of the
// tab form (UX-22, UX-24): the tab's Save applies it. Only shared parts of @crsp/ui are used (UX-9, UX-13, UX-22).
import {
  Button,
  CheckRow,
  Cluster,
  CodeBlock,
  ErrorSummary,
  FieldGrid,
  Message,
  NumberField,
  Section,
  Stack,
  Text,
  TextField,
} from '@crsp/ui';
import { useState } from 'react';
import { unwrap, useClient, type Schemas } from '../../api/api';
import { useCommand, useLoad } from '../../api/hooks';
import { useSession } from '../../auth/session';
import { Load } from '../common';
import { useTabPart } from './TabForm';

type Metrics = Schemas['MetricsSettings'];
type Set = Schemas['MetricsSettingsSet'];

const toSet = (m: Metrics): Set => ({
  enabled: m.enabled,
  port: m.port,
  expose: m.expose,
  needs_key: m.needs_key,
  push_enabled: m.push_enabled,
  push_endpoint: m.push_endpoint,
  push_seconds: m.push_seconds,
});

/** The mistakes of the form, each in one sentence, before anything is sent. */
export function metricsProblems(s: Set, keyMade: boolean): string[] {
  const out: string[] = [];
  if (!Number.isInteger(s.port) || s.port < 1024 || s.port > 65535)
    out.push('The port must be from 1024 to 65535.');
  if (!Number.isInteger(s.push_seconds) || s.push_seconds < 10 || s.push_seconds > 3600)
    out.push('The push interval must be from 10 to 3600 seconds.');
  if (s.push_enabled && !/^https?:\/\/[^/\s]+/.test(s.push_endpoint.trim()))
    out.push('The collector address must start with http:// or https://.');
  if (s.enabled && s.needs_key && !keyMade)
    out.push('Make a key first, or turn the key off.');
  return out;
}

export function MetricsSettings() {
  const client = useClient();
  const { result, reload } = useLoad(
    () => unwrap(client.GET('/api/v1/settings/metrics')),
    [],
  );
  return (
    <Section title="Metrics">
      <Load result={result} what="the metrics settings" reload={reload}>
        {(m) => <MetricsForm key={JSON.stringify(m)} saved={m} onSaved={reload} />}
      </Load>
    </Section>
  );
}

function MetricsForm({ saved, onSaved }: { saved: Metrics; onSaved: () => void }) {
  const client = useClient();
  const session = useSession();
  const [s, setS] = useState<Set>(toSet(saved));
  const [errors, setErrors] = useState<string[]>([]);
  const [key, setKey] = useState<string | undefined>(undefined);
  const base = toSet(saved);
  const changed = (k: keyof Set) => base[k] !== s[k];
  const changeCount = (Object.keys(base) as (keyof Set)[]).filter(changed).length;
  const keyMade = saved.key_made || key !== undefined;
  const save = useCommand(async () => {
    const problems = metricsProblems(s, keyMade);
    setErrors(problems);
    if (problems.length) return false;
    await unwrap(client.PUT('/api/v1/settings/metrics', { body: s }));
    onSaved();
    return true;
  });
  const makeKey = useCommand(async () => {
    const r = await unwrap(client.POST('/api/v1/settings/metrics/token'));
    setKey(r.key);
    return true;
  });
  useTabPart('metrics', {
    changes: changeCount,
    save: async () => (await save.run()) === true,
    reset: () => {
      setS(base);
      setErrors([]);
      save.clear();
    },
    blocked: session.can('admin') ? undefined : session.whyNot('admin'),
  });
  const reason = session.whyNot('admin');
  return (
    <Stack>
      <Text>
        Send the pool&apos;s numbers to the tools you already use: runners by state, time
        in the queue, job time, jobs sent to the hosted fallback, cache hits and how long
        each machine has been up. Nothing is sent until you turn it on.
      </Text>
      <CheckRow
        label="Prometheus page"
        help={`Serves /metrics for Prometheus to read.${saved.running && saved.address ? ` Now on ${saved.address}.` : ''}`}
        checked={s.enabled}
        changed={changed('enabled')}
        disabledReason={reason}
        onChange={(v) => setS({ ...s, enabled: v })}
      />
      {s.enabled ? (
        <Stack>
          <FieldGrid>
            <NumberField
              label="Port"
              value={s.port}
              changed={changed('port')}
              min={1024}
              max={65535}
              onChange={(v) => setS({ ...s, port: v })}
            />
          </FieldGrid>
          <CheckRow
            label="Let other computers read the page"
            help="Off: only this PC can read it. On: any computer that can reach this PC can."
            checked={s.expose}
            changed={changed('expose')}
            disabledReason={reason}
            onChange={(v) => setS({ ...s, expose: v })}
          />
          <CheckRow
            label="Ask for a key"
            help="The reader must send the key as a bearer token."
            checked={s.needs_key}
            changed={changed('needs_key')}
            disabledReason={reason}
            onChange={(v) => setS({ ...s, needs_key: v })}
          />
          {s.expose && !s.needs_key ? (
            <Message tone="note">
              Anyone who can reach this PC will see the pool&apos;s numbers. Ask for a key
              to keep them private.
            </Message>
          ) : null}
          <Cluster>
            <Button
              onPress={() => void makeKey.run()}
              busy={makeKey.busy}
              disabled={Boolean(reason)}
              reason={reason}
            >
              {saved.key_made ? 'Make a new key' : 'Make a key'}
            </Button>
          </Cluster>
          {key !== undefined ? (
            <Stack>
              <Message tone="note">
                Copy this key now. It is not shown again. An older key stops working.
              </Message>
              <CodeBlock text={key} label="The metrics key" />
            </Stack>
          ) : null}
          {makeKey.error ? (
            <Message tone="problem">Could not make a key. {makeKey.error}</Message>
          ) : null}
        </Stack>
      ) : null}
      <CheckRow
        label="Push to OpenTelemetry"
        help="Sends the same numbers to a collector over OTLP (HTTP)."
        checked={s.push_enabled}
        changed={changed('push_enabled')}
        disabledReason={reason}
        onChange={(v) => setS({ ...s, push_enabled: v })}
      />
      {s.push_enabled ? (
        <FieldGrid>
          <TextField
            label="Collector address"
            help="For example http://collector:4318"
            value={s.push_endpoint}
            mono
            onChange={(v) => setS({ ...s, push_endpoint: v })}
          />
          <NumberField
            label="Push every"
            value={s.push_seconds}
            changed={changed('push_seconds')}
            unit=" s"
            min={10}
            max={3600}
            onChange={(v) => setS({ ...s, push_seconds: v })}
          />
        </FieldGrid>
      ) : null}
      {saved.pushing && saved.last_push_at ? (
        <Message tone="done">The last push worked.</Message>
      ) : null}
      {saved.last_push_error ? (
        <Message tone="problem">The last push failed. {saved.last_push_error}</Message>
      ) : null}
      {saved.enabled ? (
        <Stack>
          <Text>Paste this into prometheus.yml:</Text>
          <CodeBlock text={saved.scrape_config} label="Prometheus scrape configuration" />
          {saved.needs_key ? (
            <Text>Replace YOUR-TOKEN with the key you made.</Text>
          ) : null}
        </Stack>
      ) : null}
      <ErrorSummary errors={errors} />
      {save.error ? <Message tone="problem">Could not save. {save.error}</Message> : null}
    </Stack>
  );
}
