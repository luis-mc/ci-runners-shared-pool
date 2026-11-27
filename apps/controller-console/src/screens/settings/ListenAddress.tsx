// Settings > Console > Network, and the dialog that chooses the console address (operations R5.90).
// The owner chooses the address the controller listens on in the console, never with a terminal. The choice is
// kept and the controller restarts once through the drain path (running jobs finish first); the dialog waits and
// says when the controller answers on the new address. Only shared parts of @crsp/ui are used (UX-9, UX-13,
// UX-22, UX-38, UX-44).
import {
  Button,
  ChoiceField,
  Cluster,
  Dialog,
  Facts,
  Message,
  Section,
  Stack,
  Text,
  TextField,
} from '@crsp/ui';
import { useEffect, useRef, useState } from 'react';
import { unwrap, useClient, type Schemas } from '../../api/api';
import { useCommand, useLoad } from '../../api/hooks';
import { CallbackGuide } from '../../auth/CallbackGuide';
import { useSession } from '../../auth/session';
import { TIMING } from '../../timing';
import { Load } from '../common';

type Listen = Schemas['ListenAddress'];

const OTHER = 'other';

/** Settings > Console > Network. */
export function NetworkSettings() {
  const client = useClient();
  const session = useSession();
  const { result, reload } = useLoad(
    () => unwrap(client.GET('/api/v1/settings/listen-address')),
    [],
  );
  const [open, setOpen] = useState(false);
  return (
    <Section title="Network">
      <Load result={result} what="the controller address" reload={reload}>
        {(l) => (
          <Stack>
            <Text>
              The address runners and machines reach this controller at. The local console
              and the tray always keep 127.0.0.1.
            </Text>
            <Facts
              items={[
                { name: 'Listening on', value: l.running },
                ...(l.stored && l.stored !== l.running
                  ? [{ name: 'Chosen, after the next restart', value: l.stored }]
                  : []),
              ]}
            />
            {l.from_env ? (
              <Message tone="note">
                This address is set by {l.env_name ?? 'CRSP_LISTEN_ADDR'} on this PC, so
                it cannot be changed here.
              </Message>
            ) : null}
            {l.problem ? <Message tone="problem">{l.problem}</Message> : null}
            <Cluster>
              <Button
                onPress={() => setOpen(true)}
                disabled={l.from_env || !session.can('admin')}
                reason={
                  session.whyNot('admin') ??
                  (l.from_env
                    ? `The address is set by ${l.env_name ?? 'CRSP_LISTEN_ADDR'}.`
                    : undefined)
                }
              >
                Choose an address
              </Button>
            </Cluster>
          </Stack>
        )}
      </Load>
      {open ? (
        <ChooseAddressDialog onClose={() => setOpen(false)} onChosen={() => reload()} />
      ) : null}
    </Section>
  );
}

type Phase = 'choosing' | 'guide' | 'applying' | 'done';
type SignInAddress = Schemas['SignInAddress'];

