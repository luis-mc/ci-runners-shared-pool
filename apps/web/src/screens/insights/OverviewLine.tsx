// The Overview's best next change (LOOK56, option D): the top suggestion with its saving, a link to it and a link to
// the whole list in Analytics > Suggestions. It has no button (UX-52).
import { Section, Text, TextLink } from '@crsp/ui';
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
      <Text>
        <TextLink to={findingPath(top)}>{titleWithRepository(top)}</TextLink>
      </Text>
      <Text tone="muted">{`Saves ${minutesWords(top.minutes_saved_week)} (estimate). Fix: ${top.fix}`}</Text>
      <TextLink to="/analytics">
        {`See all ${items.length} in Analytics > Suggestions`}
      </TextLink>
    </Section>
  );
}
