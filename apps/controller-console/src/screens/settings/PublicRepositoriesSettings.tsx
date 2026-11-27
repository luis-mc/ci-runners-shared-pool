// Settings, Repositories (web-console R5.68; github-integration R5.54 to R5.57): the one switch "Enable for
// public repositories". Off by default. Both directions ask first.
import {
  Facts,
  Loading,
  Message,
  Section,
  Stack,
  Switch,
  Text,
  formatDateTime,
} from '@crsp/ui';
import { useSession } from '../../auth/session';
import {
  usePublicRepositories,
  usePublicSwitch,
} from '../repositories/publicRepositories';

export function PublicRepositoriesSettings() {
  const session = useSession();
  const { setting, reload } = usePublicRepositories();
  const sw = usePublicSwitch(setting, reload);
  return (
    <Section title="Public repositories">
      <Stack>
        {setting ? (
          <>
            <Switch
              label="Enable for public repositories"
              on={setting.enabled}
              busy={sw.busy}
              disabledReason={session.whyNot('owner')}
              onChange={sw.ask}
            />
            <Text>
              {setting.enabled
                ? 'On: the pool can serve public repositories you turn it on for. Jobs from forks stay refused.'
                : 'Off: the pool serves private repositories only. Keeping repositories private is safer.'}
            </Text>
            {setting.changed_at ? (
              <Facts
                items={[
                  { name: 'Last changed', value: formatDateTime(setting.changed_at) },
                  ...(setting.changed_by
                    ? [{ name: 'Changed by', value: setting.changed_by }]
                    : []),
                ]}
              />
            ) : null}
            {sw.stopped !== undefined && sw.stopped > 0 ? (
              <Message tone="done">
                {`The pool was switched off for ${sw.stopped} public ${sw.stopped === 1 ? 'repository' : 'repositories'}. Their settings are kept.`}
              </Message>
            ) : null}
            {sw.error ? (
              <Message tone="problem">
                Could not save Enable for public repositories. {sw.error}
              </Message>
            ) : null}
            {sw.dialog}
          </>
        ) : (
          <Loading what="Loading Enable for public repositories" />
        )}
      </Stack>
    </Section>
  );
}
