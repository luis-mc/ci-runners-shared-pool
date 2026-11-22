// One line on the Overview: how many things to do next and the minutes a week. It links to Do next and has no
// button (UX-52).
import { Text, TextLink } from '@crsp/ui';
import { useInsightSummary } from './api';

export function OverviewInsights() {
  const { result } = useInsightSummary();
  if (result.state !== 'ready' || result.data.open === 0) return null;
  const { open, minutes_saved_week } = result.data;
  return (
    <Text>
      <TextLink to="/analytics/insights">
        {`${open} ${open === 1 ? 'thing' : 'things'} to do next, about ${minutes_saved_week} min a week`}
      </TextLink>
    </Text>
  );
}
