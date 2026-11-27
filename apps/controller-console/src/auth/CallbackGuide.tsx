// The guided step "Add this sign-in address to your GitHub App" (github-integration R5.8, OPS16). GitHub has no
// way to add a callback URL for the person, so the step gives the exact URL with a Copy button and a button to
// the App's settings page. Only shared parts of @crsp/ui are used (UX-8, UX-9).
import { CodeBlock, ExternalButton, Stack, Text } from '@crsp/ui';

/** Only a GitHub page is offered as a link; the controller checks it too. */
export function safeAppUrl(v: string | null | undefined): string | undefined {
  if (!v) return undefined;
  try {
    const u = new URL(v);
    return u.protocol === 'https:' && u.host === 'github.com' && !u.username
      ? u.toString()
      : undefined;
  } catch {
    return undefined;
  }
}

export function CallbackGuide({
  callbackUrl,
  appUrl,
}: {
  callbackUrl: string;
  appUrl?: string | undefined;
}) {
  const link = safeAppUrl(appUrl);
  return (
    <Stack>
      <Text>
        GitHub only signs people in on addresses the GitHub App knows. It does not know
        this one yet. Add this callback URL to the App: copy it, open the App's settings
        on GitHub, and under Callback URL choose Add Callback URL.
      </Text>
      <CodeBlock text={callbackUrl} label="Callback URL" />
      {link ? <ExternalButton href={link}>Open the App on GitHub</ExternalButton> : null}
    </Stack>
  );
}
