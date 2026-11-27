// Webhook health of a repository (github-integration R5.50, RES14): the last delivery time and result, and when
// deliveries fail the page says crsp polls this repository now and offers the switch back.
import {
  Button,
  Message,
  Stack,
  Status,
  TextLink,
  formatPast,
  useNow,
  type StatusKind,
} from '@crsp/ui';
import { unwrap, useClient } from '../../api/api';
import { useCommand } from '../../api/hooks';
import { useSession } from '../../auth/session';
import type { Repository } from './plans';

/** The last delivery as a status word. */
export function deliveryStatus(r: Repository): { kind: StatusKind; word: string } {
  const w = r.webhook;
  if (w?.webhooks_off) return { kind: 'off', word: 'no webhook' };
  if (!w || !w.last_delivery_at || !w.last_result)
    return { kind: 'off', word: 'no delivery yet' };
  if (w.last_result === 'ok') return { kind: 'ok', word: 'delivered' };
  if (w.last_result === 'missed') return { kind: 'failed', word: 'delivery missed' };
  return { kind: 'failed', word: 'delivery failed' };
}

/** The last delivery as a status, for a table cell. */
export function DeliveryStatus({ r }: { r: Repository }) {
  const s = deliveryStatus(r);
  return <Status kind={s.kind} word={s.word} />;
}

/** The delivery time and result in words, with how long ago. */
export function DeliveryWhen({ r }: { r: Repository }) {
  const now = useNow();
  const w = r.webhook;
  const s = deliveryStatus(r);
  return (
    <span>
      <Status kind={s.kind} word={s.word} />
      {w?.last_delivery_at && !w.webhooks_off
        ? ` ${formatPast(w.last_delivery_at, now)}`
        : ''}
    </span>
  );
}

/** What the learning source is, in the repository facts. */
export function learnedBy(r: Repository): string {
  return r.webhook?.mode === 'polling' || r.webhook?.webhooks_off
    ? 'polling'
    : 'webhooks';
}

/** Words for a missed delivery, or for an App that has no webhook. Empty when all is well. */
export function deliveryNotice(r: Repository): string | null {
  const w = r.webhook;
  if (!w) return null;
  if (w.webhooks_off)
    return 'The GitHub App of this controller has no webhook, so GitHub tells crsp nothing. crsp finds jobs by polling GitHub every 15 seconds, and jobs still run. Nothing needs fixing unless you want jobs to start at once. You can turn on webhooks in Settings.';
  if (w.last_result === 'missed' && w.mode !== 'polling')
    return 'GitHub did not tell crsp about a job; crsp found it by polling, so it ran a little late. The webhook of the GitHub App cannot reach this controller. In the GitHub App settings, check that the webhook address is public and starts with https, and look at Recent Deliveries for the red mark.';
  return null;
}

/** The polling notice with the switch back. Nothing is drawn while deliveries work. */
export function PollingNotice({
  r,
  onChanged,
}: {
  r: Repository;
  onChanged: () => void;
}) {
  const client = useClient();
  const session = useSession();
  const back = useCommand(async () => {
    await unwrap(
      client.PUT('/api/v1/repositories/{repositoryId}/webhook-mode', {
        params: { path: { repositoryId: r.id } },
        body: { mode: 'webhook' },
      }),
    );
    onChanged();
  });
  const notice = deliveryNotice(r);
  if (r.webhook?.mode !== 'polling')
    return notice ? (
      <Message tone="note">
        {notice}
        {r.webhook?.webhooks_off ? (
          <>
            {' '}
            <TextLink to="/settings/access#webhooks">Turn on webhooks</TextLink>
          </>
        ) : null}
      </Message>
    ) : null;
  return (
    <Stack>
      <Message tone="needs-you">
        GitHub deliveries to this repository failed {r.webhook.failures_in_a_row} times in
        a row, so crsp now polls GitHub for its jobs. Jobs still run, but they start a
        little later.
      </Message>
      <Button
        onPress={() => void back.run()}
        busy={back.busy}
        busyLabel="Switching"
        disabled={!session.can('admin')}
        reason={session.whyNot('admin')}
      >
        Switch back to webhooks
      </Button>
      {back.error ? (
        <Message tone="problem">Could not switch back. {back.error}</Message>
      ) : null}
    </Stack>
  );
}
