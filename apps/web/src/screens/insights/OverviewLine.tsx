// The Overview's best next change (LOOK56, option D; LOOK61): one line, the top suggestion with its saving and the
// link to it on the right. It has no button (UX-52).
import { Cluster, Section, Stack, Strong, Text, TextLink } from '@crsp/ui';
import { findingPath } from './DoNext';
import { useFindings } from './api';
import { minutesWords, ranked, titleWithRepository } from './words';

export function BestNextChange() {
  const { result } = useFindings(undefined);
  if (result.state !== 'ready') return null;
  const items = ranked(result.data.items);
  const top = items[0];
  if (!top) return null;
  return (
    <Section title="Best next change" count={items.length}>
      <Cluster>
        <Stack gap="tight">
          <Text>
            <Strong>{titleWithRepository(top)}</Strong>
          </Text>
          <Text tone="muted">{`Saves ${minutesWords(top.minutes_saved_week)} (estimate).`}</Text>
        </Stack>
        <TextLink to={findingPath(top)}>Open it in Analytics &gt; Suggestions</TextLink>
      </Cluster>
    </Section>
  );
}
