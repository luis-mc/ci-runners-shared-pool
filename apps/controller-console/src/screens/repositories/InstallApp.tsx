// Installing the App on more repositories, in the console (web-console 4.3, UX-6). The button opens GitHub's
// install page for the App in a new tab. When the user comes back to this tab, the page reads the list again.
// GitHub's own return (the App's setup URL, with setup_action) lands on /repositories and is told there.
import { Button, Message, Stack } from '@crsp/ui';
import { useEffect, useRef, useState } from 'react';
import { unwrap, useClient } from '../../api/api';
import { useCommand } from '../../api/hooks';
import { useSession } from '../../auth/session';

export function InstallAppButton({
  label = 'Add a repository',
  onReturn,
}: {
  label?: string;
  /** Called once each time the user comes back to this tab after opening GitHub. */
  onReturn?: (() => void) | undefined;
}) {
  const client = useClient();
  const session = useSession();
  const [opened, setOpened] = useState(false);
  const back = useRef(onReturn);
  back.current = onReturn;
  const open = useCommand(async () => {
    const r = await unwrap(client.GET('/api/v1/github/install-url'));
    window.open(r.url, '_blank', 'noopener');
    setOpened(true);
  });
  useEffect(() => {
    if (!opened) return;
    const returned = () => {
      if (document.visibilityState === 'hidden') return;
      setOpened(false);
      back.current?.();
    };
    window.addEventListener('focus', returned);
    document.addEventListener('visibilitychange', returned);
    return () => {
      window.removeEventListener('focus', returned);
      document.removeEventListener('visibilitychange', returned);
    };
  }, [opened]);
  return (
    <Stack gap="tight">
      <Button
        onPress={() => void open.run()}
        busy={open.busy}
        busyLabel="Opening GitHub"
        disabled={!session.can('admin')}
        reason={session.whyNot('admin')}
      >
        {label}
      </Button>
      {opened ? (
        <Message tone="note">
          GitHub is open in a new tab. Pick the account and the repositories there, then
          come back to this tab. The list refreshes by itself.
        </Message>
      ) : null}
      {open.error ? (
        <Message tone="problem">Could not open GitHub. {open.error} Try again.</Message>
      ) : null}
    </Stack>
  );
}
