// Settings > Access > Webhooks (github-integration R5.51, OPS25). The owner turns real GitHub webhooks on from the
// console: a public https address that forwards to the controller. The controller makes the secret, sends a signed
// test to the address and sets the App's webhook at GitHub; nothing is done in a terminal or in GitHub's settings,
// except one guided step when the App does not subscribe to workflow_job. Only shared parts of @crsp/ui are used
// (UX-22, UX-24, UX-38, UX-71). The section is a part of the Access tab's one form: the bar at the foot counts its changes and saves it.
import {
  CheckRow,
  ErrorSummary,
  ExternalButton,
  Facts,
  Message,
  Section,
  Stack,
  Status,
  Text,
  TextField,
  formatPast,
  useNow,
} from '@crsp/ui';
import { useState } from 'react';
import { unwrap, useClient, type Schemas } from '../../api/api';
import { useCommand, useLoad } from '../../api/hooks';
import { safeAppUrl } from '../../auth/CallbackGuide';
import { useSession } from '../../auth/session';
import { Load } from '../common';
import { useTabPart } from './TabForm';

type Webhook = Schemas['AppWebhook'];

const LOCAL_SUFFIXES = [
  '.local',
  '.lan',
  '.internal',
  '.home.arpa',
  '.localhost',
  '.intranet',
  '.corp',
];

/** What is wrong with a typed address, in words; undefined when it can be tried. The controller checks it again. */
export function webhookAddressProblem(text: string): string | undefined {
  const v = text.trim();
  if (!v) return 'Write the public address that forwards to this controller.';
  let u: URL;
  try {
    u = new URL(v);
  } catch {
    return 'This is not a web address. Write it like https://hooks.example.com.';
  }
  if (u.protocol !== 'https:')
    return 'The address must start with https://. GitHub does not send webhooks to plain http.';
  if (u.username || u.search || u.hash)
    return 'Write the address without a user name, a query or a #fragment.';
  const host = u.hostname.toLowerCase();
  const ip4 = /^\d{1,3}(\.\d{1,3}){3}$/.test(host);
  const local =
    host === 'localhost' ||
    host.startsWith('[') ||
    !host.includes('.') ||
    LOCAL_SUFFIXES.some((s) => host.endsWith(s)) ||
    (ip4 &&
      /^(10|127|0)\.|^192\.168\.|^169\.254\.|^172\.(1[6-9]|2\d|3[01])\./.test(host));
  if (local)
    return `${host} is a local address. GitHub can only reach a public address, such as a tunnel or a reverse proxy with a real certificate.`;
  return undefined;
}

function stateStatus(w: Webhook): { kind: 'ok' | 'off' | 'failed'; word: string } {
  if (w.state === 'on') return { kind: 'ok', word: 'on' };
  if (w.state === 'failing') return { kind: 'failed', word: 'failing' };
  return { kind: 'off', word: 'off' };
}

/** Settings > Access > Webhooks. */
export function WebhooksSettings() {
  const client = useClient();
  const { result, reload } = useLoad(() => unwrap(client.GET('/api/v1/app/webhook')), []);
  // The done line outlives the reload that follows a save, so the state and the line agree.
  const [done, setDone] = useState<string | undefined>(undefined);
  return (
    <Section title="Webhooks">
      <Load result={result} what="the webhook state" reload={reload}>
        {(w) => (
          <WebhooksForm
            key={JSON.stringify(w)}
            webhook={w}
            done={done}
            setDone={setDone}
            onSaved={reload}
          />
        )}
      </Load>
    </Section>
  );
}

function LastDelivery({ w }: { w: Webhook }) {
  const now = useNow();
  if (!w.last_delivery_at) return <span>none yet</span>;
  return (
    <span>
      {formatPast(w.last_delivery_at, now)}
      {w.last_result ? `, ${w.last_result === 'ok' ? 'delivered' : w.last_result}` : ''}
    </span>
  );
}

