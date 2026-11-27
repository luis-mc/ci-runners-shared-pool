// The two Overview tiles of today's savings (observability R5.15, R5.16, R5.19; web-console 4.2). Each links to the
// Analytics report. The money tile says "estimate" and the date the rates were copied (R5.16).
import { StatGrid, StatTile, formatNumber } from '@crsp/ui';
import { unwrap, useClient } from '../../api/api';
import { useLoad } from '../../api/hooks';
import { SAVINGS_WORDS, money } from './words';

export function SavingsTiles() {
  const client = useClient();
  const { result } = useLoad(
    () =>
      unwrap(client.GET('/api/v1/savings', { params: { query: { range: 'today' } } })),
    [client],
  );
  const r = result.state === 'ready' ? result.data : undefined;
  const loading = result.state === 'loading';
  return (
    <StatGrid>
      <StatTile
        label={SAVINGS_WORDS.tileMinutes}
        value={r ? formatNumber(r.total.minutes) : undefined}
        unit={SAVINGS_WORDS.minutesUnit}
        loading={loading}
        to="/analytics/savings"
        linkName="Open the savings report"
      />
      <StatTile
        label={SAVINGS_WORDS.tileMoney}
        value={r ? money(r.total.money) : undefined}
        unit={SAVINGS_WORDS.estimate}
        changeText={r ? `Rates copied ${r.copied_on}` : undefined}
        loading={loading}
        to="/analytics/savings"
        linkName="Open the savings report"
      />
    </StatGrid>
  );
}