/** The dialog: three choices as a radio group, Cancel left, Use this address right (UX-44). */
export function ChooseAddressDialog({
  onClose,
  onChosen,
}: {
  onClose: () => void;
  onChosen: () => void;
}) {
  const client = useClient();
  const { result, reload } = useLoad(
    () => unwrap(client.GET('/api/v1/settings/listen-address')),
    [],
  );
  const [choice, setChoice] = useState<string | undefined>(undefined);
  const [typed, setTyped] = useState('');
  const [phase, setPhase] = useState<Phase>('choosing');
  const [chosen, setChosen] = useState('');
  const [failure, setFailure] = useState<string | undefined>(undefined);
  const [guide, setGuide] = useState<SignInAddress | undefined>(undefined);
  const alive = useRef(true);
  useEffect(() => {
    alive.current = true;
    return () => {
      alive.current = false;
    };
  }, []);

  const data: Listen | undefined = result.state === 'ready' ? result.data : undefined;
  const candidates = data?.candidates ?? [];
  const selected =
    choice ?? candidates.find((c) => c.recommended)?.id ?? candidates[0]?.id;
  const picked = candidates.find((c) => c.id === selected);
  const address = picked?.id === OTHER ? typed.trim() : (picked?.address ?? '');

  // The controller restarts: poll until it answers on the chosen address.
  const follow = async (want: string) => {
    for (;;) {
      await new Promise((r) => setTimeout(r, TIMING.planPollMs));
      if (!alive.current) return;
      try {
        const l = await unwrap(client.GET('/api/v1/settings/listen-address'));
        if (l.running === want) {
          setPhase('done');
          onChosen();
          return;
        }
        if (l.problem) {
          setFailure(l.problem);
          setPhase('choosing');
          reload();
          return;
        }
      } catch {
        // The controller is restarting: keep waiting.
      }
    }
  };

  const put = async () => {
    const out = await unwrap(
      client.PUT('/api/v1/settings/listen-address', { body: { address } }),
    );
    setChosen(out.address);
    setPhase('applying');
    void follow(out.address);
  };

  // Before switching, GitHub must know the sign-in address of the new console address (OPS16): a dry sign-in
  // asks it. When it does not, the guided step gives the callback URL to add; Try again asks once more.
  const apply = useCommand(async () => {
    setFailure(undefined);
    const info = await unwrap(
      client.POST('/api/v1/settings/signin-address', { body: { address } }),
    );
    if (!info.registered) {
      setGuide(info);
      setPhase('guide');
      return;
    }
    await put();
  });

  const title = 'Choose an address';
  if (phase === 'done')
    return (
      <Dialog title={title} onClose={onClose} cancelLabel="Close">
        <Message tone="done">
          The controller answers on {chosen}. It also keeps 127.0.0.1 for this console and
          the tray.
        </Message>
      </Dialog>
    );
  if (phase === 'guide' && guide)
    return (
      <Dialog
        title="Add this sign-in address to your GitHub App"
        onClose={onClose}
        primary={{
          label: 'Try again',
          onPress: () => void apply.run(),
          busy: apply.busy,
          busyLabel: 'Asking GitHub',
        }}
      >
        <Stack>
          <CallbackGuide callbackUrl={guide.callback_url} appUrl={guide.app_url} />
          <Text>
            {`When you have added it, press Try again. The controller then tries a sign-in for ${guide.address} and switches to it. Until then it stays on ${data?.running ?? 'the old address'}.`}
          </Text>
          {apply.error ? (
            <Message tone="problem">Could not check the address. {apply.error}</Message>
          ) : (
            <Message tone="note">
              GitHub does not know this address yet. Pressing Try again before you added
              it changes nothing.
            </Message>
          )}
        </Stack>
      </Dialog>
    );
  if (phase === 'applying')
    return (
      <Dialog
        title={title}
        onClose={onClose}
        cancelLabel="Hide"
        primary={{
          label: 'Use this address',
          onPress: () => undefined,
          busy: true,
          busyLabel: 'Restarting',
        }}
      >
        <Message tone="note">
          {`Waiting for running jobs to finish, then the controller restarts on ${chosen}. No job is stopped. This page reopens here when the controller answers.`}
        </Message>
      </Dialog>
    );

  return (
    <Dialog
      title={title}
      onClose={onClose}
      primary={{
        label: 'Use this address',
        onPress: () => void apply.run(),
        busy: apply.busy,
        busyLabel: 'Saving',
        disabled: !address || Boolean(data?.from_env),
        reason: data?.from_env
          ? `The address is set by ${data.env_name ?? 'CRSP_LISTEN_ADDR'}.`
          : !address
            ? 'Choose an address, or write one as host:port.'
            : undefined,
      }}
    >
      <Load result={result} what="the addresses of this PC" reload={reload}>
        {(l) => (
          <Stack>
            {l.from_env ? (
              <Message tone="note">
                This address is set by {l.env_name ?? 'CRSP_LISTEN_ADDR'} on this PC, so
                it cannot be changed here. It is {l.running}.
              </Message>
            ) : null}
            <ChoiceField
              label="Address of the controller"
              value={selected ?? ''}
              onChange={setChoice}
              choices={l.candidates.map((c) => ({
                value: c.id,
                label: c.label,
                ...(c.address ? { detail: c.address } : {}),
                description: c.who,
                ...(c.id === OTHER
                  ? {
                      extra: (
                        <TextField
                          label="Address as host:port"
                          value={typed}
                          onChange={setTyped}
                          help="An address of this PC, such as 192.168.1.20:8460. The port is 8460 unless you write another."
                        />
                      ),
                    }
                  : {}),
                ...(l.from_env ? { disabledReason: 'Set by the environment.' } : {}),
              }))}
            />
            <Text>
              The controller restarts once when you press Use this address. Running jobs
              finish first.
            </Text>
            {failure ? (
              <Message tone="problem">
                The controller did not listen on the new address. {failure}
              </Message>
            ) : null}
            {apply.error ? (
              <Message tone="problem">Could not use this address. {apply.error}</Message>
            ) : null}
          </Stack>
        )}
      </Load>
    </Dialog>
  );
}