function WebhooksForm({
  webhook,
  done,
  setDone,
  onSaved,
}: {
  webhook: Webhook;
  done: string | undefined;
  setDone: (text: string | undefined) => void;
  onSaved: () => void;
}) {
  const client = useClient();
  const session = useSession();
  const wasOn = webhook.state !== 'off';
  const [on, setOn] = useState(wasOn);
  const [address, setAddress] = useState(webhook.address ?? '');
  const [errors, setErrors] = useState<string[]>([]);
  const save = useCommand(async () => {
    setDone(undefined);
    if (on) {
      const problem = webhookAddressProblem(address);
      setErrors(problem ? [problem] : []);
      if (problem) return false;
      await unwrap(
        client.PUT('/api/v1/app/webhook', { body: { address: address.trim() } }),
      );
      setDone('Webhooks are on. GitHub now tells crsp about jobs at once.');
      onSaved();
      return true;
    }
    setErrors([]);
    if (!wasOn) return true;
    await unwrap(client.DELETE('/api/v1/app/webhook'));
    setDone('Webhooks are off. crsp finds jobs by polling GitHub again.');
    onSaved();
    return true;
  });
  const st = stateStatus(webhook);
  const link = safeAppUrl(webhook.events_link);
  const owner = session.can('owner');
  const addressChanged = on && address.trim() !== (webhook.address ?? '');
  // The section is a part of the Access tab: its changes count in the bar, and Save in the bar saves it.
  useTabPart('webhooks', {
    changes: (on !== wasOn ? 1 : 0) + (addressChanged ? 1 : 0),
    save: async () => (await save.run()) === true,
    reset: () => {
      setOn(wasOn);
      setAddress(webhook.address ?? '');
      setErrors([]);
      setDone(undefined);
      save.clear();
    },
    blocked: owner ? undefined : (session.whyNot('owner') ?? 'Only the owner.'),
  });
  return (
    <>
      <Stack>
        <Text>
          With webhooks GitHub tells crsp about a job the moment it is queued, so the job
          starts at once. Without them crsp asks GitHub for new jobs every 15 seconds, so
          a job can start up to 15 seconds late. Webhooks need an address GitHub can
          reach: a tunnel or a reverse proxy with a real certificate. A LAN address or a
          self-signed certificate does not work.
        </Text>
        <Facts
          items={[
            { name: 'Webhooks', value: <Status kind={st.kind} word={st.word} /> },
            ...(webhook.state !== 'off' && webhook.address
              ? [{ name: 'Address', value: webhook.address }]
              : []),
            ...(webhook.state !== 'off'
              ? [{ name: 'Last delivery', value: <LastDelivery w={webhook} /> }]
              : []),
          ]}
        />
        <CheckRow
          label="Real webhooks"
          help="Clear it and Save to find jobs by polling GitHub again."
          checked={on}
          changed={on !== wasOn}
          onChange={(v) => {
            setOn(v);
            setDone(undefined);
            setErrors([]);
          }}
          {...(!owner
            ? { disabledReason: session.whyNot('owner') ?? 'Only the owner.' }
            : {})}
        />
        {on ? (
          <TextField
            label="Public https address"
            help="The address that forwards to this controller, for example https://hooks.example.com. crsp sends a signed test to it before GitHub is changed."
            value={address}
            changed={addressChanged}
            onChange={(v) => {
              setAddress(v);
              setDone(undefined);
            }}
            width="full"
            readOnly={!owner}
          />
        ) : null}
        <ErrorSummary errors={errors} />
        {done ? <Message tone="done">{done}</Message> : null}
        {save.error ? (
          <Message tone="problem">Could not save the webhook. {save.error}</Message>
        ) : null}
        {link ? (
          <Message tone="needs-you">
            The App does not listen for workflow jobs yet. On GitHub, open the App's
            permissions page and under Subscribe to events tick Workflow job, then save.
            Jobs are learned by webhooks once you have done that.
          </Message>
        ) : null}
        {link ? (
          <ExternalButton href={link}>Open the App on GitHub</ExternalButton>
        ) : null}
      </Stack>
    </>
  );
}
