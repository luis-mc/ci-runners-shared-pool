// Sign-in page (web-console 4.1): signed out, signing in, failed, no access (R5.8..R5.10).
import {
  ActionBar,
  Button,
  Message,
  Page,
  PageHeader,
  Stack,
  Text,
  TextLink,
} from '@crsp/ui';
import { useState } from 'react';
import { Navigate, useLocation, useSearchParams } from 'react-router-dom';
import { unwrap, useClient } from '../api/api';
import { useLoad } from '../api/hooks';
import { CallbackGuide } from './CallbackGuide';
import { signInUrl, safeReturnPath, useSession } from './session';

const SIGN_IN_FAILURES: Record<string, string> = {
  state_missing: 'The sign-in took too long or was already used. Try again.',
  state_mismatch: 'The sign-in started on another address. Try again from this address.',
  address_refused:
    'Sign-in does not work on this address. Use https, or http://127.0.0.1 on the PC that runs the controller.',
  exchange_failed: 'GitHub did not accept the sign-in. Try again.',
  user_failed: 'GitHub did not say who you are. Try again.',
  session_failed: 'The controller could not start your session. Try again.',
  handoff_failed:
    'The sign-in could not return to the address you started on. Try again.',
  no_app: 'This controller has no GitHub App yet. Open the setup page to create it.',
  secret_store_unavailable: 'The OS secret store is not available. See the doctor.',
  sign_in_failed: 'Sign-in failed. Try again.',
};

function failureText(code: string): string {
  return SIGN_IN_FAILURES[code] ?? 'Sign-in failed. Try again.';
}

export function SignInPage() {
  const session = useSession();
  const client = useClient();
  const [params] = useSearchParams();
  const location = useLocation();
  const [starting, setStarting] = useState(false);
  const from = (location.state as { from?: string } | null)?.from;
  const returnTo = safeReturnPath(params.get('return_to') ?? from);
  const failed = params.get('error');
  const { result: setup } = useLoad(() => unwrap(client.GET('/api/v1/setup')), []);

  if (session.state.status === 'ready') return <Navigate to={returnTo} replace />;

  if (session.state.status === 'no-access') {
    const login = session.state.login;
    return <NoAccess login={login} onSignOut={() => void session.signOut()} />;
  }

  const noApp = setup.state === 'ready' && !setup.data.has_app;

  return (
    <main>
      <Page frame="narrow">
        <PageHeader title="Sign in" lead="CI Runners Shared Pool" />
        {failed === 'link_expired' || failed === 'link_local_only' ? (
          <Message tone="problem">
            {failed === 'link_expired'
              ? 'The sign-in link from the tray expired or was already used. Choose Open in the tray again, or sign in with GitHub.'
              : 'The sign-in link from the tray works on the computer that made it only. Sign in with GitHub here.'}
          </Message>
        ) : failed === 'callback_not_registered' ? (
          <Stack>
            <Message tone="needs-you">
              {`Sign-in does not work on ${params.get('address') ?? 'this address'} yet. Open the console on the PC that runs it at http://127.0.0.1:8460, or add this address to the GitHub App.`}
            </Message>
            {params.get('callback') ? (
              <CallbackGuide
                callbackUrl={params.get('callback') ?? ''}
                appUrl={params.get('app_url') ?? undefined}
              />
            ) : null}
          </Stack>
        ) : failed ? (
          <Message tone="problem">
            {failed === 'not_allowed'
              ? `${params.get('login') ?? 'This GitHub account'} is not on the list. Ask the owner to add you.`
              : failureText(failed)}
          </Message>
        ) : null}
        {noApp ? (
          <Message tone="needs-you">
            This controller has no GitHub App yet. Open the setup page to create it.
          </Message>
        ) : null}
        <Stack>
          <Text>Sign in with the GitHub account the owner allowed.</Text>
          {noApp ? <TextLink to="/setup?wizard=1">Open the setup page</TextLink> : null}
        </Stack>
        <ActionBar
          primary={
            <Button
              kind="primary"
              busy={starting}
              busyLabel="Signing in"
              disabled={noApp}
              reason={noApp ? 'Create the GitHub App first.' : undefined}
              onPress={() => {
                setStarting(true);
                window.location.assign(signInUrl(returnTo));
              }}
            >
              {failed === 'callback_not_registered' ? 'Try again' : 'Sign in with GitHub'}
            </Button>
          }
        />
      </Page>
    </main>
  );
}

export function NoAccess({ login, onSignOut }: { login: string; onSignOut: () => void }) {
  return (
    <main>
      <Page frame="narrow">
        <PageHeader title="No access" />
        <Message tone="needs-you">
          Signed in as {login || 'this account'}. This GitHub account has no access. Ask
          the owner to add it.
        </Message>
        <ActionBar
          primary={
            <Button kind="primary" onPress={onSignOut}>
              Sign out
            </Button>
          }
        />
      </Page>
    </main>
  );
}
