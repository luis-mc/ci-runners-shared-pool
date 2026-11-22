// Switch to crsp runners (github-integration R5.109, web-console R5.110). One pane on a repository's page and one on
// the Repositories list. It shows the files and the diff of the runs-on change, opens the pull request only when
// the owner presses the button, then links to it. Nothing here merges, and nothing opens a pull request by itself.
import { Button, CodeBlock, ExternalButton, Message, Pane, Stack, Text } from '@crsp/ui';
import { useState } from 'react';
import { unwrap, useClient, type Schemas } from '../../api/api';
import { useCommand } from '../../api/hooks';
import { useSession } from '../../auth/session';

type Preview = Schemas['RunsOnPullRequestPreview'];
type Result = Schemas['RunsOnPullRequestResult'];

/** The words of one preview line (content.md, Runs-on pull request words). */
export function previewLine(p: Preview): string {
  switch (p.state) {
    case 'change':
      return `${p.files.length} ${p.files.length === 1 ? 'file changes' : 'files change'} in ${p.full_name}.`;
    case 'nothing_to_change':
      return `Nothing to change in ${p.full_name}.`;
    case 'not_owned':
      return `${p.full_name} is not yours, so crsp opens no pull request there.`;
    default:
      return `Could not read ${p.full_name}. ${p.message ?? ''}`.trim();
  }
}

/** The words of one result line. */
export function resultLine(r: Result): string {
  switch (r.state) {
    case 'opened':
      return `Pull request opened on ${r.full_name}.`;
    case 'existing':
      return `A pull request is already open on ${r.full_name}.`;
    case 'nothing_to_change':
      return `Nothing to change in ${r.full_name}.`;
    case 'not_owned':
      return `${r.full_name} is not yours, so crsp opens no pull request there.`;
    default:
      return `Could not open the pull request on ${r.full_name}. ${r.message ?? ''}`.trim();
  }
}

export function RunsOnPrPane({ repositoryId }: { repositoryId?: number }) {
  const client = useClient();
  const session = useSession();
  const all = repositoryId === undefined;
  const [previews, setPreviews] = useState<Preview[] | undefined>(undefined);
  const [results, setResults] = useState<Result[] | undefined>(undefined);

  const preview = useCommand(async () => {
    setResults(undefined);
    const r = await unwrap(
      client.GET('/api/v1/runs-on-pr', {
        params: { query: all ? {} : { repository_id: repositoryId } },
      }),
    );
    setPreviews(r.items);
  });
  const open = useCommand(async () => {
    const r = await unwrap(
      client.POST('/api/v1/runs-on-pr', {
        body: all ? { all: true } : { repository_id: repositoryId },
      }),
    );
    setPreviews(undefined);
    setResults(r.items);
  });

  const why = session.whyNot('admin');
  const changing = (previews ?? []).filter((p) => p.state === 'change');
  const busy = preview.busy || open.busy;
  const action = all ? 'Switch all to crsp runners' : 'Switch to crsp runners';

  return (
    <Pane title="Switch to crsp runners">
      <Stack>
        <Text>
          {all
            ? 'crsp opens one pull request on each repository with the pool on. It changes each routed runs-on line to its own runner label. You review and merge it; crsp never merges.'
            : 'crsp opens one pull request on this repository. It changes each routed runs-on line to its own runner label. You review and merge it; crsp never merges.'}
        </Text>
        {previews === undefined && results === undefined ? (
          <Button
            onPress={() => void preview.run()}
            busy={preview.busy}
            busyLabel="Reading the workflows"
            disabled={why !== undefined}
            reason={why}
          >
            {action}
          </Button>
        ) : null}
        {preview.error ? (
          <Message tone="problem">Could not read the change. {preview.error}</Message>
        ) : null}
        {previews !== undefined ? (
          <Stack>
            {previews.length === 0 ? (
              <Message tone="note">No repository has the pool on.</Message>
            ) : null}
            {previews.map((p) => (
              <Stack key={p.repository_id}>
                <Text>{previewLine(p)}</Text>
                {p.state === 'change' ? (
                  <CodeBlock text={p.diff} label={`Change in ${p.full_name}`} />
                ) : null}
                {p.skipped.length > 0 ? (
                  <Message tone="note">
                    Not changed (runs-on is not on one line): {p.skipped.join(', ')}
                  </Message>
                ) : null}
              </Stack>
            ))}
            {changing.length > 0 ? (
              <Button
                onPress={() => void open.run()}
                busy={open.busy}
                busyLabel="Opening"
                disabled={busy}
              >
                {changing.length === 1
                  ? 'Open the pull request'
                  : 'Open the pull requests'}
              </Button>
            ) : null}
          </Stack>
        ) : null}
        {open.error ? (
          <Message tone="problem">Could not open the pull request. {open.error}</Message>
        ) : null}
        {results !== undefined
          ? results.map((r) => (
              <Message
                key={r.repository_id}
                tone={r.state === 'error' ? 'problem' : 'done'}
                action={
                  r.url ? (
                    <ExternalButton href={r.url}>Open on GitHub</ExternalButton>
                  ) : undefined
                }
              >
                {resultLine(r)}
              </Message>
            ))
          : null}
      </Stack>
    </Pane>
  );
}
